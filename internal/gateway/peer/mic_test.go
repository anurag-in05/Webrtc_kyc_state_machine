package peer

import (
	"sync"
	"testing"
)

// TestMicGate: feedMic delivers to the handler only while one is set (the
// listening window); frames with no handler are discarded.
func TestMicGate(t *testing.T) {
	var p Peer
	var got [][]byte

	p.feedMic([]byte{1}) // no handler yet → discarded

	p.SetMic(func(b []byte) { got = append(got, append([]byte(nil), b...)) })
	p.feedMic([]byte{2, 3})
	p.feedMic([]byte{4})

	p.SetMic(nil)        // listening window closed
	p.feedMic([]byte{5}) // discarded

	if len(got) != 2 || string(got[0]) != string([]byte{2, 3}) || string(got[1]) != string([]byte{4}) {
		t.Fatalf("got %v, want [[2 3] [4]] (frames outside the window discarded)", got)
	}
}

// TestMicGateConcurrent exercises SetMic racing feedMic (run under -race): the
// audio reader feeds while the turn loop opens/closes the window.
func TestMicGateConcurrent(t *testing.T) {
	var p Peer
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // the audio reader
		defer wg.Done()
		buf := []byte{1, 2}
		for {
			select {
			case <-stop:
				return
			default:
				p.feedMic(buf)
			}
		}
	}()

	for i := 0; i < 1000; i++ { // the turn loop toggling the window
		var n int
		p.SetMic(func([]byte) { n++ })
		p.SetMic(nil)
	}
	close(stop)
	wg.Wait()
}
