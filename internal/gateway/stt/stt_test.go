package stt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsServer starts an httptest server that upgrades to a WebSocket and runs handle
// with the connection + request. Returns its ws:// URL.
func wsServer(t *testing.T, handle func(conn *websocket.Conn, r *http.Request)) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn, r)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func testClient(wsURL string, timeout time.Duration) *Client {
	return &Client{apiKey: "test-key", wsURL: wsURL, timeout: timeout}
}

// TestRunFidelity checks the wire framing against stt.py: the exact query string
// (params + order + escaping), the Api-Subscription-Key header, base64 audio
// frames, the flush message, and the vad→final event sequence.
func TestRunFidelity(t *testing.T) {
	type capture struct {
		query, header string
		flush         bool
		audio         []byte
	}
	var cap capture
	done := make(chan struct{})

	wsURL := wsServer(t, func(conn *websocket.Conn, r *http.Request) {
		cap.query = r.URL.RawQuery
		cap.header = r.Header.Get("Api-Subscription-Key")
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				Type  string `json:"type"`
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			}
			_ = json.Unmarshal(raw, &m)
			if m.Type == "flush" {
				cap.flush = true
				break
			}
			if m.Audio.Data != "" {
				d, _ := base64.StdEncoding.DecodeString(m.Audio.Data)
				cap.audio = append(cap.audio, d...)
			}
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"events","data":{"signal_type":"START_SPEECH","occured_at":0.1}}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"events","data":{"signal_type":"END_SPEECH","occured_at":0.5}}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"data","data":{"transcript":"  yes that's me  "}}`))
		close(done)
	})

	mic := NewMicBuffer()
	mic.Push([]byte{1, 2, 3, 4})
	mic.Push([]byte{5, 6})
	mic.CloseInput()

	var got []Event
	testClient(wsURL, time.Second).Run(context.Background(), "english", mic, func(e Event) { got = append(got, e) })
	<-done // synchronize the captured fields

	wantQuery := "language-code=en-IN&model=saarika%3Av2.5&sample_rate=16000&input_audio_codec=pcm_s16le&vad_signals=true"
	if cap.query != wantQuery {
		t.Errorf("query =\n  %s\nwant\n  %s", cap.query, wantQuery)
	}
	if cap.header != "test-key" {
		t.Errorf("Api-Subscription-Key = %q, want test-key", cap.header)
	}
	if !cap.flush {
		t.Error("server never received the flush message")
	}
	if want := []byte{1, 2, 3, 4, 5, 6}; string(cap.audio) != string(want) {
		t.Errorf("audio = %v, want %v (base64 framing)", cap.audio, want)
	}

	want := []Event{
		{Type: "vad", Signal: "START_SPEECH", At: 0.1},
		{Type: "vad", Signal: "END_SPEECH", At: 0.5},
		{Type: "final", Text: "yes that's me"}, // trimmed
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRunVendorError: a Sarvam error event becomes a terminal error (no transcript).
func TestRunVendorError(t *testing.T) {
	wsURL := wsServer(t, func(conn *websocket.Conn, r *http.Request) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","data":{"error":"boom"}}`))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	mic := NewMicBuffer()
	mic.CloseInput()

	var got []Event
	testClient(wsURL, time.Second).Run(context.Background(), "english", mic, func(e Event) { got = append(got, e) })

	if len(got) != 1 || got[0].Type != "error" || got[0].Message != "boom" {
		t.Fatalf("got %+v, want one error{boom}", got)
	}
}

// TestRunTimeoutIsWallClock proves the 15 s budget is wall-clock-from-connect and
// is NOT reset by incoming events. The server sends one vad at 300 ms then nothing;
// with a 500 ms budget the timeout must fire ~500 ms after connect. An idle-reset
// would push it to 300+500 = 800 ms, so we assert Run finishes well before then.
func TestRunTimeoutIsWallClock(t *testing.T) {
	wsURL := wsServer(t, func(conn *websocket.Conn, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"events","data":{"signal_type":"START_SPEECH","occured_at":0.3}}`))
		for { // hold the connection open, send no data
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	mic := NewMicBuffer() // never closed: the turn is still "listening" at timeout
	var got []Event
	done := make(chan struct{})
	go func() {
		testClient(wsURL, 500*time.Millisecond).Run(context.Background(), "english", mic, func(e Event) { got = append(got, e) })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(700 * time.Millisecond):
		t.Fatal("Run did not finish by 700ms — the deadline was reset by the vad event (idle-reset, not wall-clock-from-connect)")
	}

	if len(got) == 0 || got[len(got)-1].Type != "error" || !strings.Contains(got[len(got)-1].Message, "timed out") {
		t.Fatalf("got %+v, want a terminal timeout error", got)
	}
	if got[0].Type != "vad" {
		t.Errorf("first event = %+v, want the vad received before the timeout", got[0])
	}
}

// TestMicBufferDropsOldestNeverBlocks: Push never blocks under backpressure, drops
// the OLDEST frames, and counts the drops (Run logs the count once per turn).
func TestMicBufferDropsOldestNeverBlocks(t *testing.T) {
	m := newMicBuffer(2)
	done := make(chan struct{})
	go func() {
		for i := 1; i <= 5; i++ {
			m.Push([]byte{byte(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Push blocked under backpressure")
	}

	if got := m.dropped.Load(); got != 3 {
		t.Fatalf("dropped = %d, want 3 (5 pushed into a cap-2 buffer)", got)
	}
	a, b := <-m.ch, <-m.ch
	if a[0] != 4 || b[0] != 5 {
		t.Fatalf("retained %d,%d, want 4,5 (drop-oldest keeps the newest)", a[0], b[0])
	}
}
