// Package recordclient tees one call's media to the recorder over gRPC
// (CONTRACTS §3) and kicks the finalize combine over HTTP. It is the only place
// the gateway talks to the recorder.
//
// call_us discipline (load-bearing). Every frame's call_us is stamped here from
// session.CallUS() — the single call-clock origin (CONTRACTS §3: one origin, all
// three streams). Callers pass only (kind, data); they cannot supply a call_us,
// so they cannot supply a wrong one. ts_us is DERIVED from the same clock
// (call_us minus this stream's first-frame call_us), so it adds no second time
// source. The recorder ignores ts_us for PCM and uses it only for video pacing.
package recordclient

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"kyc-monorepo/internal/gateway/session"
	recorderpb "kyc-monorepo/proto"
)

// sendBuffer bounds the in-flight frame queue. A full buffer means the recorder
// stream is wedged; we drop frames rather than block the media readers
// (invariant 4). ~512 frames ≈ several seconds of mixed video+audio.
const sendBuffer = 512

// Client owns the per-call RecordStream. One per session.
type Client struct {
	sess    *session.Session
	httpURL string

	conn   *grpc.ClientConn
	stream grpc.ClientStreamingClient[recorderpb.Frame, recorderpb.Ack]

	ch   chan *recorderpb.Frame
	done chan struct{}

	// Per-stream ts_us origin: the first frame's call_us for each kind. Each kind
	// is fed by a single goroutine (video reader / audio reader / agent speak), so
	// these need no lock.
	tsOrigin [3]uint64
	tsSet    [3]bool
}

// New dials the recorder gRPC (RECORDER_GRPC_ADDR). The dial is lazy and the
// stream is opened by the sender goroutine, NOT here — so a recorder that is down
// at call start does not fail the call (invariant 4): New returns cleanly and
// frames are simply dropped until/unless the stream opens. New errors only on a
// malformed address. httpURL is RECORDER_HTTP_URL, used by Finalize.
func New(sess *session.Session, grpcAddr, httpURL string) (*Client, error) {
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("recordclient dial %s: %w", grpcAddr, err)
	}
	c := &Client{
		sess:    sess,
		httpURL: httpURL,
		conn:    conn,
		ch:      make(chan *recorderpb.Frame, sendBuffer),
		done:    make(chan struct{}),
	}
	go c.run()
	return c, nil
}

// Send stamps the frame from the call clock and enqueues it — for video and user
// audio, which are sent the moment they're received. The caller supplies no
// timestamp, so it cannot supply a wrong one.
func (c *Client) Send(kind recorderpb.Kind, data []byte) {
	c.SendAt(kind, c.sess.CallUS(), data) // THE only source of call_us for this path
}

// SendAt enqueues a frame with an EXPLICIT call_us — for the agent path, where the
// plan executor samples session.CallUS() once per chunk and that single value must
// reach both the browser send and this recorder tee (so they carry the identical
// timestamp for the same bytes). call_us MUST come from session.CallUS() (the one
// origin); do not invent it. Non-blocking: a full queue drops the frame (never
// block the media reader). The data is copied — the caller may reuse its buffer.
func (c *Client) SendAt(kind recorderpb.Kind, callUS uint64, data []byte) {
	if !c.tsSet[kind] {
		c.tsOrigin[kind] = callUS
		c.tsSet[kind] = true
	}
	b := make([]byte, len(data))
	copy(b, data)
	f := &recorderpb.Frame{
		SessionId: c.sess.ID,
		Kind:      kind,
		TsUs:      callUS - c.tsOrigin[kind], // per-stream, derived from the same clock
		CallUs:    callUS,
		Data:      b,
	}
	select {
	case c.ch <- f:
	default:
		log.Printf("recordclient: queue full, dropping %v frame (recording degraded)", kind)
	}
}

// run is the single sender goroutine. It opens the stream (a down recorder here
// degrades the recording, not the call — invariant 4) and drains the queue to it.
// A send failure means the stream broke; we stop transmitting but keep draining
// so producers never block on a full queue.
func (c *Client) run() {
	defer close(c.done)
	stream, err := recorderpb.NewRecorderClient(c.conn).RecordStream(context.Background())
	if err != nil {
		log.Printf("recordclient: recorder stream unavailable, recording degraded: %v", err)
		for range c.ch { // drain-drop so producers never block
		}
		return
	}
	c.stream = stream
	broken := false
	for f := range c.ch {
		if broken {
			continue
		}
		if err := stream.Send(f); err != nil {
			log.Printf("recordclient: stream send failed, recording degraded: %v", err)
			broken = true
		}
	}
}

// Close flushes the stream and tears down the gRPC connection. Call it only after
// the media producers have stopped (teardown order) — like the recorder's
// videoWriter, it closes the send channel. The half-close makes the recorder see
// io.EOF and flush its files. c.stream is set by run() and read here only after
// <-c.done, so no lock is needed; it stays nil if the recorder was never reachable.
func (c *Client) Close() {
	close(c.ch)
	<-c.done
	if c.stream != nil {
		ack, err := c.stream.CloseAndRecv()
		if err != nil {
			log.Printf("recordclient: close stream: %v", err)
		} else {
			log.Printf("recordclient: session %s recorded %d frames", c.sess.ID, ack.GetFrames())
		}
	}
	c.conn.Close()
}

// Finalize asks the recorder to run the ffmpeg combine (CONTRACTS §3). Async on
// the recorder side — this returns as soon as the recorder accepts (202).
func (c *Client) Finalize() error {
	url := fmt.Sprintf("%s/sessions/%s/finalize", c.httpURL, c.sess.ID)
	resp, err := http.Post(url, "", nil)
	if err != nil {
		return fmt.Errorf("recordclient finalize: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("recordclient finalize: status %d", resp.StatusCode)
	}
	return nil
}
