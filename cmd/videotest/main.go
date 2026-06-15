package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Eyevinn/mp4ff/avc"

	"kyc-monorepo/internal/recorder"
)

// splitIntoAUs groups Annex-B NALUs into access units: each VCL slice (IDR or
// non-IDR), together with the non-VCL NALUs (SPS/PPS/SEI) that precede it, is
// one access unit — which is what Pion's OnTrack hands you in the real gateway.
func splitIntoAUs(annexB []byte) [][]byte {
	nalus := avc.ExtractNalusFromByteStream(annexB)
	var aus [][]byte
	var cur []byte
	for _, n := range nalus {
		cur = append(append(cur, 0, 0, 0, 1), n...) // re-add the start code
		switch avc.GetNaluType(n[0]) {
		case avc.NALU_IDR, avc.NALU_NON_IDR: // a slice ends the access unit
			aus = append(aus, cur)
			cur = nil
		}
	}
	return aus
}

func main() {
	annexB, err := os.ReadFile("test.h264")
	if err != nil {
		log.Fatal(err)
	}
	aus := splitIntoAUs(annexB)
	fmt.Printf("access units: %d\n", len(aus))

	crash := len(os.Args) > 1 && os.Args[1] == "crash"

	w := recorder.NewVideoWriterForTest("out.mp4")
	limit := len(aus)
	if crash {
		limit = 35 // feed only part, then exit without finalize
	}
	for i := 0; i < limit; i++ {
		w.Send(uint64(i)*33333, aus[i]) // 30fps → 33.333ms/frame, in microseconds
	}

	if crash {
		time.Sleep(200 * time.Millisecond) // let the mux flush completed fragments
		fmt.Println("simulating kill — no finalize")
		os.Exit(1)
	}

	w.Close()
	fmt.Println("wrote out.mp4")
}