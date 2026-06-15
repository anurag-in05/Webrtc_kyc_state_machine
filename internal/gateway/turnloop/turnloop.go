// Package turnloop is the driver (interpretation A — a port of the old
// _run_turn_loop). The brain decides WHAT to say; the turn loop decides WHEN. It
// greets, then for each start_turn runs STT on the live mic, asks the brain, sends
// the reply over the control WS, and speaks it — until the brain reports the
// session is no longer active or the socket drops.
//
// Degradation (hard-invariant 2): the recovery must not require the failed
// component. Failures the brain CAN respond to converge on please_repeat — an STT
// failure yields an empty transcript, which the brain turns into please_repeat just
// like an intent-classifier failure does. Failures of reaching the brain (transport
// error) or of voicing a reply (TTS error) degrade to silence-plus-signal — the
// gateway holds no phrases, so it can't synthesize please_repeat itself; it sends a
// control error + agent_done and the call continues.
package turnloop

import (
	"context"
	"log"
	"time"

	"kyc-monorepo/internal/gateway/brainclient"
	"kyc-monorepo/internal/gateway/control"
	"kyc-monorepo/internal/gateway/stt"
	"kyc-monorepo/internal/gateway/tts"
)

// ttsBudget bounds one Speak (connect + every segment) — a hang killer, passed as
// the ctx deadline. ElevenLabs streams faster than real-time, so even the long
// greeting completes well within it.
const ttsBudget = 30 * time.Second

// Deps is everything the turn loop drives — all concrete, wired by package call.
// The only interface here is AgentSink (the agent-audio sink).
type Deps struct {
	SessionID string
	Conn      *control.Conn
	Sink      tts.AgentSink
	Clock     func() uint64      // session.CallUS — the one origin, for per-chunk stamping
	SetMic    func(func([]byte)) // peer.SetMic — open/close the listening window
	Brain     *brainclient.Client
	TTS       *tts.Client
	STT       *stt.Client
}

// Run drives one call: greet, then loop over start_turn/end until the brain says
// the session is no longer active, the socket drops, or ctx is cancelled (teardown).
func Run(ctx context.Context, d Deps) {
	boot, err := d.Brain.Get(ctx, d.SessionID)
	if err != nil {
		log.Printf("turnloop %s: bootstrap: %v", d.SessionID, err)
		_ = d.Conn.SendError("could not start session")
		return
	}
	d.greet(ctx, boot)
	if boot.Status != "active" {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-d.Conn.Inbox():
			if !ok {
				return // socket dropped
			}
			switch msg.Type {
			case "end":
				return
			case "start_turn":
				if !d.runTurn(ctx, boot.Language) {
					return
				}
			}
		}
	}
}

// greet speaks the bootstrap greeting and signals turn 0 (interpretation A:
// brainclient.Get → speak → agent_done{turn:0}).
func (d Deps) greet(ctx context.Context, boot *brainclient.Snapshot) {
	if boot.AgentText != "" {
		if err := d.speak(ctx, boot.TTSPlan, boot.VoiceID, boot.ModelID); err != nil {
			log.Printf("turnloop %s: greeting tts: %v", d.SessionID, err)
			_ = d.Conn.SendError("greeting could not be voiced: " + boot.AgentText)
		}
	}
	_ = d.Conn.SendAgentDone(boot.TurnIndex, boot.Status)
}

// runTurn runs one turn; returns false when the session is no longer active.
func (d Deps) runTurn(ctx context.Context, language string) (active bool) {
	transcript := d.listen(ctx, language) // "" on any STT failure → please_repeat via the brain

	resp, err := d.Brain.Turn(ctx, d.SessionID, transcript)
	if err != nil {
		// Brain transport failure: no reply, and the gateway holds no phrases, so it
		// can't produce please_repeat itself → silence-plus-signal, call continues.
		log.Printf("turnloop %s: brain turn: %v", d.SessionID, err)
		_ = d.Conn.SendError("brain unavailable; please try again")
		_ = d.Conn.SendAgentDone(0, "active")
		return true
	}

	_ = d.sendFinal(resp)
	if resp.AgentText != "" {
		if err := d.speak(ctx, resp.TTSPlan, resp.VoiceID, resp.ModelID); err != nil {
			// Output-side failure: the reply couldn't be voiced. The brain already
			// advanced once; signal so the UI shows the already-sent final text.
			log.Printf("turnloop %s: reply tts: %v", d.SessionID, err)
			_ = d.Conn.SendError("agent reply could not be voiced: " + resp.AgentText)
		}
	}
	_ = d.Conn.SendAgentDone(resp.TurnIndex, resp.Status)
	return resp.Status == "active"
}

// listen runs STT for one turn over the live mic and returns the transcript ("" on
// any STT failure — the input-side fold to please_repeat). It opens the listening
// window, forwards vad, and on END_SPEECH closes the window FIRST then flushes.
func (d Deps) listen(ctx context.Context, language string) string {
	mic := stt.NewMicBuffer()
	d.SetMic(mic.Push) // open the listening window
	transcript := ""
	d.STT.Run(ctx, language, mic, func(ev stt.Event) {
		switch ev.Type {
		case "vad":
			_ = d.Conn.SendVAD(ev.Signal, ev.At)
			if ev.Signal == "END_SPEECH" {
				d.SetMic(nil)    // close the window FIRST (barrier: no Push after this returns)
				mic.CloseInput() // THEN flush to Sarvam
			}
		case "final":
			transcript = ev.Text
		case "error":
			transcript = "" // STT failure → empty → please_repeat via the brain
		}
	})
	d.SetMic(nil) // ensure the window is closed on the timeout / no-END_SPEECH path
	return transcript
}

// speak executes a tts_plan with a hard ctx deadline (ttsBudget bounds connect +
// streaming). Clock is the session clock, sampled once per chunk inside Speak.
func (d Deps) speak(ctx context.Context, plan []tts.PlanItem, voiceID, modelID string) error {
	sctx, cancel := context.WithTimeout(ctx, ttsBudget)
	defer cancel()
	return d.TTS.Speak(sctx, plan, voiceID, modelID, d.Sink, d.Clock)
}

// sendFinal forwards the brain's TurnResponse to the browser as {"type":"final", …}.
func (d Deps) sendFinal(resp *brainclient.TurnResponse) error {
	return d.Conn.SendFinal(finalMsg{Type: "final", TurnResponse: resp})
}

type finalMsg struct {
	Type                      string `json:"type"`
	*brainclient.TurnResponse        // fields promoted inline
}
