// Package session holds the per-call state hub. Every per-call goroutine — the
// video/audio OnTrack handlers (peer), the agent speak path (turnloop/tts) —
// shares one Session so they all stamp frames from a single call clock.
//
// THE CALL CLOCK (load-bearing, CONTRACTS §3). The recorder aligns video,
// user audio, and agent audio off `call_us` alone, never off arrival time. For
// that to work, all three streams must carry microseconds measured from one
// shared origin. That origin is Session.start, set exactly once at call
// creation. Every frame is stamped via Session.CallUS(). There is no second
// time reference: a grep for time.Now() across internal/gateway must return
// exactly this one site. Two origins → silent drift, no error. Do not add another.
package session

import (
	"sync"
	"time"
)

// Session is the per-call hub. Fields beyond the clock (peer, sinks, cancel,
// closed) are added by later milestones; the clock is fixed here.
type Session struct {
	ID    string
	start time.Time // the ONE call-clock origin — set once, never reassigned
}

// newSession stamps the call-clock origin. This is the only time.Now() in the
// gateway by design (see package doc).
func newSession(id string) *Session {
	return &Session{ID: id, start: time.Now()}
}

// CallUS returns microseconds since this call's single origin. Stamp every
// VIDEO_AU / USER_PCM / AGENT_PCM frame from this — one origin, all three streams.
func (s *Session) CallUS() uint64 {
	return uint64(time.Since(s.start).Microseconds())
}

// Registry is the live-session map, guarded for concurrent HTTP handlers.
type Registry struct {
	mu sync.Mutex
	m  map[string]*Session
}

func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*Session)}
}

// Create makes a new session (stamping its clock origin) and registers it.
func (r *Registry) Create(id string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := newSession(id)
	r.m[id] = s
	return s
}

func (r *Registry) Get(id string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.m[id]
	return s, ok
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}
