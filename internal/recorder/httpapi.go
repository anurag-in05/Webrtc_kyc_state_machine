package recorder

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"sync"
)

// HTTPAPI is the recorder's control-plane HTTP surface (CONTRACTS §3):
//
//	POST /sessions/{id}/finalize  — kick the ffmpeg combine (async, returns now)
//	GET  /sessions/{id}/status    — poll the finalize result
//
// It shares nothing mutable with the gRPC ingest server; they only have the
// recordings directory (a path) in common. The finalize status lives entirely
// here, since only these two endpoints touch it.
type HTTPAPI struct {
	dir string
	s3  *S3Uploader // nil when S3 is unconfigured → finalize keeps local URLs

	mu     sync.Mutex
	status map[string]Status // session_id → latest finalize status
}

// Status is the GET /status response body (CONTRACTS §3).
type Status struct {
	State string `json:"status"` // pending|complete|partial|audio_only|failed
	URL   string `json:"url"`    // path/URL to full_call.mp4, or ""
}

func NewHTTPAPI(dir string, s3 *S3Uploader) *HTTPAPI {
	return &HTTPAPI{dir: dir, s3: s3, status: map[string]Status{}}
}

func (h *HTTPAPI) Handler() http.Handler {
	mux := http.NewServeMux() // Go 1.22 method+pattern routing
	mux.HandleFunc("POST /sessions/{id}/finalize", h.handleFinalize)
	mux.HandleFunc("GET /sessions/{id}/status", h.handleStatus)
	return mux
}

// handleFinalize marks the session pending, kicks the combine on a goroutine,
// and returns 202 immediately (CONTRACTS §3: "Returns immediately").
func (h *HTTPAPI) handleFinalize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.set(id, Status{State: statusPending})
	go h.runFinalize(id)
	w.WriteHeader(http.StatusAccepted)
}

func (h *HTTPAPI) runFinalize(id string) {
	state, out := combine(filepath.Join(h.dir, id))
	// Default the URL to the local path; on a produced mp4 with S3 configured,
	// stream it up and front it with the AWS URL. A failed/disabled upload keeps
	// the local path (invariant 4: degrade the recording, never fail the call).
	// The local file stays put either way.
	url := out
	if out != "" && h.s3 != nil {
		if s3url, err := h.s3.upload(context.Background(), id, out); err != nil {
			log.Printf("recorder: S3 upload failed for %s: %v; using local path", id, err)
		} else {
			url = s3url
		}
	}
	h.set(id, Status{State: state, URL: url})
}

func (h *HTTPAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, ok := h.get(id)
	if !ok {
		s = Status{State: statusPending} // unknown id = not finalized yet
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

func (h *HTTPAPI) set(id string, s Status) {
	h.mu.Lock()
	h.status[id] = s
	h.mu.Unlock()
}

func (h *HTTPAPI) get(id string) (Status, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.status[id]
	return s, ok
}
