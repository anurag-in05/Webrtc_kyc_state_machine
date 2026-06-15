// Package stt is the Sarvam streaming speech-to-text client — a port of
// app/pipeline/stt.py:transcribe_stream (CONTRACTS §5). It streams 16 kHz mono
// s16le PCM over a WebSocket and emits VAD signals followed by exactly one
// terminal final/error event. Any failure (connect, vendor error, WS close, or
// the 15 s timeout) ends as an error event with no transcript — the turn then
// folds to please_repeat (hard-invariant 2).
package stt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	sarvamWSURL = "wss://api.sarvam.ai/speech-to-text/ws"
	sarvamModel = "saarika:v2.5"
	sampleRate  = "16000"
	// streamTimeout is the wall-clock safety net from BEFORE connect (matching
	// stt.py's t0 set before websockets.connect): abort if Sarvam never returns a
	// 'data' event. Covers connect + stream as one budget; NOT reset per event.
	streamTimeout = 15 * time.Second
	// micFrames bounds the mic buffer at ~400 × 20 ms frames ≈ 8 s (GATEWAY.md,
	// reusing the old bound).
	micFrames = 400
)

// Event is one item the stream emits: a VAD signal, or the terminal final/error.
// Mirrors the dicts stt.py yields.
type Event struct {
	Type    string  // "vad" | "final" | "error"
	Signal  string  // vad: START_SPEECH | END_SPEECH
	At      float64 // vad: occured_at
	Text    string  // final: transcript
	Message string  // error: message
}

// Client is the Sarvam STT client. One per gateway; Run is called per turn.
type Client struct {
	apiKey  string
	WSURL   string        // base WS URL; overridable in tests
	timeout time.Duration // wall-clock budget from before-connect
}

func New(apiKey string) *Client {
	return &Client{apiKey: apiKey, WSURL: sarvamWSURL, timeout: streamTimeout}
}

// MicBuffer is the bounded, drop-oldest mic feed into one STT turn. Push is
// called by the audio reader and NEVER blocks it; under backpressure it drops the
// oldest frame and counts the drop (logged once per turn by Run, not per frame —
// same stance as the recorder tee). Single producer (the audio reader).
type MicBuffer struct {
	ch      chan []byte
	dropped atomic.Int64
}

func NewMicBuffer() *MicBuffer { return newMicBuffer(micFrames) }

func newMicBuffer(n int) *MicBuffer { return &MicBuffer{ch: make(chan []byte, n)} }

// Push enqueues one 16 kHz PCM frame without ever blocking. On a full buffer it
// drops the oldest frame to make room for the newest (drop-oldest).
func (m *MicBuffer) Push(pcm []byte) {
	select {
	case m.ch <- pcm:
		return
	default:
	}
	select { // full: drop the oldest (single producer → the freed slot is ours)
	case <-m.ch:
		m.dropped.Add(1)
	default:
	}
	select {
	case m.ch <- pcm:
	default:
		m.dropped.Add(1) // consumer stopped / still full: drop the newest too
	}
}

// CloseInput signals end-of-audio for the turn; Run then flushes Sarvam.
func (m *MicBuffer) CloseInput() { close(m.ch) }

// Run streams mic PCM to Sarvam for one turn, calling emit for each event in
// order (zero+ vad, then one final/error). It returns when a terminal event is
// emitted or ctx is cancelled. The caller calls mic.CloseInput() at end-of-audio
// (END_SPEECH) to trigger the flush.
func (c *Client) Run(ctx context.Context, language string, mic *MicBuffer, emit func(Event)) {
	defer func() {
		if d := mic.dropped.Load(); d > 0 {
			log.Printf("stt: dropped %d mic frames under backpressure this turn", d)
		}
	}()

	// ONE wall-clock budget covering connect + stream, set before connect, never
	// reset on events — faithful to stt.py's t0 (before websockets.connect).
	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	header := http.Header{"Api-Subscription-Key": {c.apiKey}}
	conn, _, err := websocket.DefaultDialer.DialContext(runCtx, c.buildURL(language), header)
	if err != nil {
		emit(Event{Type: "error", Message: fmt.Sprintf("Sarvam WS connect: %v", err)})
		return
	}
	defer conn.Close()

	events := make(chan Event, 8)
	go c.read(conn, events)
	go c.write(runCtx, conn, mic)

	for {
		select {
		case <-runCtx.Done():
			// Deadline = the 15 s hard timeout (stt.py). A parent cancel (peer
			// closed / turn abandoned) emits nothing.
			if runCtx.Err() == context.DeadlineExceeded {
				emit(Event{Type: "error", Message: fmt.Sprintf("STT stream timed out after %s", c.timeout)})
			}
			return
		case ev := <-events:
			emit(ev)
			if ev.Type == "final" || ev.Type == "error" {
				return
			}
		}
	}
}

// buildURL mirrors stt.py:_build_ws_url — the same params in the same order
// (manual build, not url.Values, which would sort keys).
func (c *Client) buildURL(language string) string {
	q := "language-code=" + url.QueryEscape(sarvamLangCode(language)) +
		"&model=" + url.QueryEscape(sarvamModel) +
		"&sample_rate=" + sampleRate +
		"&input_audio_codec=pcm_s16le" +
		"&vad_signals=true"
	return c.WSURL + "?" + q
}

// read is the reader goroutine (stt.py:reader): WS frames → events. Binary and
// non-JSON frames are ignored; 'data' and 'error' terminate.
func (c *Client) read(conn *websocket.Conn, events chan<- Event) {
	for {
		mt, raw, err := conn.ReadMessage()
		if err != nil {
			events <- Event{Type: "error", Message: fmt.Sprintf("Sarvam WS closed: %v", err)}
			return
		}
		if mt == websocket.BinaryMessage {
			continue
		}
		var msg sarvamMsg
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "data":
			events <- Event{Type: "final", Text: strings.TrimSpace(msg.Data.Transcript)}
			return
		case "events":
			if msg.Data.SignalType != "" {
				events <- Event{Type: "vad", Signal: msg.Data.SignalType, At: msg.Data.OccuredAt}
			}
		case "error":
			m := msg.Data.Error
			if m == "" {
				m = "Sarvam STT error"
			}
			events <- Event{Type: "error", Message: m}
			return
		}
	}
}

// write is the writer goroutine (stt.py:writer): each mic frame → a base64 audio
// message; on end-of-audio (mic closed) → a flush. Exits on ctx done or any write
// error.
func (c *Client) write(ctx context.Context, conn *websocket.Conn, mic *MicBuffer) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-mic.ch:
			if !ok {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"flush"}`))
				return
			}
			payload, _ := json.Marshal(audioMsg{Audio: audioBody{
				Data:       base64.StdEncoding.EncodeToString(chunk),
				SampleRate: sampleRate,
				Encoding:   "audio/wav",
			}})
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
	}
}

type sarvamMsg struct {
	Type string `json:"type"`
	Data struct {
		Transcript string  `json:"transcript"`
		SignalType string  `json:"signal_type"`
		OccuredAt  float64 `json:"occured_at"`
		Error      string  `json:"error"`
	} `json:"data"`
}

type audioMsg struct {
	Audio audioBody `json:"audio"`
}

type audioBody struct {
	Data       string `json:"data"`
	SampleRate string `json:"sample_rate"`
	Encoding   string `json:"encoding"`
}
