package recorder

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRunFinalizeLocalFallback: with S3 unconfigured (nil uploader), runFinalize
// must set Status.URL to the local path and Status.State to the combine result —
// no network, no error (invariant 4). The "nothing captured" case never shells
// out to ffmpeg, so it runs everywhere; the audio case is skipped without ffmpeg.
func TestRunFinalizeLocalFallback(t *testing.T) {
	haveFFmpeg := func() bool { _, err := exec.LookPath("ffmpeg"); return err == nil }()

	cases := []struct {
		name        string
		writePCM    bool // user.pcm + agent.pcm present → audio_only
		needsFFmpeg bool
		wantState   string
		wantMP4     bool // true → URL is <dir>/full_call.mp4; false → URL is ""
	}{
		{name: "nothing captured", writePCM: false, needsFFmpeg: false, wantState: statusFailed, wantMP4: false},
		{name: "audio only", writePCM: true, needsFFmpeg: true, wantState: statusAudioOnly, wantMP4: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.needsFFmpeg && !haveFFmpeg {
				t.Skip("ffmpeg not on PATH")
			}
			dir := t.TempDir()
			sessDir := filepath.Join(dir, c.name)
			if err := os.MkdirAll(sessDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.writePCM {
				pcm := make([]byte, tFrameBytes)
				for _, f := range []string{"user.pcm", "agent.pcm"} {
					if err := os.WriteFile(filepath.Join(sessDir, f), pcm, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}

			h := NewHTTPAPI(dir, nil) // S3 unconfigured → local fallback
			h.runFinalize(c.name)

			s, ok := h.get(c.name)
			if !ok {
				t.Fatal("status not recorded")
			}
			if s.State != c.wantState {
				t.Errorf("state = %q, want %q", s.State, c.wantState)
			}
			wantURL := ""
			if c.wantMP4 {
				wantURL = filepath.Join(sessDir, "full_call.mp4")
			}
			if s.URL != wantURL {
				t.Errorf("url = %q, want %q (local path, not S3)", s.URL, wantURL)
			}
		})
	}
}

// TestS3KeyAndURL pins the key/URL scheme to brain/app/storage/s3.py: _key is
// "{folder}/{sid}/{filename}" (folder omitted when empty) and _url percent-encodes
// each segment, so recorder MP4 URLs and brain transcript URLs are identical.
func TestS3KeyAndURL(t *testing.T) {
	const base = "https://cdn.example.com"
	cases := []struct {
		folder, sid, file string
		wantKey, wantURL  string
	}{
		{"recordings", "deadbeef", "full_call.mp4",
			"recordings/deadbeef/full_call.mp4",
			"https://cdn.example.com/recordings/deadbeef/full_call.mp4"},
		{"", "deadbeef", "full_call.mp4",
			"deadbeef/full_call.mp4",
			"https://cdn.example.com/deadbeef/full_call.mp4"},
		{"kyc media", "abc123", "full_call.mp4", // folder with a space → %20
			"kyc media/abc123/full_call.mp4",
			"https://cdn.example.com/kyc%20media/abc123/full_call.mp4"},
	}
	for _, c := range cases {
		key := s3Key(c.folder, c.sid, c.file)
		if key != c.wantKey {
			t.Errorf("s3Key(%q,%q,%q) = %q, want %q", c.folder, c.sid, c.file, key, c.wantKey)
		}
		if got := s3URL(base, key); got != c.wantURL {
			t.Errorf("s3URL = %q, want %q", got, c.wantURL)
		}
	}
}
