package audio

import "encoding/binary"

// Upsampler converts a stream of 24 kHz mono s16le PCM to 48 kHz by ×2 linear
// interpolation: between each pair of input samples (a, b) it emits a, mid(a,b).
// It is STATEFUL — it holds each chunk's final sample so the midpoint spanning a
// chunk boundary is computed against the next chunk's first sample, not distorted
// by the split. It also holds a trailing odd byte: an s16le stream read off the
// network (resp.Body.Read) arrives split at arbitrary byte offsets, so a chunk can
// end mid-sample. The split sample's low half is carried into the next chunk, not
// dropped — dropping it would desync every later sample by one byte (high/low
// swapped → full-scale noise). One Upsampler per agent speech segment; Flush emits
// the held final sample at segment end (so the output is exactly 2× the input
// length). Not safe for concurrent use.
type Upsampler struct {
	prev    int16
	hasPrev bool
	pend    byte // low half of a sample split across a chunk boundary
	hasPend bool
}

// Up24to48 returns the 48 kHz output for one 24 kHz input chunk of s16le PCM. The
// chunk may end mid-sample (odd length): the trailing byte is held for the next
// chunk. The final whole sample is held back for the next chunk or Flush.
func (u *Upsampler) Up24to48(in []byte) []byte {
	out := make([]byte, 0, len(in)*2)
	i := 0
	// Complete a sample whose low half was held from the previous chunk.
	if u.hasPend {
		if len(in) == 0 {
			return out
		}
		out = u.push(out, int16(binary.LittleEndian.Uint16([]byte{u.pend, in[0]})))
		u.hasPend = false
		i = 1
	}
	for ; i+1 < len(in); i += 2 {
		out = u.push(out, int16(binary.LittleEndian.Uint16(in[i:])))
	}
	// Hold a trailing odd byte (the low half of a sample split across this read
	// boundary) until the next chunk supplies its high half.
	if i < len(in) {
		u.pend, u.hasPend = in[i], true
	}
	return out
}

// push interpolates one input sample against the held previous sample.
func (u *Upsampler) push(out []byte, cur int16) []byte {
	if u.hasPrev {
		out = appendSample(out, u.prev)
		out = appendSample(out, mid(u.prev, cur))
	}
	u.prev, u.hasPrev = cur, true
	return out
}

// Flush emits the final held sample (duplicated, since there is no next sample to
// interpolate toward), making the segment exactly 2× the input. Call once at the
// end of a speech segment; it resets the state.
func (u *Upsampler) Flush() []byte {
	u.pend, u.hasPend = 0, false
	if !u.hasPrev {
		return nil
	}
	out := appendSample(appendSample(make([]byte, 0, 4), u.prev), u.prev)
	u.prev, u.hasPrev = 0, false
	return out
}

func mid(a, b int16) int16 { return int16((int32(a) + int32(b)) / 2) }

func appendSample(b []byte, s int16) []byte {
	return binary.LittleEndian.AppendUint16(b, uint16(s))
}
