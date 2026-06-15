// Package audio holds the gateway's fixed-ratio sample-rate converters. The only
// ratios are exact integers (GATEWAY.md): 48k→16k decimate-by-3 for STT (here);
// 24k→48k upsample-by-2 for the agent stream lands with the TTS path. No DSP
// library — exact integer ratios need none.
package audio

// Downsampler converts a stream of 48 kHz mono s16le PCM to 16 kHz by keeping
// every third sample (48000/16000 = 3). It is STATEFUL: the decimation phase
// carries across chunk boundaries, so a chunk whose sample count isn't a multiple
// of 3 does not restart the pattern — output samples are the input samples at
// global indices 0, 3, 6, … over the whole stream. One Downsampler per audio
// stream; not safe for concurrent use.
type Downsampler struct {
	phase int // position (0,1,2) of the NEXT input sample within its 3-sample group
}

// Down48to16 returns the 16 kHz output for one 48 kHz input chunk. The input is
// s16le whole samples (even length); any odd trailing byte is ignored.
func (d *Downsampler) Down48to16(in []byte) []byte {
	out := make([]byte, 0, len(in)/3+2)
	for i := 0; i+1 < len(in); i += 2 { // step one s16le sample at a time
		if d.phase == 0 {
			out = append(out, in[i], in[i+1])
		}
		d.phase = (d.phase + 1) % 3
	}
	return out
}
