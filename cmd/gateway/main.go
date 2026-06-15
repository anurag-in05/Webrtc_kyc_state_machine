// Command gateway is the media plane: it terminates the one WebRTC peer per
// call, runs STT/TTS, drives the turn loop, and tees media to the recorder.
// See docs/GATEWAY.md and CONTRACTS §2. Not an SFU — one human + one bot, no
// forwarding.
//
// As of G2b the offer SDP exchange + media tee are live (peer → recorder). STT,
// TTS, the turn loop, and the control WebSocket land in G3–G7; /control is a stub.
package main

import (
	"encoding/json"
	"log"
	"net/http"

	"kyc-monorepo/internal/gateway/call"
	"kyc-monorepo/internal/gateway/config"
	"kyc-monorepo/internal/gateway/control"
)

func main() {
	cfg := config.Load()
	reg := call.NewRegistry(cfg)

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

type sdp struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// offerHandler accepts the browser's send-only SDP offer (CONTRACTS §2) and
// returns the answer. A first offer starts the call (session clock, recorder tee,
// peer); a re-offer for a known session is an ICE restart on the existing peer,
// so the recording is not split.
func offerHandler(reg *call.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var offer sdp
		if err := json.NewDecoder(r.Body).Decode(&offer); err != nil || offer.SDP == "" {
			http.Error(w, "bad offer", http.StatusBadRequest)
			return
		}

		var answer string
		var err error
		if c, ok := reg.Get(id); ok {
			answer, err = c.Reoffer(offer.SDP) // re-offer → ICE restart
		} else {
			answer, err = reg.Start(id, offer.SDP)
		}
		if err != nil {
			log.Printf("offer %s: %v", id, err)
			http.Error(w, "offer failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sdp{SDP: answer, Type: "answer"})
	}
}

// controlHandler is the /control WebSocket (CONTRACTS §2): JSON control in, JSON
// events + binary agent audio out. It upgrades and attaches the socket to the
// existing call; the turn loop that drives it (greeting → turns → agent_done)
// lands in G7.
func controlHandler(reg *call.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := reg.Get(r.PathValue("id"))
		if !ok {
			http.Error(w, "unknown session", http.StatusNotFound) // /control before /offer
			return
		}
		conn, err := control.Upgrade(w, r)
		if err != nil {
			return // Upgrade already wrote the HTTP error
		}
		c.AttachControl(conn)
	}
}

// closeHandler tears the call down (CONTRACTS §2). Idempotent: a /close and a
// peer disconnect may race (guarded by Call.Close's sync.Once).
func closeHandler(reg *call.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reg.End(r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
	}
}
