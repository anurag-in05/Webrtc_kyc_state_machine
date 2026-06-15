// Package call is the per-call orchestrator: it owns the session (clock), the
// recorder tee, and the WebRTC peer for one call, and sequences their teardown.
// It sits above the leaf packages (session, recordclient, peer) so they stay a
// clean dependency DAG with no import cycle.
package call

import (
	"log"
	"sync"

	"kyc-monorepo/internal/gateway/config"
	"kyc-monorepo/internal/gateway/peer"
	"kyc-monorepo/internal/gateway/recordclient"
	"kyc-monorepo/internal/gateway/session"
)

// Call is one live call's resources.
type Call struct {
	sess *session.Session
	peer *peer.Peer
	rec  *recordclient.Client
	once sync.Once // teardown happens exactly once (a /close racing a disconnect)
}

// Reoffer applies a re-offer (ICE restart) to the existing peer (CONTRACTS §2).
func (c *Call) Reoffer(offerSDP string) (string, error) {
	return c.peer.Reoffer(offerSDP)
}

// Close tears the call down in order: stop the peer (its OnTrack read goroutines
// exit and flush their final frames to the recorder), then close the recorder
// stream, then kick the finalize combine. Idempotent.
func (c *Call) Close() {
	c.once.Do(func() {
		c.peer.Close() // waits for read goroutines → all media Sends done
		c.rec.Close()  // half-close the recorder stream (it flushes files)
		if err := c.rec.Finalize(); err != nil {
			log.Printf("call %s: finalize: %v", c.sess.ID, err)
		}
	})
}

// Registry holds the live calls, keyed by session id.
type Registry struct {
	cfg config.Config
	mu  sync.Mutex
	m   map[string]*Call
}

func NewRegistry(cfg config.Config) *Registry {
	return &Registry{cfg: cfg, m: make(map[string]*Call)}
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
	r.mu.Lock()
	r.m[id] = &Call{sess: sess, peer: p, rec: rec}
	r.mu.Unlock()
	return answer, nil
}
