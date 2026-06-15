package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	elevenLabsBase  = "https://api.elevenlabs.io/v1/text-to-speech"
	streamChunkSize = 4096 // tts.py STREAM_CHUNK_SIZE
)

// stream is the port of tts.py:_stream_one — POST one text segment to ElevenLabs
// and deliver raw 24 kHz mono s16le PCM chunks via onChunk. `slow` (the <var>
// digits) uses clear-enunciation settings and omits the speed field; everything
// else carries `speed`. onChunk receives a buffer it must not retain past the call.
func (c *Client) stream(ctx context.Context, text, voiceID, modelID string, slow bool, speed float64, onChunk func([]byte) error) error {
	url := c.Base + "/" + voiceID + "/stream?output_format=pcm_24000"
	body, _ := json.Marshal(buildPayload(text, modelID, slow, speed))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ElevenLabs TTS failed: %d", resp.StatusCode)
	}

	buf := make([]byte, streamChunkSize)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if err := onChunk(buf[:n]); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// buildPayload mirrors tts.py:_build_payload / _build_payload_slow. Normal
// segments carry speed; slow segments use stability 0.7 and OMIT speed entirely.
func buildPayload(text, modelID string, slow bool, speed float64) ttsPayload {
	vs := voiceSettings{Stability: 0.5, SimilarityBoost: 0.75, Style: 0.0, UseSpeakerBoost: true, Speed: &speed}
	if slow {
		vs = voiceSettings{Stability: 0.7, SimilarityBoost: 0.75, Style: 0.0, UseSpeakerBoost: true} // no speed
	}
	return ttsPayload{Text: text, ModelID: modelID, VoiceSettings: vs}
}

type ttsPayload struct {
	Text          string        `json:"text"`
	ModelID       string        `json:"model_id"`
	VoiceSettings voiceSettings `json:"voice_settings"`
}

type voiceSettings struct {
	Stability       float64  `json:"stability"`
	SimilarityBoost float64  `json:"similarity_boost"`
	Style           float64  `json:"style"`
	UseSpeakerBoost bool     `json:"use_speaker_boost"`
	Speed           *float64 `json:"speed,omitempty"` // omitted for slow segments
}
