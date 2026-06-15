package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	recorderpb "kyc-monorepo/proto"
)
// Session owns the output files for ONE recording. There is exactly one
// Session per gRPC stream, so every frame for this session arrives on a
// single goroutine, in order — which is why there's no mutex here.
type Session struct {
	id    string
	dir   string
	user  *os.File     // user.pcm  — mic audio, 48k mono s16le
	agent *os.File     // agent.pcm — tts audio, 48k mono s16le
	video *videoWriter // video_raw.mp4 — filled in step 3
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

// write routes one frame to the right sink.
func (s *Session) write(kind recorderpb.Kind, tsUS uint64, data []byte) error {
	switch kind {
	case recorderpb.Kind_USER_PCM:
		_, err := s.user.Write(data) // raw PCM: just append the bytes
		return err
	case recorderpb.Kind_AGENT_PCM:
		_, err := s.agent.Write(data)
		return err
	case recorderpb.Kind_VIDEO_AU:
		s.video.send(tsUS, data) // enqueue to the mux goroutine; never blocks on disk
		return nil
	default:
		return fmt.Errorf("unknown kind %v", kind)
	}
}

func (s *Session) close() {
	s.video.close() // flush the last MP4 fragment (step 3)
	s.user.Close()
	s.agent.Close()
}