package control

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialControl stands up an httptest server that upgrades to a control.Conn, and a
// gorilla client connected to it. Returns the gateway-side Conn and the client.
func dialControl(t *testing.T) (*Conn, *websocket.Conn) {
	t.Helper()
	connCh := make(chan *Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		connCh <- c // the reader goroutine keeps the hijacked socket alive
	}))
	t.Cleanup(srv.Close)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return <-connCh, client
}

// TestControlFrameDiscipline: text frames carry JSON control both ways; binary
// frames carry agent audio out — on the one socket.
func TestControlFrameDiscipline(t *testing.T) {
	gw, client := dialControl(t)

	// client → gateway: a text control message surfaces on Inbox.
	if err := client.WriteMessage(websocket.TextMessage, []byte(`{"type":"start_turn"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case in := <-gw.Inbox():
		if in.Type != "start_turn" {
			t.Errorf("inbox = %q, want start_turn", in.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("start_turn never reached Inbox")
	}

	// gateway → client: each event is a TEXT frame with the right JSON.
	if err := gw.SendVAD("START_SPEECH", 0.1); err != nil {
		t.Fatal(err)
	}
	assertText(t, client, `"type":"vad"`, `"signal":"START_SPEECH"`)

	if err := gw.SendAgentDone(2, "active"); err != nil {
		t.Fatal(err)
	}
	assertText(t, client, `"type":"agent_done"`, `"turn_index":2`, `"status":"active"`)

	if err := gw.SendError("oops"); err != nil {
		t.Fatal(err)
	}
	assertText(t, client, `"type":"error"`, `"message":"oops"`)

	// gateway → client: agent audio is a BINARY frame, byte-for-byte.
	if err := gw.SendAudio([]byte{9, 8, 7, 6}); err != nil {
		t.Fatal(err)
	}
	mt, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if mt != websocket.BinaryMessage || !bytes.Equal(data, []byte{9, 8, 7, 6}) {
		t.Fatalf("audio frame: type=%d data=%v, want binary [9 8 7 6]", mt, data)
	}
}

func assertText(t *testing.T, client *websocket.Conn, wants ...string) {
	t.Helper()
	mt, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("frame type = %d, want text", mt)
	}
	for _, w := range wants {
		if !strings.Contains(string(data), w) {
			t.Errorf("frame %s missing %s", data, w)
		}
	}
}

// TestSinkHonorsCallUSAndTees: the sink passes the executor's call_us straight to
// the recorder tee (no clock sampling) and sends the same bytes to the browser.
func TestSinkHonorsCallUSAndTees(t *testing.T) {
	gw, client := dialControl(t)

	var teedPCM []byte
	var teedCallUS uint64
	teeCount := 0
	sink := NewSink(gw, func(pcm48 []byte, callUS uint64) {
		teedPCM = append([]byte(nil), pcm48...)
		teedCallUS = callUS
		teeCount++
	})

	if err := sink.WriteAgentAudio([]byte{1, 2, 3, 4}, 4242); err != nil {
		t.Fatal(err)
	}
	if teeCount != 1 || teedCallUS != 4242 || !bytes.Equal(teedPCM, []byte{1, 2, 3, 4}) {
		t.Fatalf("tee: count=%d call_us=%d pcm=%v, want 1/4242/[1 2 3 4] (call_us passed through)", teeCount, teedCallUS, teedPCM)
	}
	mt, data, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if mt != websocket.BinaryMessage || !bytes.Equal(data, []byte{1, 2, 3, 4}) {
		t.Fatalf("browser frame: type=%d data=%v", mt, data)
	}
}

// TestSinkRecordsThroughWSDrop: when the socket has dropped mid-utterance the
// recorder tee STILL happens (the recording survives) and the browser write errors
// (so the executor learns the socket is gone).
func TestSinkRecordsThroughWSDrop(t *testing.T) {
	gw, _ := dialControl(t)
	gw.Close() // the socket is gone (stands in for a mid-utterance drop)

	teed := false
	var teedCallUS uint64
	sink := NewSink(gw, func(pcm48 []byte, callUS uint64) {
		teed, teedCallUS = true, callUS
	})

	err := sink.WriteAgentAudio([]byte{1, 2}, 555)
	if !teed || teedCallUS != 555 {
		t.Fatalf("recorder tee did not happen through the WS drop (teed=%v)", teed)
	}
	if err == nil {
		t.Error("want a browser-write error after the socket dropped")
	}
}
