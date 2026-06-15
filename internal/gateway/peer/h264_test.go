package peer

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/pion/rtp"
)

// Single-NAL RTP payloads (Pion emits these as Annex-B NALs on Unmarshal). The
// header byte is forbidden_zero(1) | nal_ref_idc(2) | type(5).
var (
	sps    = []byte{0x67, 0x42, 0x00, 0x1f} // SPS, type 7
	pps    = []byte{0x68, 0xce, 0x3c, 0x80} // PPS, type 8
	idr    = []byte{0x65, 0x11, 0x22, 0x33} // IDR slice, type 5 (keyframe)
	nonIDR = []byte{0x41, 0x44, 0x55}       // non-IDR slice, type 1 (P-frame)

	// One IDR fragmented across two FU-A packets (NRI=3, type=5).
	idrData1 = []byte{0xaa, 0xbb}
	idrData2 = []byte{0xcc, 0xdd}
	fuaStart = append([]byte{0x7c, 0x85}, idrData1...) // FU indicator 0x7c, FU header S|type5
	fuaEnd   = append([]byte{0x7c, 0x45}, idrData2...) // FU header E|type5
)

func pkt(ts uint32, marker bool, payload []byte) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Timestamp: ts, Marker: marker}, Payload: payload}
}

// drive feeds packets and collects every emitted access unit (flushing the tail).
func drive(a *h264Assembler, pkts ...*rtp.Packet) [][]byte {
	var aus [][]byte
	for _, p := range pkts {
		if au := a.push(p); au != nil {
			aus = append(aus, au)
		}
	}
	if au := a.flush(); au != nil {
		aus = append(aus, au)
	}
	return aus
}

func nalTypes(au []byte) []avc.NaluType {
	var out []avc.NaluType
	for _, n := range avc.ExtractNalusFromByteStream(au) {
		if len(n) > 0 {
			out = append(out, avc.GetNaluType(n[0]))
		}
	}
	return out
}

func sameTypes(got []avc.NaluType, want ...avc.NaluType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func lastNal(au []byte) []byte {
	nals := avc.ExtractNalusFromByteStream(au)
	if len(nals) == 0 {
		return nil
	}
	return nals[len(nals)-1]
}

// The normal WebRTC case: SPS, PPS, IDR all inline in one access unit → emitted
// unchanged (no cache prepend, no duplication).
func TestKeyframeWithInlineParamSets(t *testing.T) {
	var a h264Assembler
	aus := drive(&a, pkt(1, false, sps), pkt(1, false, pps), pkt(1, true, idr))
	if len(aus) != 1 {
		t.Fatalf("got %d AUs, want 1", len(aus))
	}
	if !sameTypes(nalTypes(aus[0]), avc.NALU_SPS, avc.NALU_PPS, avc.NALU_IDR) {
		t.Errorf("AU = %v, want SPS,PPS,IDR", nalTypes(aus[0]))
	}
}

// THE FIX: a bare keyframe (no inline parameter sets) still ships SPS+PPS inline,
// pulled from sets cached off an earlier parameter-set-only AU. Without this the
// recorder would stall forever on writeInit.
func TestKeyframeMissingParamSetsGetsThemFromCache(t *testing.T) {
	var a h264Assembler
	aus := drive(&a,
		pkt(1, false, sps), pkt(1, true, pps), // parameter-set-only AU → cached, not emitted
		pkt(2, true, idr), // bare keyframe, no inline SPS/PPS
	)
	if len(aus) != 1 {
		t.Fatalf("got %d AUs, want 1 (param-set-only AU dropped, keyframe emitted)", len(aus))
	}
	if !sameTypes(nalTypes(aus[0]), avc.NALU_SPS, avc.NALU_PPS, avc.NALU_IDR) {
		t.Errorf("keyframe AU = %v, want cached SPS,PPS prepended before IDR", nalTypes(aus[0]))
	}
}

// Nothing is forwarded before the first keyframe (an MP4 must open on one); after
// it, P-frames pass through.
func TestDropsUntilFirstKeyframe(t *testing.T) {
	var a h264Assembler
	aus := drive(&a,
		pkt(1, true, nonIDR),                                      // slice before any keyframe → dropped
		pkt(2, false, sps), pkt(2, false, pps), pkt(2, true, idr), // keyframe
		pkt(3, true, nonIDR), // now started → P-frame forwarded
	)
	if len(aus) != 2 {
		t.Fatalf("got %d AUs, want 2 (pre-keyframe slice dropped)", len(aus))
	}
	if !sameTypes(nalTypes(aus[0]), avc.NALU_SPS, avc.NALU_PPS, avc.NALU_IDR) {
		t.Errorf("first AU = %v, want keyframe", nalTypes(aus[0]))
	}
	if !sameTypes(nalTypes(aus[1]), avc.NALU_NON_IDR) {
		t.Errorf("second AU = %v, want P-frame", nalTypes(aus[1]))
	}
}

// An IDR fragmented across FU-A packets is reassembled into one keyframe AU.
func TestReassemblesFragmentedKeyframe(t *testing.T) {
	var a h264Assembler
	aus := drive(&a,
		pkt(1, false, sps), pkt(1, false, pps),
		pkt(1, false, fuaStart), pkt(1, true, fuaEnd), // IDR split across two FU-A packets
	)
	if len(aus) != 1 {
		t.Fatalf("got %d AUs, want 1", len(aus))
	}
	if !sameTypes(nalTypes(aus[0]), avc.NALU_SPS, avc.NALU_PPS, avc.NALU_IDR) {
		t.Errorf("AU = %v, want SPS,PPS,IDR (FU-A reassembled)", nalTypes(aus[0]))
	}
	want := append([]byte{0x65}, append(append([]byte{}, idrData1...), idrData2...)...)
	if got := lastNal(aus[0]); !bytes.Equal(got, want) {
		t.Errorf("reassembled IDR = %x, want %x", got, want)
	}
}

// A keyframe with no parameter sets seen anywhere is held back (undecodable),
// then recovered once SPS/PPS and a fresh keyframe arrive.
func TestKeyframeWithoutAnyParamSetsIsHeldBack(t *testing.T) {
	var a h264Assembler
	if aus := drive(&a, pkt(1, true, idr)); len(aus) != 0 {
		t.Fatalf("got %d AUs, want 0 (undecodable keyframe held back)", len(aus))
	}
	aus := drive(&a, pkt(2, false, sps), pkt(2, false, pps), pkt(2, true, idr))
	if len(aus) != 1 {
		t.Fatalf("recovery: got %d AUs, want 1", len(aus))
	}
	if !sameTypes(nalTypes(aus[0]), avc.NALU_SPS, avc.NALU_PPS, avc.NALU_IDR) {
		t.Errorf("recovered AU = %v, want SPS,PPS,IDR", nalTypes(aus[0]))
	}
}

// TestFinalAccessUnitFlushedAtTeardown locks the timestamp-boundary choice
// against G2b's close path. An AU is emitted only when the NEXT timestamp arrives,
// so the last frame of a call sits buffered when the stream ends. G2b's
// OnTrack(video) goroutine (which owns the assembler) MUST call flush() when its
// ReadRTP loop exits (track end / peer close) and forward that final AU, or it is
// silently dropped. This test feeds frames, confirms the last one is still
// buffered (not emitted by push), then flushes the way teardown will and asserts
// it comes out.
func TestFinalAccessUnitFlushedAtTeardown(t *testing.T) {
	var a h264Assembler
	var emitted [][]byte
	push := func(p *rtp.Packet) {
		if au := a.push(p); au != nil {
			emitted = append(emitted, au)
		}
	}
	push(pkt(1, false, sps))
	push(pkt(1, false, pps))
	push(pkt(1, true, idr))    // keyframe (ts=1)
	push(pkt(2, true, nonIDR)) // ts=2 arrives → keyframe (ts=1) emitted
	push(pkt(3, true, nonIDR)) // ts=3 arrives → P-frame (ts=2) emitted; ts=3 now buffered

	// Stream still open: the final frame (ts=3) has NOT been emitted by push.
	if len(emitted) != 2 {
		t.Fatalf("before teardown: %d AUs emitted, want 2 (final frame must still be buffered)", len(emitted))
	}

	// Teardown: the read loop ends and flushes the final buffered AU.
	final := a.flush()
	if final == nil {
		t.Fatal("teardown flush() returned nil — final access unit was dropped")
	}
	if !sameTypes(nalTypes(final), avc.NALU_NON_IDR) {
		t.Errorf("final AU = %v, want the buffered P-frame", nalTypes(final))
	}

	// A second flush (a double /close racing a disconnect) is a safe no-op.
	if a.flush() != nil {
		t.Error("second flush() should be a no-op")
	}
}
