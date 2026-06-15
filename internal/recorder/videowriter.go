package recorder

import (
	"encoding/binary"
	"log"
	"os"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// videoWriter turns a stream of H.264 access units (Annex-B) into a crash-safe
// fragmented MP4 — see docs/RECORDER_MP4.md. One GOP = one fragment, flushed to
// disk the moment it completes, so a killed process still leaves a playable file
// containing every fragment flushed before the crash.
//
// Concrete type, no interface (CLAUDE.md: no interfaces for single impls). All
// muxing runs on ONE dedicated goroutine fed by a buffered channel; send() only
// enqueues and never blocks the gRPC reader on disk I/O (RECORDER_MP4.md).
type videoWriter struct {
	path string
	ch   chan auFrame
	done chan struct{}
}

type auFrame struct {
	tsUS uint64
	data []byte
}

func newVideoWriter(path string) *videoWriter {
	w := &videoWriter{
		path: path,
		ch:   make(chan auFrame, 256), // ~8s at 30fps before backpressure
		done: make(chan struct{}),
	}
	go w.run()
	return w
}

// send enqueues one access unit. Never blocks: if the mux can't keep up we drop
// the frame. Degrades the recording; never stalls the call (invariant 4).
func (w *videoWriter) send(tsUS uint64, au []byte) {
	b := make([]byte, len(au)) // copy: we don't own the gRPC buffer past this call
	copy(b, au)
	select {
	case w.ch <- auFrame{tsUS: tsUS, data: b}:
	default:
		log.Printf("recorder: video buffer full, dropping access unit")
	}
}

// close stops the mux goroutine and waits for the final fragment to be flushed.
func (w *videoWriter) close() {
	close(w.ch)
	<-w.done
}

func (w *videoWriter) run() {
	defer close(w.done)

	f, err := os.Create(w.path)
	if err != nil {
		log.Printf("recorder: create %s: %v", w.path, err)
		for range w.ch { // drain so a future blocking producer can't stall
		}
		return
	}
	defer f.Close()

	var st muxState
	for au := range w.ch {
		st.handle(f, au.tsUS, au.data)
	}
	st.finalize(f) // flush the held sample + last fragment on clean stop
}

const (
	videoTimescale = 90000 // H.264 RTP clock (RECORDER_MP4.md)
	defaultDur90k  = 3000  // 1/30s; used only for a single-sample file
)

// muxState is the fragmented-MP4 state machine. It lives entirely on the mux
// goroutine, so it needs no locks.
type muxState struct {
	started   bool
	firstTS90 uint64 // 90kHz timestamp of the first kept keyframe

	seq  uint32        // fragment sequence number (1-based)
	frag *mp4.Fragment // fragment currently being filled

	// One-sample lookahead: a sample's duration is (next AU ts - this AU ts),
	// known only when the NEXT AU arrives. So we hold the current sample
	// "pending" until then.
	pending   bool
	pendDTS90 uint64 // pending sample decode time, relative to firstTS90
	pendData  []byte // pending sample bytes (AVCC, length-prefixed)
	pendIsKey bool
	lastDur   uint32 // most recent computed duration (reused for the final sample)
}

func usTo90k(tsUS uint64) uint64 { return tsUS * 9 / 100 } // 90000 / 1_000_000

func (m *muxState) handle(f *os.File, tsUS uint64, au []byte) {
	ts90 := usTo90k(tsUS)

	sample, isKey, ok := buildSample(au)
	if !ok {
		return // no VCL NALs (e.g. an SPS/PPS-only AU) → nothing to record
	}

	if !m.started {
		if !isKey {
			return // an MP4 must open on a keyframe; drop until one arrives
		}
		spss, ppss := avc.GetParameterSetsFromByteStream(au)
		if len(spss) == 0 || len(ppss) == 0 {
			// WebRTC normally delivers SPS+PPS inline in the keyframe AU. If a
			// keyframe arrives without them we can't build avcC — wait for one
			// that has them rather than write an undecodable file.
			log.Printf("recorder: first keyframe lacks SPS/PPS; waiting for one with them")
			return
		}
		if err := writeInit(f, spss, ppss); err != nil {
			log.Printf("recorder: write init segment: %v", err)
			return // can't start the file; keep dropping (recording ends up failed)
		}
		m.started = true
		m.firstTS90 = ts90
		m.seq = 1
		m.frag, _ = mp4.CreateFragment(m.seq, 1) // trackID = 1
		m.setPending(0, sample, true)            // first sample's relative dts is 0
		return
	}

	dtsRel := ts90 - m.firstTS90
	dur := uint32(dtsRel - m.pendDTS90) // duration of the PENDING sample = gap to this AU
	m.flushPending(dur)                 // pending now has a known duration → add it

	if isKey {
		// the current fragment already holds the sample we just added → roll it.
		m.finishFragment(f)
		m.seq++
		m.frag, _ = mp4.CreateFragment(m.seq, 1)
	}
	m.setPending(dtsRel, sample, isKey)
}

func (m *muxState) setPending(dtsRel uint64, data []byte, isKey bool) {
	m.pending, m.pendDTS90, m.pendData, m.pendIsKey = true, dtsRel, data, isKey
}

// flushPending adds the held sample to the current fragment with the given duration.
func (m *muxState) flushPending(dur uint32) {
	if !m.pending {
		return
	}
	if dur == 0 {
		dur = m.lastDur // duplicate/non-monotonic ts: fall back rather than write 0
	}
	flags := mp4.SetNonSyncSampleFlags(0)
	if m.pendIsKey {
		flags = mp4.SetSyncSampleFlags(0) // keyframe = seekable sync sample
	}
	m.frag.AddFullSample(mp4.FullSample{
		Sample:     mp4.Sample{Flags: flags, Dur: dur, Size: uint32(len(m.pendData))},
		DecodeTime: m.pendDTS90,
		Data:       m.pendData,
	})
	m.lastDur = dur
	m.pending = false
}

func (m *muxState) finishFragment(f *os.File) {
	if m.frag == nil {
		return
	}
	if err := m.frag.Encode(f); err != nil {
		log.Printf("recorder: encode fragment: %v", err)
		m.frag = nil
		return
	}
	f.Sync() // flush THIS fragment to disk now — the crash-safety guarantee
	m.frag = nil
}

func (m *muxState) finalize(f *os.File) {
	if !m.started {
		return // never got a keyframe; nothing was written
	}
	dur := m.lastDur
	if dur == 0 {
		dur = defaultDur90k // single-sample file: no previous duration to reuse
	}
	m.flushPending(dur)
	m.finishFragment(f)
}

// buildSample converts one Annex-B access unit into a length-prefixed (AVCC)
// MP4 sample. It drops SPS/PPS (those live in avcC, never in sample data) and
// reports whether the AU is a keyframe (contains an IDR slice).
func buildSample(au []byte) (sample []byte, isKey bool, ok bool) {
	nalus := avc.ExtractNalusFromByteStream(au)
	var lp [4]byte
	hasVCL := false
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		switch avc.GetNaluType(nalu[0]) {
		case avc.NALU_SPS, avc.NALU_PPS:
			continue // parameter sets go in avcC, not in samples
		case avc.NALU_IDR:
			isKey, hasVCL = true, true
		case avc.NALU_NON_IDR:
			hasVCL = true
		}
		binary.BigEndian.PutUint32(lp[:], uint32(len(nalu)))
		sample = append(sample, lp[:]...)
		sample = append(sample, nalu...)
	}
	return sample, isKey, hasVCL
}

func writeInit(f *os.File, spss, ppss [][]byte) error {
	init := mp4.CreateEmptyInit()                              // ftyp + moov + mvex
	trak := init.AddEmptyTrack(videoTimescale, "video", "und") // + trex (fragments legal)
	// includePS=true writes SPS/PPS into the avcC box (required for an avc1
	// track). The sample payload still excludes them — see buildSample.
	if err := trak.SetAVCDescriptor("avc1", spss, ppss, true); err != nil {
		return err
	}
	if err := init.Encode(f); err != nil {
		return err
	}
	return f.Sync() // flush the init segment immediately (RECORDER_MP4.md rule 1)
}