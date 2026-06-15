// Package control is the browser ⇄ gateway control WebSocket (CONTRACTS §2). One
// socket carries two frame kinds: TEXT frames are JSON control (the gateway sends
// vad/final/agent_done/error; the browser sends start_turn/end), and BINARY frames
// are agent audio out (raw s16le mono 48 kHz PCM the browser plays via Web Audio).
// The turn loop (G7) drives this; G5 builds the transport + the agent-audio sink.
package control

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// CheckOrigin allows any origin: the browser client connects cross-origin in dev,
// as in the old web/index.html flow. The session id in the path is the authz.
var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// Inbound is a client→gateway control message: {"type":"start_turn"} or {"type":"end"}.
type Inbound struct {
	Type string `json:"type"`
}

// Conn wraps the control WebSocket. A single reader goroutine surfaces inbound
// control on Inbox(); all outbound writes (text events + binary audio) go through
// writeMu, since gorilla permits only one concurrent writer.
type Conn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	inbox   chan Inbound
}

// Upgrade performs the WebSocket handshake and starts the reader goroutine.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	c := &Conn{ws: ws, inbox: make(chan Inbound, 8)}
	go c.read()
	return c, nil
}

// read is the single reader goroutine. TEXT frames are parsed as control and put
// on Inbox; BINARY inbound is ignored (the browser sends only text control). On
// any read error — the socket dropped — it closes Inbox to signal the turn loop.
func (c *Conn) read() {
	defer close(c.inbox)
	for {
		mt, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage {
			continue
		}
		var msg Inbound
		if json.Unmarshal(data, &msg) == nil && msg.Type != "" {
			c.inbox <- msg
		}
	}
}

// Inbox yields inbound control messages; it is closed when the socket drops.
func (c *Conn) Inbox() <-chan Inbound { return c.inbox }

// SendVAD forwards a Sarvam VAD signal to the browser UI.
func (c *Conn) SendVAD(signal string, at float64) error {
	return c.writeJSON(map[string]any{"type": "vad", "signal": signal, "at": at})
}

// SendFinal forwards the brain's TurnResponse to the browser (the turn loop sets
// the "type":"final" field on the payload it passes).
func (c *Conn) SendFinal(payload any) error {
	return c.writeJSON(payload)
}

// SendAgentDone tells the browser it may start the next turn.
func (c *Conn) SendAgentDone(turnIndex int, status string) error {
	return c.writeJSON(map[string]any{"type": "agent_done", "turn_index": turnIndex, "status": status})
}

// SendError reports a turn failure to the browser UI.
func (c *Conn) SendError(message string) error {
	return c.writeJSON(map[string]any{"type": "error", "message": message})
}

func (c *Conn) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(v)
}

// SendAudio writes one agent-audio chunk as a BINARY frame (raw s16le mono 48 kHz).
func (c *Conn) SendAudio(pcm48 []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(websocket.BinaryMessage, pcm48)
}

// Close closes the socket; the reader goroutine then exits and Inbox closes.
func (c *Conn) Close() error { return c.ws.Close() }
