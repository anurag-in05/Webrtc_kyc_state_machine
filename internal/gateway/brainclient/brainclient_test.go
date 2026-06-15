package brainclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The CONTRACTS §1 turn-response example, with the §4 tts_plan example inlined and
// the "..." placeholders concretized — i.e. exactly what the brain returns.
const cannedTurn = `{
  "session_id": "abc123",
  "state": "congrats",
  "intent": "yes",
  "transcript": "yes that's me",
  "agent_text": "Thank you.",
  "tts_plan": [
    {"kind":"speech","text":"Is your mobile number","slow":false,"speed":1.0},
    {"kind":"silence","ms":200},
    {"kind":"speech","text":"nine, eight, seven, six, five.","slow":true},
    {"kind":"silence","ms":200},
    {"kind":"speech","text":"?","slow":false,"speed":1.0}
  ],
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "model_id": "eleven_flash_v2_5",
  "turn_index": 1,
  "attempt_count": 0,
  "events": [{"event_type":"record_verification_step","payload":{"step":"primary_mobile","status":"verified"}}],
  "status": "active"
}`

const cannedSnapshot = `{
  "session_id": "abc123",
  "state": "greeting",
  "language": "english",
  "turn_index": 0,
  "agent_text": "Good morning, this is Kavya.",
  "tts_plan": [{"kind":"speech","text":"Good morning","slow":false,"speed":1.0}],
  "voice_id": "21m00Tcm4TlvDq8ikWAM",
  "model_id": "eleven_flash_v2_5",
  "status": "active",
  "events": [{"event_type":"greeting_finished","payload":{}}],
  "recording_status": "pending",
  "full_call_video_url": null
}`

// TestTurnRoundTrips decodes the canned turn response field-for-field (every
// tts_plan item, voice/model, events, status) and checks the request shape.
func TestTurnRoundTrips(t *testing.T) {
	type reqInfo struct {
		method, path, transcript string
	}
	reqCh := make(chan reqInfo, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Transcript string `json:"transcript"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		reqCh <- reqInfo{r.Method, r.URL.Path, body.Transcript}
		_, _ = w.Write([]byte(cannedTurn))
	}))
	defer srv.Close()

	resp, err := New(srv.URL).Turn(context.Background(), "sess42", "yes that's me")
	if err != nil {
		t.Fatal(err)
	}

	req := <-reqCh
	if req.method != http.MethodPost || req.path != "/api/v1/sessions/sess42/turn" || req.transcript != "yes that's me" {
		t.Errorf("request = %s %s transcript=%q", req.method, req.path, req.transcript)
	}

	if resp.State != "congrats" || resp.Intent != "yes" || resp.Transcript != "yes that's me" ||
		resp.AgentText != "Thank you." || resp.VoiceID != "21m00Tcm4TlvDq8ikWAM" ||
		resp.ModelID != "eleven_flash_v2_5" || resp.TurnIndex != 1 || resp.AttemptCount != 0 ||
		resp.Status != "active" {
		t.Errorf("scalar fields wrong: %+v", resp)
	}

	wantPlan := []struct {
		kind, text string
		slow       bool
		speed      float64
		ms         int
	}{
		{"speech", "Is your mobile number", false, 1.0, 0},
		{"silence", "", false, 0, 200},
		{"speech", "nine, eight, seven, six, five.", true, 0, 0},
		{"silence", "", false, 0, 200},
		{"speech", "?", false, 1.0, 0},
	}
	if len(resp.TTSPlan) != len(wantPlan) {
		t.Fatalf("tts_plan len = %d, want %d", len(resp.TTSPlan), len(wantPlan))
	}
	for i, w := range wantPlan {
		got := resp.TTSPlan[i]
		if got.Kind != w.kind || got.Text != w.text || got.Slow != w.slow || got.Speed != w.speed || got.MS != w.ms {
			t.Errorf("tts_plan[%d] = %+v, want %v", i, got, w)
		}
	}

	if len(resp.Events) != 1 || resp.Events[0].EventType != "record_verification_step" ||
		resp.Events[0].Payload["step"] != "primary_mobile" || resp.Events[0].Payload["status"] != "verified" {
		t.Errorf("events = %+v", resp.Events)
	}
}

// TestGetRoundTrips decodes the canned snapshot (the bootstrap fields).
func TestGetRoundTrips(t *testing.T) {
	reqCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCh <- r.URL.Path
		_, _ = w.Write([]byte(cannedSnapshot))
	}))
	defer srv.Close()

	snap, err := New(srv.URL).Get(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if p := <-reqCh; p != "/api/v1/sessions/abc123" {
		t.Errorf("path = %q", p)
	}
	if snap.Language != "english" || snap.State != "greeting" || snap.TurnIndex != 0 ||
		snap.AgentText != "Good morning, this is Kavya." || snap.VoiceID != "21m00Tcm4TlvDq8ikWAM" ||
		snap.ModelID != "eleven_flash_v2_5" || snap.Status != "active" || snap.RecordingStatus != "pending" {
		t.Errorf("snapshot fields wrong: %+v", snap)
	}
	if snap.FullCallVideoURL != nil {
		t.Errorf("full_call_video_url = %v, want nil", *snap.FullCallVideoURL)
	}
	if len(snap.TTSPlan) != 1 || snap.TTSPlan[0].Text != "Good morning" {
		t.Errorf("tts_plan = %+v", snap.TTSPlan)
	}
	if len(snap.Events) != 1 || snap.Events[0].EventType != "greeting_finished" {
		t.Errorf("events = %+v", snap.Events)
	}
}

// TestFailuresReturnError: brain unreachable, non-200, and malformed JSON each
// return an error (the turn loop folds it to please_repeat). No retries.
func TestFailuresReturnError(t *testing.T) {
	// unreachable
	if _, err := New("http://127.0.0.1:1").Turn(context.Background(), "x", "hi"); err == nil {
		t.Error("unreachable brain: want error")
	}
	// non-200
	srv5xx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv5xx.Close()
	if _, err := New(srv5xx.URL).Turn(context.Background(), "x", "hi"); err == nil {
		t.Error("brain 500: want error")
	}
	// malformed JSON
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srvBad.Close()
	if _, err := New(srvBad.URL).Get(context.Background(), "x"); err == nil {
		t.Error("malformed JSON: want error")
	}
}
