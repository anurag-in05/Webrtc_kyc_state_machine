package peer

import (
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// annexBStart is the 4-byte Annex-B NAL start code Pion emits and the recorder
// expects on VIDEO_AU frames.
var annexBStart = []byte{0x00, 0x00, 0x00, 0x01}

// h264Assembler turns a video track's RTP packets into Annex-B H.264 access
// units for the recorder (VIDEO_AU). It:
//
//   - reassembles NALs across packets — Pion handles STAP-A aggregation and FU-A
//     fragmentation;
//   - groups NALs into one access unit per RTP timestamp;
//   - drops everything until the first keyframe (an MP4 must open on one);
//   - caches SPS/PPS and guarantees the first forwarded keyframe AU carries them
//     INLINE. Browsers normally send parameter sets inline with each IDR, but if
//     one arrives without them the recorder's writeInit stalls forever
//     ("first keyframe lacks SPS/PPS; waiting...", videowriter.go). This makes
//     inline parameter sets a guarantee rather than a hope.
//
// Not safe for concurrent use: one video track is read by one goroutine.
type h264Assembler struct {
	depacket codecs.H264Packet // Pion FU-A/STAP-A reassembly → Annex-B NALs

	sps, pps []byte // most-recent parameter sets (Annex-B, start-code prefixed)
	started  bool   // has the opening keyframe been emitted?

	buf    []byte // current access unit, accumulated across one RTP timestamp
	bufTS  uint32
	hasBuf bool
}

// push feeds one RTP packet. It returns a completed access unit when this packet
// opens a new one (a timestamp change closes the previous AU), otherwise nil.
// Call flush at end of stream to drain the final AU.
func (a *h264Assembler) push(pkt *rtp.Packet) []byte {
	var au []byte
	if a.hasBuf && pkt.Timestamp != a.bufTS {
		au = a.complete()
		a.buf, a.hasBuf = nil, false
	}
	if nals, err := a.depacket.Unmarshal(pkt.Payload); err == nil && len(nals) > 0 {
		a.buf = append(a.buf, nals...)
	}
	a.bufTS, a.hasBuf = pkt.Timestamp, true
	return au
}

// flush returns the final buffered access unit (or nil) at end of stream.
func (a *h264Assembler) flush() []byte {
	if !a.hasBuf {
		return nil
	}
	au := a.complete()
	a.buf, a.hasBuf = nil, false
	return au
}

// complete turns the buffered NALs into a forwardable access unit, applying the
// drop-until-keyframe and inline-parameter-set rules. It returns nil when the AU
// is dropped: a non-picture AU (parameter sets are cached, nothing to record), a
// pre-keyframe slice, or a keyframe with no parameter sets seen anywhere yet
// (undecodable — wait for a complete one).
func (a *h264Assembler) complete() []byte {
	var hasSPS, hasPPS, isKey, hasVCL bool
	for _, n := range avc.ExtractNalusFromByteStream(a.buf) {
		if len(n) == 0 {
			continue
		}
		switch avc.GetNaluType(n[0]) {
		case avc.NALU_SPS:
			hasSPS, a.sps = true, withStartCode(n)
		case avc.NALU_PPS:
			hasPPS, a.pps = true, withStartCode(n)
		case avc.NALU_IDR:
			isKey, hasVCL = true, true
		case avc.NALU_NON_IDR:
			hasVCL = true
		}
	}
	if !hasVCL {
		return nil // parameter-set-only / non-picture AU: cached above, nothing to record
	}
	if !a.started && !isKey {
		return nil // an MP4 must open on a keyframe; drop until one arrives
	}

	out := a.buf
	if isKey && (!hasSPS || !hasPPS) {
		if a.sps == nil || a.pps == nil {
			return nil // keyframe but no parameter sets seen anywhere → undecodable; wait
		}
		// The fix: prepend the missing cached parameter sets so the keyframe AU is
		// self-contained, whether or not the encoder sent them inline.
		var prefix []byte
		if !hasSPS {
			prefix = append(prefix, a.sps...)
		}
		if !hasPPS {
			prefix = append(prefix, a.pps...)
		}
		out = append(prefix, a.buf...)
	}
	if isKey {
		a.started = true
	}
	return out
}

func withStartCode(nal []byte) []byte {
	b := make([]byte, 0, len(annexBStart)+len(nal))
	b = append(b, annexBStart...)
	return append(b, nal...)
}
