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

// New dials the recorder gRPC (RECORDER_GRPC_ADDR) and opens this call's stream.
// The dial is lazy — a recorder that is down does not fail here; sends to it just
// drop (the call continues, the recording degrades). httpURL is
// RECORDER_HTTP_URL, used by Finalize.
func New(sess *session.Session, grpcAddr, httpURL string) (*Client, error) {
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("recordclient dial %s: %w", grpcAddr, err)
	}
	stream, err := recorderpb.NewRecorderClient(conn).RecordStream(context.Background())
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("recordclient open stream: %w", err)
	}
	c := &Client{
		sess:    sess,
		httpURL: httpURL,
		conn:    conn,
		stream:  stream,
		ch:      make(chan *recorderpb.Frame, sendBuffer),
		done:    make(chan struct{}),
	}
	go c.run()
	return c, nil
}

// Send stamps the frame from the call clock and enqueues it. Non-blocking: if the
// queue is full the frame is dropped (never block the media reader). The data is
// copied — the caller may reuse its buffer after this returns.
func (c *Client) Send(kind recorderpb.Kind, data []byte) {
	callUS := c.sess.CallUS() // THE only source of call_us
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

// run is the single sender goroutine: it drains the queue to the gRPC stream. A
// send failure means the recorder stream broke; we stop transmitting but keep
// draining so producers never block on a full queue. Degrades the recording;
// never the call.
func (c *Client) run() {
	defer close(c.done)
	broken := false
	for f := range c.ch {
		if broken {
			continue
		}
		if err := c.stream.Send(f); err != nil {
			log.Printf("recordclient: stream send failed, recording degraded: %v", err)
			broken = true
		}
	}
}

// Close flushes the stream and tears down the gRPC connection. Call it only after
// the media producers have stopped (teardown order) — like the recorder's
// videoWriter, it closes the send channel. The half-close makes the recorder see
// io.EOF and flush its files.
func (c *Client) Close() {
	close(c.ch)
	<-c.done
	ack, err := c.stream.CloseAndRecv()
	if err != nil {
		log.Printf("recordclient: close stream: %v", err)
	} else {
		log.Printf("recordclient: session %s recorded %d frames", c.sess.ID, ack.GetFrames())
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
