package recorder

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	recorderpb "kyc-monorepo/proto"
)

// 20 ms of 48 kHz mono s16le.
const (
	tFrameUS    = 20_000
	tFrameBytes = pcmSampleRate / 1000 * 20 * pcmBytesPerSample // 1920
)

func filledFrame(marker byte) []byte {
	b := make([]byte, tFrameBytes)
	for i := range b {
		b[i] = marker
	}
	return b
}

func allBytes(b []byte, v byte) bool {
	for _, c := range b {
		if c != v {
			return false
		}
	}
	return true
}

// TestAudioTimelineCallUsAlignment is the regression guard for the audio-drift
// fix: a gapped agent track (only teed while the agent speaks) must be
// reconstructed as a full-length, call-aligned stream, with its second burst at
// the absolute byte offset its call_us maps to — NOT slid earlier to sit right
// after the first burst (which is what raw-append produced and what causes the
// cumulative Q/A drift).
//
// agent.pcm maps to the right channel in the (already-tested) combine, so
// asserting placement in agent.pcm is asserting right-channel placement.
func TestAudioTimelineCallUsAlignment(t *testing.T) {
	dir := t.TempDir()
	sess, err := newSession(dir, "sess")
	if err != nil {
		t.Fatal(err)
	}

	const (
		userMarker  = 0x33
		burst1Mark  = 0x11
		burst2Mark  = 0x22
		userFrames  = 100       // continuous mic, 0..2000 ms
		burst1Count = 10        // agent speaks 0..200 ms
		burst2Start = 1_200_000 // ...then silent for 1 s, then speaks again
		burst2Count = 10        // 1200..1400 ms
	)

	// Continuous user mic for the whole call.
	for i := 0; i < userFrames; i++ {
		callUS := uint64(i) * tFrameUS
		if err := sess.write(recorderpb.Kind_USER_PCM, callUS, callUS, filledFrame(userMarker)); err != nil {
			t.Fatal(err)
		}
	}
	// Agent burst 1, then a 1 s gap with NOTHING sent, then burst 2.
	for i := 0; i < burst1Count; i++ {
		callUS := uint64(i) * tFrameUS
		if err := sess.write(recorderpb.Kind_AGENT_PCM, callUS, callUS, filledFrame(burst1Mark)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < burst2Count; i++ {
		callUS := uint64(burst2Start) + uint64(i)*tFrameUS
		if err := sess.write(recorderpb.Kind_AGENT_PCM, callUS, callUS, filledFrame(burst2Mark)); err != nil {
			t.Fatal(err)
		}
	}
	sess.close()

	agent, err := os.ReadFile(filepath.Join(dir, "sess", "agent.pcm"))
	if err != nil {
		t.Fatal(err)
	}

	burst1End := pcmByteOffset(burst1Count * tFrameUS)           // 200 ms  -> 19200
	burst2Off := pcmByteOffset(burst2Start)                      // 1200 ms -> 115200
	wantLen := pcmByteOffset(burst2Start + burst2Count*tFrameUS) // 1400 ms -> 134400
	if burst2Off == burst1End {
		t.Fatal("test misconfigured: bug and fix offsets coincide")
	}

	// Full length including the silence-filled gap. (Raw-append would give only
	// burst1+burst2 = 38400 bytes, so this single check already catches the bug.)
	if int64(len(agent)) != wantLen {
		t.Fatalf("agent.pcm = %d bytes, want %d (gap not filled with silence)", len(agent), wantLen)
	}
	// Three regions, exactly: burst1 | silence gap | burst2-at-its-call_us.
	if !allBytes(agent[:burst1End], burst1Mark) {
		t.Errorf("region [0,%d) is not burst1", burst1End)
	}
	if !allBytes(agent[burst1End:burst2Off], 0x00) {
		t.Errorf("gap [%d,%d) is not silence — burst2 slid earlier", burst1End, burst2Off)
	}
	if !allBytes(agent[burst2Off:], burst2Mark) {
		t.Errorf("burst2 does not start exactly at its call_us offset %d", burst2Off)
	}

	// user.pcm is the continuous stream, untouched in length.
	user, err := os.ReadFile(filepath.Join(dir, "sess", "user.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	if want := pcmByteOffset(userFrames * tFrameUS); int64(len(user)) != want {
		t.Errorf("user.pcm = %d bytes, want %d", len(user), want)
	}

	// End-to-end confirmation through the real finalize pass when ffmpeg exists:
	// combine merges to stereo (user=L, agent=R). No video here → audio_only.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Log("ffmpeg not on PATH; byte-level alignment already verified, skipping combine")
		return
	}
	status, out := combine(filepath.Join(dir, "sess"))
	if status != statusAudioOnly {
		t.Fatalf("combine status = %q, want %q", status, statusAudioOnly)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=channels", "-of", "default=nw=1:nk=1", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if got := strings.TrimSpace(string(probe)); got != "2" {
		t.Errorf("full_call.mp4 channels = %q, want 2 (stereo user|agent)", got)
	}

	// Informational (logged, not asserted — keeps the test robust): run
	// silencedetect on the RIGHT channel (agent). The 1 s gap should show up as
	// silence_start≈0.2 / silence_end≈1.2 — i.e. burst2 begins at 1.2 s on the
	// right channel, exactly its call_us, not slid earlier.
	det, _ := exec.Command("ffmpeg", "-v", "info", "-i", out,
		"-af", "pan=mono|c0=c1,silencedetect=n=-50dB:d=0.2", "-f", "null", "-").CombinedOutput()
	for _, line := range strings.Split(string(det), "\n") {
		if strings.Contains(line, "silence_") {
			t.Logf("right-channel %s", strings.TrimSpace(line))
		}
	}
}
