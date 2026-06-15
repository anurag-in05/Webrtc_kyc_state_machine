package control

// Sink is the agent-audio sink — the first real implementation of tts.AgentSink.
// It writes each PCM chunk to the browser as a binary control frame AND tees it to
// the recorder via tee.
//
// call_us is supplied by the plan executor (sampled once per chunk from the
// session clock) and used AS-IS: the sink never samples the clock, so the recorder
// tee and the browser send carry the identical timestamp for the same bytes.
//
// tee runs FIRST, so the recording captures the agent audio even if the browser
// socket has dropped mid-utterance. The browser write may then error; that error
// is returned to the executor, which ends the turn (the recording is already safe).
type Sink struct {
	conn *Conn
	tee  func(pcm48 []byte, callUS uint64)
}

// NewSink builds the sink. tee is wired by the caller to recordclient.SendAt with
// AGENT_PCM — passing call_us straight through.
func NewSink(conn *Conn, tee func(pcm48 []byte, callUS uint64)) *Sink {
	return &Sink{conn: conn, tee: tee}
}

func (s *Sink) WriteAgentAudio(pcm48 []byte, callUS uint64) error {
	s.tee(pcm48, callUS)           // record first — survives a mid-utterance WS drop
	return s.conn.SendAudio(pcm48) // browser; errors if the socket has dropped
}
