// Package tts executes a brain-built tts_plan (CONTRACTS §4) into agent audio:
// it streams each speech segment from ElevenLabs (CONTRACTS §5), upsamples to
// 48 kHz, and hands the PCM to an AgentSink. It holds NO markup/phrase logic —
// the brain already resolved the text, voice, model, and split the plan.
package tts

import (
	"context"

	"kyc-monorepo/internal/gateway/audio"
)

// AgentSink is where the agent's voice goes. ONE method, and (today) ONE
// implementation — the only interface in the gateway. The current impl writes the
// PCM as a binary frame on the control WS (browser playback) AND tees it to the
// recorder as AGENT_PCM. The seam exists for a single planned swap (CONTRACTS A→B):
// delivering the agent voice over an outbound WebRTC Opus track instead of the WS,
// which would be a second impl with nothing else changing.
//
// callUS is sampled ONCE per chunk by the plan executor (Speak) from the session
// clock and passed in here. The sink MUST NOT sample the clock itself: that way
// the recorder tee and the browser send carry the identical timestamp for the
// same bytes.
type AgentSink interface {
	WriteAgentAudio(pcm48 []byte, callUS uint64) error
}

// PlanItem is one entry of a tts_plan (CONTRACTS §4).
type PlanItem struct {
	Kind  string  `json:"kind"` // "speech" | "silence"
	Text  string  `json:"text"`
	Slow  bool    `json:"slow"`
	Speed float64 `json:"speed"`
	MS    int     `json:"ms"`
}

// Client is the TTS executor. One per gateway; Speak is called per agent turn.
type Client struct {
	apiKey string
	Base   string // ElevenLabs base URL; overridable in tests
}

func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, Base: elevenLabsBase}
}

// Speak executes a tts_plan, emitting 48 kHz mono s16le PCM to sink. For each
// chunk it samples clock() once (the session clock) and passes that call_us down,
// so the recorder tee and the browser send agree on timing. voiceID/modelID come
// from the brain. Returns on the first ElevenLabs or sink error (the turn loop
// folds a TTS failure to please_repeat).
func (c *Client) Speak(ctx context.Context, plan []PlanItem, voiceID, modelID string, sink AgentSink, clock func() uint64) error {
	for _, item := range plan {
		switch item.Kind {
		case "silence":
			// Zeros upsample to zeros, so emit the 48 kHz silence directly — no
			// HTTP, no upsampler. 48000 * ms/1000 * 2 bytes.
			zeros := make([]byte, 48000*item.MS/1000*2)
			if err := sink.WriteAgentAudio(zeros, clock()); err != nil {
				return err
			}
		case "speech":
			var up audio.Upsampler
			err := c.stream(ctx, item.Text, voiceID, modelID, item.Slow, item.Speed, func(chunk24 []byte) error {
				out := up.Up24to48(chunk24)
				if len(out) == 0 {
					return nil
				}
				return sink.WriteAgentAudio(out, clock())
			})
			if err != nil {
				return err
			}
			if tail := up.Flush(); len(tail) > 0 {
				if err := sink.WriteAgentAudio(tail, clock()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
