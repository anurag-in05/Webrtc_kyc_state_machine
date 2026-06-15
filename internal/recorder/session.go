package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	recorderpb "kyc-monorepo/proto"
)

const (
	pcmSampleRate     = 48000 // Hz, mono
	pcmBytesPerSample = 2     // s16le
)

// Session owns the output files for ONE recording. There is exactly one Session
// per gRPC stream, so every frame for this session arrives on a single
// goroutine, in order — which is why there's no mutex here.
type Session struct {
	id    string
	dir   string
	user  *os.File     // user.pcm  — mic audio, 48k mono s16le
	agent *os.File     // agent.pcm — tts audio, 48k mono s16le
	video *videoWriter // video_raw.mp4

	// Bytes written so far to each PCM file = its current write cursor. Each
	// frame is positioned by call_us against this cursor (see appendPCM), so the
	// recorder holds no per-stream origin/first-frame state.
	userBytes  int64
	agentBytes int64
}

func newSession(recordingsDir, id string) (*Session, error) {
	dir := filepath.Join(recordingsDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	user, err := os.Create(filepath.Join(dir, "user.pcm"))
	if err != nil {
		return nil, fmt.Errorf("create user.pcm: %w", err)
	}
	agent, err := os.Create(filepath.Join(dir, "agent.pcm"))
	if err != nil {
		user.Close()
		return nil, fmt.Errorf("create agent.pcm: %w", err)
	}
	return &Session{
		id:    id,
		dir:   dir,
		user:  user,
		agent: agent,
		video: newVideoWriter(filepath.Join(dir, "video_raw.mp4")),
	}, nil
}

// write routes one frame to the right sink. callUS is the shared per-call clock
// (CONTRACTS §3): it positions both PCM streams on one timeline and stamps the
// video's lip-sync offset. tsUS is the per-stream pacing clock, used only by the
// MP4 writer.
func (s *Session) write(kind recorderpb.Kind, tsUS, callUS uint64, data []byte) error {
	switch kind {
	case recorderpb.Kind_USER_PCM:
		return appendPCM(s.user, &s.userBytes, callUS, data)
	case recorderpb.Kind_AGENT_PCM:
		return appendPCM(s.agent, &s.agentBytes, callUS, data)
	case recorderpb.Kind_VIDEO_AU:
		s.video.send(tsUS, callUS, data) // enqueue to the mux goroutine; never blocks on disk
		return nil
	default:
		return fmt.Errorf("unknown kind %v", kind)
	}
}

// pcmByteOffset is the absolute byte offset in a 48 kHz mono s16le stream for a
// given call-clock time, snapped down to a whole sample (so always even).
func pcmByteOffset(callUS uint64) int64 {
	return int64(callUS) * pcmSampleRate / 1_000_000 * pcmBytesPerSample
}

// appendPCM writes one PCM frame at the absolute position its call_us maps to,
// filling any gap since the last write with silence (zero bytes). Both PCM
// streams share the call clock, so positioning each by call_us puts them on one
// timeline: sample 0 is call_us == 0, and a stream that starts late carries
// leading silence for free — which is how an agent track that's only teed while
// the agent speaks becomes a full-length, call-aligned stream. Computing the
// offset from absolute call_us each frame (not summing per-gap) means rounding
// never accumulates, so there is no drift. A frame whose offset is at/behind the
// cursor (reorder/jitter) is appended in place; we never seek backward.
func appendPCM(f *os.File, written *int64, callUS uint64, data []byte) error {
	if pad := pcmByteOffset(callUS) - *written; pad > 0 {
		if _, err := f.Write(make([]byte, pad)); err != nil {
			return err
		}
		*written += pad
	}
	n, err := f.Write(data)
	*written += int64(n)
	return err
}

// close flushes all sinks and writes meta.json. It blocks on the video writer
// finishing, after which video.firstCallUS is safe to read.
func (s *Session) close() {
	s.video.close()
	s.user.Close()
	s.agent.Close()
	s.writeMeta()
}

// writeMeta persists the finalize-pass inputs that have to survive to a separate
// (possibly post-restart) /finalize request. Only the video's first-keyframe
// call_us is needed: audio sample 0 is call_us 0 by construction, so the video
// offset is just that value. Best-effort — a missing meta.json means offset 0.
func (s *Session) writeMeta() {
	b, _ := json.Marshal(recordingMeta{VideoFirstCallUS: s.video.firstCallUS})
	_ = os.WriteFile(filepath.Join(s.dir, "meta.json"), b, 0o644)
}
