// Command gateway is the media plane: it terminates the one WebRTC peer per
// call, runs STT/TTS, drives the turn loop, and tees media to the recorder.
// See docs/GATEWAY.md and CONTRACTS §2. Not an SFU — one human + one bot, no
// forwarding.
//
// G0 scaffold: config, the per-call clock (internal/gateway/session), and the
// HTTP surface (CONTRACTS §2). The peer (offer SDP exchange) lands in G2 and
// the control WebSocket in G5; their handlers are stubs here. /close already
// works so the session lifecycle (create-on-offer → remove-on-close) is real.
package main

import (
	"log"
	"net/http"

	"kyc-monorepo/internal/gateway/config"
	"kyc-monorepo/internal/gateway/session"
)

func main() {
	cfg := config.Load()
	reg := session.NewRegistry()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions/{id}/offer", offerHandler(reg))
	mux.HandleFunc("GET /sessions/{id}/control", controlHandler(reg))
	mux.HandleFunc("POST /sessions/{id}/close", closeHandler(reg))

	log.Printf("gateway listening on :%s (brain=%s recorder=%s)",
		cfg.Port, cfg.BrainURL, cfg.RecorderGRPCAddr)
	if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
		log.Fatalf("gateway: %v", err)
	}
}

// offerHandler accepts the browser's send-only SDP offer (CONTRACTS §2). The
// call clock's origin is set here, the moment the call begins. The Pion peer
// create/answer + OnTrack wiring lands in G2.
func offerHandler(reg *session.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s := reg.Create(id)
		log.Printf("offer %s: session created, call clock origin set (call_us=%d)", id, s.CallUS())
		http.Error(w, "peer wiring lands in G2", http.StatusNotImplemented)
	}
}

// controlHandler is the /control WebSocket (CONTRACTS §2): JSON control in,
// JSON events + binary agent audio out. Upgrade + protocol land in G5.
func controlHandler(reg *session.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "control websocket lands in G5", http.StatusNotImplemented)
	}
}

// closeHandler tears the call down (CONTRACTS §2). Idempotent: a /close and a
// peer disconnect may race. Recorder flush + finalize land with teardown in G7.
func closeHandler(reg *session.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		reg.Remove(id)
		w.WriteHeader(http.StatusOK)
	}
}
