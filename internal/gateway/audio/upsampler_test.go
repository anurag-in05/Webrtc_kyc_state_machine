package audio

import (
	"reflect"
	"testing"
)

// Whole segment, sample-exact: 3 samples → 6 (×2) via interpolated midpoints, with
// the final sample duplicated by Flush.
func TestUp24to48SingleSegment(t *testing.T) {
	var u Upsampler
	out := u.Up24to48(pcm(100, 200, 300))
	out = append(out, u.Flush()...)
	if got, want := samples(out), []int16{100, 150, 200, 250, 300, 300}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The key streaming property: splitting the input at an ODD boundary (after 1
// sample) must yield byte-identical output to the whole segment — the midpoint
// across the boundary (mid(100,200)=150) is computed against the next chunk's
// first sample, not lost. A stateless per-chunk upsampler would distort here.
func TestUp24to48OddChunkBoundary(t *testing.T) {
	var u Upsampler
	out := u.Up24to48(pcm(100)) // 1 sample (odd boundary): nothing emitted yet
	out = append(out, u.Up24to48(pcm(200, 300))...)
	out = append(out, u.Flush()...)
	if got, want := samples(out), []int16{100, 150, 200, 250, 300, 300}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (interpolation phase not carried across the chunk boundary)", got, want)
	}
}

// The real streaming hazard: a network read of an s16le stream can end on an ODD
// BYTE (mid-sample), since resp.Body.Read splits at arbitrary offsets. Feeding the
// byte stream split at an odd offset must produce output identical to feeding it
// whole — the split sample's low byte is carried, not dropped. Dropping it desyncs
// every later sample by one byte (high/low swapped → noise). This is what produced
// the agent-voice static.
func TestUp24to48OddByteBoundary(t *testing.T) {
	whole := pcm(100, 200, 300, 400, 500)

	var ref Upsampler
	want := ref.Up24to48(whole)
	want = append(want, ref.Flush()...)

	// Split after 3 bytes: 1 whole sample + the low byte of the 2nd (odd offset).
	var u Upsampler
	got := u.Up24to48(whole[:3])
	got = append(got, u.Up24to48(whole[3:])...)
	got = append(got, u.Flush()...)

	if !reflect.DeepEqual(samples(got), samples(want)) {
		t.Fatalf("odd byte split desynced:\n got %v\nwant %v", samples(got), samples(want))
	}
}
