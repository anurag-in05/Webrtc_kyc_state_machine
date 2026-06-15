// Package call is the per-call orchestrator: it owns the session (clock), the
// recorder tee, and the WebRTC peer for one call, and sequences their teardown.
// It sits above the leaf packages (session, recordclient, peer) so they stay a
// clean dependency DAG with no import cycle.
package call

import (
	"context"
	"log"
	"sync"

	"kyc-monorepo/internal/gateway/brainclient"
	"kyc-monorepo/internal/gateway/config"
	"kyc-monorepo/internal/gateway/control"
	"kyc-monorepo/internal/gateway/peer"
	"kyc-monorepo/internal/gateway/recordclient"
	"kyc-monorepo/internal/gateway/session"
	"kyc-monorepo/internal/gateway/stt"
	"kyc-monorepo/internal/gateway/tts"
	"kyc-monorepo/internal/gateway/turnloop"
	recorderpb "kyc-monorepo/proto"
)

// Call is one live call's resources.
type Call struct {
	sess *session.Session
	peer *peer.Peer
	rec  *recordclient.Client

	conn *control.Conn // attached when the browser opens /control
	sink *control.Sink // agent-audio sink

	ctx    context.Context // cancelled on teardown → aborts in-flight brain/STT/TTS
	cancel context.CancelFunc

	mu       sync.Mutex    // guards loopDone
	loopDone chan struct{} // closed when the turn loop exits; nil until StartTurnLoop

	once sync.Once // teardown happens exactly once (a /close racing a disconnect)
}

// Reoffer applies a re-offer (ICE restart) to the existing peer (CONTRACTS §2).
func (c *Call) Reoffer(offerSDP string) (string, error) {
	return c.peer.Reoffer(offerSDP)
}

// AttachControl binds the browser control socket to the call and builds the agent
// sink: it tees agent audio to the recorder as AGENT_PCM with the executor's
// call_us passed straight through (no clock sampling here). The turn loop (G7)
// drives conn.Inbox() and c.sink.
func (c *Call) AttachControl(conn *control.Conn) {
	c.conn = conn
	c.sink = control.NewSink(conn, func(pcm48 []byte, callUS uint64) {
		c.rec.SendAt(recorderpb.Kind_AGENT_PCM, callUS, pcm48)
	})
}

// Close tears the call down in one strict, idempotent (sync.Once) order, each step
// a prerequisite of the next:
//
//  1. cancel the call ctx — aborts in-flight brain/STT/TTS so the turn loop unwinds;
//  2. close the control socket — the turn loop's Inbox closes and its sends fail
//     gracefully (errors ignored), so it can't block;
//  3. wait for the turn loop to exit — by here it has cleared SetMic and finished
//     its last AGENT_PCM tee, so there is no agent producer left;
//  4. close the peer — its OnTrack read goroutines exit and flush the final video
//     access unit to the recorder (the last video producer);
//  5. close the recorder stream — now safe: no producer remains → half-close,
//     recorder flushes files;
//  6. finalize — kick the ffmpeg combine.
//
// A mid-utterance teardown is clean: step 1 aborts Speak, and the agent audio
// already streamed was tee'd to the recorder BEFORE the browser send (control.Sink),
// so the recording survives. The disconnect path (peer terminal state → onClose →
// Registry.End) and POST /close both route here.
func (c *Call) Close() {
	c.once.Do(func() {
		c.cancel()
		if c.conn != nil {
			c.conn.Close()
		}
		c.waitLoop()   // turn loop (and its AGENT_PCM tee) has stopped
		c.peer.Close() // stop peer; wait read goroutines → final video AU flushed
		c.rec.Close()  // no producer left → half-close the recorder stream
		if err := c.rec.Finalize(); err != nil {
			log.Printf("call %s: finalize: %v", c.sess.ID, err)
		}
	})
}

// waitLoop blocks until the turn loop goroutine has exited (no-op if it never
// started — e.g. a disconnect before /control).
func (c *Call) waitLoop() {
	c.mu.Lock()
	done := c.loopDone
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

// StartTurnLoop runs the turn loop for a call once its control socket is attached.
// It builds the concrete Deps from the call's resources + the gateway's clients,
// and records a done channel so Close can wait for the loop to unwind.
func (r *Registry) StartTurnLoop(c *Call) {
	done := make(chan struct{})
	c.mu.Lock()
	c.loopDone = done
	c.mu.Unlock()
	go func() {
		defer close(done)
		turnloop.Run(c.ctx, turnloop.Deps{
			SessionID: c.sess.ID,
			Conn:      c.conn,
			Sink:      c.sink,
			Clock:     c.sess.CallUS, // the one call-clock origin
			SetMic:    c.peer.SetMic, // open/close the listening window
			Brain:     r.brain,
			TTS:       r.tts,
			STT:       r.stt,
		})
	}()
}

// Registry holds the live calls (keyed by session id) and the gateway-wide control
// clients (one each, built from config).
type Registry struct {
	cfg   config.Config
	brain *brainclient.Client
	tts   *tts.Client
	stt   *stt.Client

	mu sync.Mutex
	m  map[string]*Call
}

func NewRegistry(cfg config.Config) *Registry {
	return &Registry{
		cfg:   cfg,
		brain: brainclient.New(cfg.BrainURL),
		tts:   tts.New(cfg.ElevenLabsAPIKey),
		stt:   stt.New(cfg.SarvamAPIKey),
		m:     make(map[string]*Call),
	}
}

func (r *Registry) Get(id string) (*Call, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.m[id]
	return c, ok
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}

// End tears a call down and unregisters it. Called by POST /close and by the
// peer's terminal-state callback (an unexpected disconnect). Idempotent:
// Call.Close is once-guarded and Remove of an absent id is a no-op.
func (r *Registry) End(id string) {
	if c, ok := r.Get(id); ok {
		c.Close()
		r.Remove(id)
	}
}

// Start creates a new call from the browser's offer — session (which stamps the
// call-clock origin), recorder tee, then the WebRTC peer — registers it, and
// returns the answer SDP.
func (r *Registry) Start(id, offerSDP string) (string, error) {
	sess := session.New(id)
	rec, err := recordclient.New(sess, r.cfg.RecorderGRPCAddr, r.cfg.RecorderHTTPURL)
	if err != nil {
		return "", err
	}
	// On a terminal peer state, tear the call down on a fresh goroutine (so the
	// callback can't re-enter Call.Close on the same goroutine).
	onClose := func() { go r.End(id) }
	answer, p, err := peer.New(offerSDP, peer.Config{
		TURNURL:        r.cfg.TURNURL,
		TURNUsername:   r.cfg.TURNUsername,
		TURNCredential: r.cfg.TURNCredential,
	}, rec, onClose)
	if err != nil {
		rec.Close()
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.m[id] = &Call{sess: sess, peer: p, rec: rec, ctx: ctx, cancel: cancel}
	r.mu.Unlock()
	return answer, nil
}
