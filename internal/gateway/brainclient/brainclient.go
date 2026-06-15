// Package brainclient is the gateway's thin HTTP client for the brain control
// plane (CONTRACTS §1): GET the session snapshot to bootstrap the greeting, and
// POST a transcript per turn to get the brain's decision. No retries, no circuit
// breakers — any failure (brain unreachable, non-200, malformed JSON) returns an
// error, and the turn loop folds that to please_repeat (hard-invariant 2). The
// brain being down degrades the turn, never the call.
package brainclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"kyc-monorepo/internal/gateway/tts"
)

// Event mirrors the brain's TurnEvent.
type Event struct {
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload"`
}

// Snapshot mirrors the brain's SessionSnapshot (GET /api/v1/sessions/{id}).
type Snapshot struct {
	SessionID        string         `json:"session_id"`
	State            string         `json:"state"`
	Language         string         `json:"language"`
	TurnIndex        int            `json:"turn_index"`
	AgentText        string         `json:"agent_text"`
	TTSPlan          []tts.PlanItem `json:"tts_plan"`
	VoiceID          string         `json:"voice_id"`
	ModelID          string         `json:"model_id"`
	Status           string         `json:"status"`
	Events           []Event        `json:"events"`
	RecordingStatus  string         `json:"recording_status"`
	FullCallVideoURL *string        `json:"full_call_video_url"`
}

// TurnResponse mirrors the brain's TurnResponse (POST /api/v1/sessions/{id}/turn).
// (The brain includes session_id; CONTRACTS §1's example omits it — we mirror the
// brain.)
type TurnResponse struct {
	SessionID    string         `json:"session_id"`
	State        string         `json:"state"`
	Intent       string         `json:"intent"`
	Transcript   string         `json:"transcript"`
	AgentText    string         `json:"agent_text"`
	TTSPlan      []tts.PlanItem `json:"tts_plan"`
	VoiceID      string         `json:"voice_id"`
	ModelID      string         `json:"model_id"`
	TurnIndex    int            `json:"turn_index"`
	AttemptCount int            `json:"attempt_count"`
	Events       []Event        `json:"events"`
	Status       string         `json:"status"`
}

// Client talks to the brain at BRAIN_URL.
type Client struct {
	base string
	http *http.Client
}

func New(baseURL string) *Client {
	return &Client{base: baseURL, http: http.DefaultClient}
}

// Get fetches the session snapshot (bootstrap: language, greeting agent_text +
// tts_plan, voice/model ids).
func (c *Client) Get(ctx context.Context, id string) (*Snapshot, error) {
	var snap Snapshot
	if err := c.do(ctx, http.MethodGet, "/api/v1/sessions/"+id, nil, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// Turn posts the user's transcript and returns the brain's decision for the turn.
func (c *Client) Turn(ctx context.Context, id, transcript string) (*TurnResponse, error) {
	body, _ := json.Marshal(map[string]string{"transcript": transcript})
	var resp TurnResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/sessions/"+id+"/turn", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("brain %s %s: %w", method, path, err) // unreachable / transport
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("brain %s %s: status %d", method, path, resp.StatusCode) // 5xx etc.
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("brain %s %s: decode: %w", method, path, err) // malformed JSON
	}
	return nil
}
