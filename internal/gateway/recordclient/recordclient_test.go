package recordclient

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	"kyc-monorepo/internal/gateway/session"
	"kyc-monorepo/internal/recorder"
	recorderpb "kyc-monorepo/proto"
)

// 20 ms of 48 kHz mono s16le.
const (
	tFrameUS    = 20_000
	tFrameBytes = 48000 / 1000 * 20 * 2 // 1920
)

// pcmByteOffset mirrors the recorder's own (unexported) positioning formula —
// the absolute byte offset in a 48 kHz mono s16le stream for a call-clock time.
// Asserting placement against it IS asserting call-alignment, the same way
// recorder's TestAudioTimelineCallUsAlignment does.
func pcmByteOffset(callUS uint64) int64 {
	return int64(callUS) * 48000 / 1_000_000 * 2
}

func filled(marker byte) []byte {
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

func firstNonZero(b []byte) int64 {
	for i, c := range b {
		if c != 0 {
			return int64(i)
		}
	}
	return -1
}

// startRecorder spins up the REAL recorder gRPC service on a localhost port and
// returns its address + recordings dir. Real recorder code, real gRPC, offline —
// no external/live recorder, no network, no keys.
func startRecorder(t *testing.T) (addr, dir string) {
	t.Helper()
	dir = t.TempDir()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	recorderpb.RegisterRecorderServer(srv, recorder.NewService(dir))
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), dir
}

// TestSendStreamsCallAlignedToRecorder is the G1 end-to-end alignment guard: a
// gapped agent track (teed only while the agent "speaks") streamed through the
// recordclient gRPC path must be reconstructed by the real recorder as a
// full-length, call-aligned stream — burst 2 at the absolute byte offset its
// call_us maps to, NOT slid up against burst 1. These are the exact offsets from
// recorder's TestAudioTimelineCallUsAlignment; this proves call_us survives the
// gateway→recorder gRPC hop intact.
func TestSendStreamsCallAlignedToRecorder(t *testing.T) {
	addr, dir := startRecorder(t)
	sess := session.NewRegistry().Create("sess")
	c, err := New(sess, addr, "")
	if err != nil {
		t.Fatal(err)
	}

	const (
		burst1Mark  = 0x11
		burst2Mark  = 0x22
		userMarker  = 0x33
		userFrames  = 100       // continuous mic 0..2000 ms
		burst1Count = 10        // agent speaks 0..200 ms
		burst2Start = 1_200_000 // ...silent 1 s, then speaks again
		burst2Count = 10        // 1200..1400 ms
	)
	// Push frames with explicit call_us straight onto the send path (controlled
	// timeline; Send's clock stamping is covered separately below).
	push := func(kind recorderpb.Kind, callUS uint64, marker byte) {
		c.ch <- &recorderpb.Frame{
			SessionId: "sess", Kind: kind, TsUs: callUS, CallUs: callUS, Data: filled(marker),
		}
	}
	for i := 0; i < userFrames; i++ {
		push(recorderpb.Kind_USER_PCM, uint64(i)*tFrameUS, userMarker)
	}
	for i := 0; i < burst1Count; i++ {
		push(recorderpb.Kind_AGENT_PCM, uint64(i)*tFrameUS, burst1Mark)
	}
	for i := 0; i < burst2Count; i++ {
		push(recorderpb.Kind_AGENT_PCM, burst2Start+uint64(i)*tFrameUS, burst2Mark)
	}
	c.Close() // half-close → recorder sees io.EOF → flushes files

	agent, err := os.ReadFile(filepath.Join(dir, "sess", "agent.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	burst1End := pcmByteOffset(burst1Count * tFrameUS)           // 200 ms  -> 19200
	burst2Off := pcmByteOffset(burst2Start)                      // 1200 ms -> 115200
	wantLen := pcmByteOffset(burst2Start + burst2Count*tFrameUS) // 1400 ms -> 134400

	// Full length including the silence-filled gap. (Raw-append would give 38400.)
	if int64(len(agent)) != wantLen {
		t.Fatalf("agent.pcm = %d bytes, want %d (gap not silence-filled across the gRPC hop)", len(agent), wantLen)
	}
	if !allBytes(agent[:burst1End], burst1Mark) {
		t.Errorf("region [0,%d) is not burst1", burst1End)
	}
	if !allBytes(agent[burst1End:burst2Off], 0x00) {
		t.Errorf("gap [%d,%d) is not silence — burst2 slid earlier", burst1End, burst2Off)
	}
	if !allBytes(agent[burst2Off:], burst2Mark) {
		t.Errorf("burst2 does not start at its call_us offset %d", burst2Off)
	}

	user, err := os.ReadFile(filepath.Join(dir, "sess", "user.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	if want := pcmByteOffset(userFrames * tFrameUS); int64(len(user)) != want {
		t.Errorf("user.pcm = %d bytes, want %d", len(user), want)
	}
}

// TestSendStampsCallUsFromClock proves Send sources call_us from session.CallUS()
// and nowhere else: after real time advances, a single frame sent via the public
// Send lands at the byte offset the call clock reported at send time — not at
// offset 0 (which is what arrival-order or a zero clock would produce).
func TestSendStampsCallUsFromClock(t *testing.T) {
	addr, dir := startRecorder(t)
	sess := session.NewRegistry().Create("sess2")
	c, err := New(sess, addr, "")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond) // let the call clock advance
	c0 := sess.CallUS()
	c.Send(recorderpb.Kind_USER_PCM, filled(0x44))
	c1 := sess.CallUS()
	c.Close()

	user, err := os.ReadFile(filepath.Join(dir, "sess2", "user.pcm"))
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := pcmByteOffset(c0), pcmByteOffset(c1)
	if lo < pcmByteOffset(250_000) {
		t.Fatalf("call clock barely advanced (offset %d); test not exercising the clock", lo)
	}
	got := firstNonZero(user)
	if got < lo || got > hi {
		t.Fatalf("frame at byte %d, want in [%d,%d] — call_us not stamped from CallUS()", got, lo, hi)
	}
}

// TestFinalizePostsToRecorder confirms Finalize issues POST /sessions/{id}/finalize
// and treats 202 as success. (Real gRPC for a clean Close; an httptest stub for
// the finalize POST so there's no ffmpeg dependency.)
func TestFinalizePostsToRecorder(t *testing.T) {
	addr, _ := startRecorder(t)
	var gotMethod, gotPath string
	hsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hsrv.Close()

	sess := session.NewRegistry().Create("sessF")
	c, err := New(sess, addr, hsrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if err := c.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sessions/sessF/finalize" {
		t.Errorf("got %s %s, want POST /sessions/sessF/finalize", gotMethod, gotPath)
	}
}
