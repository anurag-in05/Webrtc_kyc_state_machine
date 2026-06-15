package audio

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func pcm(vals ...int16) []byte {
	b := make([]byte, len(vals)*2)
	for i, v := range vals {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

func samples(b []byte) []int16 {
	s := make([]int16, len(b)/2)
	for i := range s {
		s[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return s
}

// One chunk, exact: 9 samples → keep global indices 0,3,6 (sample-exact values).
func TestDown48to16SingleChunk(t *testing.T) {
	var d Downsampler
	out := d.Down48to16(pcm(10, 11, 12, 13, 14, 15, 16, 17, 18))
	if got, want := samples(out), []int16{10, 13, 16}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The key streaming property: the decimation phase carries across a chunk
// boundary whose sample count is NOT a multiple of 3. chunk1 has 4 samples
// (indices 0..3), chunk2 has 5 (indices 4..8). Continuous decimation keeps global
// indices 0,3,6 → values 10,13,16. A per-chunk phase reset would instead keep
// chunk2's local index 0 and 3 (values 14,17) — this asserts that doesn't happen.
func TestDown48to16PhaseAcrossChunks(t *testing.T) {
	var d Downsampler
	out1 := d.Down48to16(pcm(10, 11, 12, 13))     // global idx 0..3
	out2 := d.Down48to16(pcm(14, 15, 16, 17, 18)) // global idx 4..8
	got := append(samples(out1), samples(out2)...)
	want := []int16{10, 13, 16}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (decimation phase not carried across the chunk boundary)", got, want)
	}
}
