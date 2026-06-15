package tts

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type capturedReq struct {
	path, query, apiKey, contentType string
	body                             []byte
}

// stubElevenLabs serves one canned response and reports the request it received
// (over a channel, so reads are race-free).
func stubElevenLabs(t *testing.T, status int, pcm []byte) (string, <-chan capturedReq) {
	t.Helper()
	reqCh := make(chan capturedReq, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqCh <- capturedReq{
			path: r.URL.Path, query: r.URL.RawQuery,
			apiKey: r.Header.Get("xi-api-key"), contentType: r.Header.Get("Content-Type"),
			body: body,
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(pcm)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, reqCh
}

type sinkCall struct {
	pcm    []byte
	callUS uint64
}

type fakeSink struct{ calls []sinkCall }

func (s *fakeSink) WriteAgentAudio(pcm48 []byte, callUS uint64) error {
	s.calls = append(s.calls, sinkCall{pcm: append([]byte(nil), pcm48...), callUS: callUS})
	return nil
}

func pcm16(vals ...int16) []byte {
	b := make([]byte, len(vals)*2)
	for i, v := range vals {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

func samples16(b []byte) []int16 {
	s := make([]int16, len(b)/2)
	for i := range s {
		s[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return s
}

// counter returns a clock that yields 1000, 2000, 3000, … — one distinct value
// per call, so the test can prove Speak samples it once per chunk.
func counter() func() uint64 {
	var i uint64
	return func() uint64 { i += 1000; return i }
}

// TestSpeakExecutesPlan: a speech item streams 24k PCM, upsamples to 48k, and
// reaches the sink; a silence item emits 48k zeros directly (no upsampler). Each
// sink call carries a fresh call_us sampled by the executor.
func TestSpeakExecutesPlan(t *testing.T) {
	url, _ := stubElevenLabs(t, http.StatusOK, pcm16(100, 200, 300))
	c := &Client{apiKey: "k", Base: url}
	sink := &fakeSink{}

	plan := []PlanItem{
		{Kind: "speech", Text: "hello", Slow: false, Speed: 1.0},
		{Kind: "silence", MS: 200},
	}
	if err := c.Speak(context.Background(), plan, "voice123", "model1", sink, counter()); err != nil {
		t.Fatal(err)
	}
	if len(sink.calls) < 2 {
		t.Fatalf("got %d sink calls, want the speech chunk(s) + silence", len(sink.calls))
	}

	// All calls but the last are the speech segment; concatenated they must equal
	// the full upsample of [100,200,300] (chunk-count-independent: this is robust
	// to however the HTTP body was split).
	var speech []byte
	for _, c := range sink.calls[:len(sink.calls)-1] {
		speech = append(speech, c.pcm...)
	}
	if got, want := samples16(speech), []int16{100, 150, 200, 250, 300, 300}; !reflect.DeepEqual(got, want) {
		t.Errorf("speech 48k = %v, want %v", got, want)
	}

	// Silence is the last call: 48000 * 200/1000 * 2 = 19200 zero bytes, NOT routed
	// through the upsampler.
	last := sink.calls[len(sink.calls)-1].pcm
	if len(last) != 19200 {
		t.Errorf("silence = %d bytes, want 19200 (48k direct)", len(last))
	}
	for _, b := range last {
		if b != 0 {
			t.Fatal("silence is not all zero")
			break
		}
	}

	// call_us is sampled once per chunk by the executor: 1000, 2000, 3000, …
	for k, call := range sink.calls {
		if want := uint64(k+1) * 1000; call.callUS != want {
			t.Errorf("call %d call_us = %d, want %d (one clock sample per chunk)", k, call.callUS, want)
		}
	}
}

// TestSpeakRequestNormal: a normal speech segment posts to /{voice}/stream with
// the right headers and a payload that carries speed and stability 0.5.
func TestSpeakRequestNormal(t *testing.T) {
	url, reqCh := stubElevenLabs(t, http.StatusOK, nil)
	c := &Client{apiKey: "secret", Base: url}
	if err := c.Speak(context.Background(), []PlanItem{{Kind: "speech", Text: "hi", Speed: 1.1}}, "voiceN", "modelN", &fakeSink{}, counter()); err != nil {
		t.Fatal(err)
	}
	req := <-reqCh

	if req.path != "/voiceN/stream" {
		t.Errorf("path = %q, want /voiceN/stream", req.path)
	}
	if req.query != "output_format=pcm_24000" {
		t.Errorf("query = %q, want output_format=pcm_24000", req.query)
	}
	if req.apiKey != "secret" || req.contentType != "application/json" {
		t.Errorf("headers: xi-api-key=%q content-type=%q", req.apiKey, req.contentType)
	}
	vs := voiceSettingsOf(t, req.body, "hi", "modelN")
	if vs["stability"] != 0.5 {
		t.Errorf("stability = %v, want 0.5", vs["stability"])
	}
	if vs["speed"] != 1.1 {
		t.Errorf("speed = %v, want 1.1 (normal carries speed)", vs["speed"])
	}
}

// TestSpeakRequestSlow: a slow (<var>) segment uses stability 0.7 and OMITS speed.
func TestSpeakRequestSlow(t *testing.T) {
	url, reqCh := stubElevenLabs(t, http.StatusOK, nil)
	c := &Client{apiKey: "secret", Base: url}
	if err := c.Speak(context.Background(), []PlanItem{{Kind: "speech", Text: "9, 8, 7.", Slow: true}}, "voiceS", "modelS", &fakeSink{}, counter()); err != nil {
		t.Fatal(err)
	}
	req := <-reqCh

	vs := voiceSettingsOf(t, req.body, "9, 8, 7.", "modelS")
	if vs["stability"] != 0.7 {
		t.Errorf("stability = %v, want 0.7", vs["stability"])
	}
	if _, has := vs["speed"]; has {
		t.Errorf("slow segment must omit speed; got %v", vs["speed"])
	}
}

// TestSpeakElevenLabsError: a non-200 ends Speak with an error (turn → please_repeat).
func TestSpeakElevenLabsError(t *testing.T) {
	url, _ := stubElevenLabs(t, http.StatusInternalServerError, nil)
	c := &Client{apiKey: "k", Base: url}
	if err := c.Speak(context.Background(), []PlanItem{{Kind: "speech", Text: "x"}}, "v", "m", &fakeSink{}, counter()); err == nil {
		t.Fatal("want an error on ElevenLabs 500")
	}
}

func voiceSettingsOf(t *testing.T, body []byte, wantText, wantModel string) map[string]any {
	t.Helper()
	var p struct {
		Text          string         `json:"text"`
		ModelID       string         `json:"model_id"`
		VoiceSettings map[string]any `json:"voice_settings"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if p.Text != wantText || p.ModelID != wantModel {
		t.Errorf("text=%q model_id=%q, want %q/%q", p.Text, p.ModelID, wantText, wantModel)
	}
	if p.VoiceSettings["similarity_boost"] != 0.75 || p.VoiceSettings["use_speaker_boost"] != true {
		t.Errorf("voice_settings = %v", p.VoiceSettings)
	}
	return p.VoiceSettings
}
