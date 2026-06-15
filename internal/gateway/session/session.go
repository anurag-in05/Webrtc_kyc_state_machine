// Package session is the per-call clock. Every per-call goroutine — the
// video/audio OnTrack handlers (peer), the agent speak path (turnloop/tts) —
// stamps frames from one Session so they all share a single call clock. The
// live-call objects that own a Session are managed by package call.
//
// THE CALL CLOCK (load-bearing, CONTRACTS §3). The recorder aligns video,
// user audio, and agent audio off `call_us` alone, never off arrival time. For
// that to work, all three streams must carry microseconds measured from one
// shared origin. That origin is Session.start, set exactly once at call
// creation. Every frame is stamped via Session.CallUS(). There is no second
// time reference: a grep for time.Now() across internal/gateway must return
// exactly this one site. Two origins → silent drift, no error. Do not add another.
package session

import "time"

// Session is the per-call clock.
type Session struct {
	ID    string
	start time.Time // the ONE call-clock origin — set once, never reassigned
}

// New stamps the call-clock origin. This is the only time.Now() in the gateway
// by design (see package doc).
func New(id string) *Session {
	return &Session{ID: id, start: time.Now()}
}

// CallUS returns microseconds since this call's single origin. Stamp every
// VIDEO_AU / USER_PCM / AGENT_PCM frame from this — one origin, all three streams.
func (s *Session) CallUS() uint64 {
	return uint64(time.Since(s.start).Microseconds())
}
