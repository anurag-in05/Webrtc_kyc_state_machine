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
