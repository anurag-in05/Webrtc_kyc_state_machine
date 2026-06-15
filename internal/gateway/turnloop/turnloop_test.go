package turnloop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"kyc-monorepo/internal/gateway/brainclient"
	"kyc-monorepo/internal/gateway/control"
	"kyc-monorepo/internal/gateway/stt"
	"kyc-monorepo/internal/gateway/tts"
)

// --- stubs ---------------------------------------------------------------

// stubBrain serves a snapshot (greeting = greetText) for GET and turnResp(transcript)
// for POST /turn, reporting each /turn transcript on the returned channel.
func stubBrain(t *testing.T, greetText string, turnResp func(transcript string) string) (string, <-chan string) {
	t.Helper()
	tx := make(chan string, 8)
	plan := "[]"
	if greetText != "" {
		plan = `[{"kind":"speech","text":"greeting","slow":false,"speed":1.0}]`
	}
	snap := `{"session_id":"s","state":"greeting","language":"english","turn_index":0,` +
		`"agent_text":"` + greetText + `","tts_plan":` + plan + `,"voice_id":"v","model_id":"m","status":"active",` +
		`"events":[],"recording_status":"pending","full_call_video_url":null}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/turn") {
			var b struct {
				Transcript string `json:"transcript"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			tx <- b.Transcript
			_, _ = io.WriteString(w, turnResp(b.Transcript))
			return
		}
		_, _ = io.WriteString(w, snap)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, tx
}

// stubSarvam sends START_SPEECH + END_SPEECH, then a final transcript once the
// gateway flushes (one connection per turn).
func stubSarvam(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"events","data":{"signal_type":"START_SPEECH","occured_at":0.1}}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"events","data":{"signal_type":"END_SPEECH","occured_at":0.5}}`))
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			if m["type"] == "flush" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"data","data":{"transcript":"hello"}}`))
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// stubEleven returns `status`; on 200 it writes 2 samples of 24k PCM (→ one
// upsampled binary frame to the browser). If hang != nil it instead signals hang
// (the request is mid-flight) and blocks until the client cancels — to test a
// mid-utterance teardown.
func stubEleven(t *testing.T, status int, hang chan struct{}) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang != nil {
			select {
			case hang <- struct{}{}:
			default:
			}
			select { // block until the gateway cancels OR the test releases at cleanup
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte{1, 2, 3, 4})
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // LIFO → runs before srv.Close, unblocking the handler
	return srv.URL
}

func controlPair(t *testing.T) (*control.Conn, *websocket.Conn) {
	t.Helper()
	connCh := make(chan *control.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := control.Upgrade(w, r)
		if err != nil {
			return
		}
		connCh <- c
	}))
	t.Cleanup(srv.Close)
	browser, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { browser.Close() })
	return <-connCh, browser
}

// --- harness -------------------------------------------------------------

type stubs struct {
	turnResp  func(transcript string) string
	greetText string        // non-empty → the greeting speaks
	sttFail   bool          // point STT at an unreachable WS (STT failure)
	ttsStatus int           // ElevenLabs status when not hanging
	ttsHang   chan struct{} // non-nil → TTS hangs (signals this channel mid-flight)
}

type harness struct {
	browser  *websocket.Conn
	brainTx  <-chan string
	cancel   context.CancelFunc // teardown trigger (what Call.Close's cancel does)
	loopDone <-chan struct{}    // closed when Run returns
}

func newHarness(t *testing.T, s stubs) *harness {
	t.Helper()
	brainURL, brainTx := stubBrain(t, s.greetText, s.turnResp)
	conn, browser := controlPair(t)

	ttsc := tts.New("k")
	ttsc.Base = stubEleven(t, s.ttsStatus, s.ttsHang)
	sttc := stt.New("k")
	if s.sttFail {
		sttc.WSURL = "ws://127.0.0.1:1" // nothing listening → STT connect fails
	} else {
		sttc.WSURL = stubSarvam(t)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		Run(ctx, Deps{
			SessionID: "s",
			Conn:      conn,
			Sink:      control.NewSink(conn, func([]byte, uint64) {}), // no recorder in this test
			Clock:     func() uint64 { return 0 },
			SetMic:    func(func([]byte)) {}, // no real audio source here
			Brain:     brainclient.New(brainURL),
			TTS:       ttsc,
			STT:       sttc,
		})
	}()
	return &harness{browser: browser, brainTx: brainTx, cancel: cancel, loopDone: loopDone}
}

func (h *harness) send(t *testing.T, msg string) {
	t.Helper()
	if err := h.browser.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

type frame struct {
	binary bool
	typ    string
	msg    map[string]any
}

// readFrames reads browser frames until one of type `until`, with a timeout.
func readFrames(t *testing.T, browser *websocket.Conn, until string) []frame {
	t.Helper()
	done := make(chan []frame, 1)
	go func() {
		var fs []frame
		for {
			mt, data, err := browser.ReadMessage()
			if err != nil {
				done <- fs
				return
			}
			if mt == websocket.BinaryMessage {
				fs = append(fs, frame{binary: true})
				continue
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			typ, _ := m["type"].(string)
			fs = append(fs, frame{typ: typ, msg: m})
			if typ == until {
				done <- fs
				return
			}
		}
	}()
	select {
	case fs := <-done:
		return fs
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q frame", until)
		return nil
	}
}

func hasType(fs []frame, typ string) bool {
	for _, f := range fs {
		if f.typ == typ {
			return true
		}
	}
	return false
}

func hasBinary(fs []frame) bool {
	for _, f := range fs {
		if f.binary {
			return true
		}
	}
	return false
}

func errMessage(fs []frame) string {
	for _, f := range fs {
		if f.typ == "error" {
			m, _ := f.msg["message"].(string)
			return m
		}
	}
	return ""
}

func agentDoneStatus(fs []frame) string {
	for _, f := range fs {
		if f.typ == "agent_done" {
			s, _ := f.msg["status"].(string)
			return s
		}
	}
	return ""
}

func recvTranscript(t *testing.T, tx <-chan string) string {
	t.Helper()
	select {
	case tr := <-tx:
		return tr
	case <-time.After(2 * time.Second):
		t.Fatal("brain /turn was not called")
		return ""
	}
}

// --- tests ---------------------------------------------------------------

const pleaseRepeatBody = `{"session_id":"s","state":"verify","intent":"please_repeat","transcript":"",` +
	`"agent_text":"Sorry, please repeat.","tts_plan":[{"kind":"speech","text":"Sorry, please repeat.","slow":false,"speed":1.0}],` +
	`"voice_id":"v","model_id":"m","turn_index":1,"attempt_count":1,"events":[],"status":"active"}`

const normalReplyBody = `{"session_id":"s","state":"verify","intent":"yes","transcript":"hello",` +
	`"agent_text":"You said hello.","tts_plan":[{"kind":"speech","text":"You said hello.","slow":false,"speed":1.0}],` +
	`"voice_id":"v","model_id":"m","turn_index":1,"attempt_count":0,"events":[],"status":"active"}`

// Input-side degradation: STT fails → the brain is called with an EMPTY transcript
// → please_repeat is returned AND spoken (the turn advances; attempt_count=1).
func TestSTTFailureFoldsToPleaseRepeat(t *testing.T) {
	h := newHarness(t, stubs{
		turnResp:  func(string) string { return pleaseRepeatBody },
		sttFail:   true,
		ttsStatus: http.StatusOK,
	})
	readFrames(t, h.browser, "agent_done") // greeting (turn 0)

	h.send(t, `{"type":"start_turn"}`)
	fs := readFrames(t, h.browser, "agent_done")

	if tr := recvTranscript(t, h.brainTx); tr != "" {
		t.Errorf("brain transcript = %q, want \"\" (STT failure folds to empty)", tr)
	}
	if !hasType(fs, "final") {
		t.Error("no final sent for the please_repeat turn")
	}
	if !hasBinary(fs) {
		t.Error("please_repeat was not spoken (no agent audio frames)")
	}
	if s := agentDoneStatus(fs); s != "active" {
		t.Errorf("agent_done status = %q, want active", s)
	}
}

// Output-side degradation: TTS fails → control error naming the unvoiced reply +
// agent_done(active), no audio, the brain advanced exactly once, the next turn works.
func TestTTSFailureSignalsAndContinues(t *testing.T) {
	h := newHarness(t, stubs{
		turnResp:  func(string) string { return normalReplyBody },
		sttFail:   false,
		ttsStatus: http.StatusInternalServerError,
	})
	readFrames(t, h.browser, "agent_done") // greeting

	h.send(t, `{"type":"start_turn"}`)
	f1 := readFrames(t, h.browser, "agent_done")
	if !strings.Contains(errMessage(f1), "could not be voiced") {
		t.Errorf("error = %q, want one naming the unvoiced reply", errMessage(f1))
	}
	if hasBinary(f1) {
		t.Error("TTS failed — there must be no agent audio")
	}
	if s := agentDoneStatus(f1); s != "active" {
		t.Errorf("agent_done status = %q, want active (call continues)", s)
	}
	tr1 := recvTranscript(t, h.brainTx)

	// Next turn works (the loop continued).
	h.send(t, `{"type":"start_turn"}`)
	f2 := readFrames(t, h.browser, "agent_done")
	if s := agentDoneStatus(f2); s != "active" {
		t.Errorf("second turn agent_done = %q, want active", s)
	}
	tr2 := recvTranscript(t, h.brainTx)

	if tr1 != "hello" || tr2 != "hello" {
		t.Errorf("brain transcripts = %q, %q, want hello, hello", tr1, tr2)
	}
	// State advanced exactly once per turn: the brain was called twice total, not
	// re-called on the TTS failure.
	select {
	case extra := <-h.brainTx:
		t.Errorf("brain called more than twice (extra %q) — TTS failure double-advanced", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestMidUtteranceTeardownUnwinds: when teardown's ctx cancel lands while the
// agent is speaking (TTS streaming), the turn loop unwinds promptly and cleanly —
// the cancel aborts Speak. This is the property Call.Close relies on when it waits
// for the loop before closing the recorder.
func TestMidUtteranceTeardownUnwinds(t *testing.T) {
	hit := make(chan struct{}, 1)
	h := newHarness(t, stubs{greetText: "Hello.", ttsHang: hit})

	select { // greeting reaches TTS and hangs → we are mid-utterance
	case <-hit:
	case <-time.After(3 * time.Second):
		t.Fatal("greeting never reached TTS")
	}

	h.cancel() // what Call.Close does first

	select {
	case <-h.loopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("turn loop did not unwind on a mid-utterance cancel")
	}
}
