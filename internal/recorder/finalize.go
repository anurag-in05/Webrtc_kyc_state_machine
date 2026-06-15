package recorder

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// Finalize status taxonomy (CONTRACTS §3).
const (
	statusPending   = "pending"
	statusComplete  = "complete"   // video + both audio tracks
	statusPartial   = "partial"    // video present, but some audio missing
	statusAudioOnly = "audio_only" // no usable video, audio recovered
	statusFailed    = "failed"     // nothing usable / ffmpeg failed
)

// combine runs the finalize ffmpeg pass for one finished session directory and
// returns the resulting status and the output path ("" on failure). It is the
// ONE ffmpeg invocation in the whole recorder — a stream-copy of the video plus
// a stereo merge of the two PCM tracks (user=left, agent=right). No re-encode of
// video; that would rebuild the per-call CPU wall this project removed.
func combine(dir string) (status string, outPath string) {
	out := filepath.Join(dir, "full_call.mp4")
	hasVideo := nonEmpty(filepath.Join(dir, "video_raw.mp4"))
	hasUser := nonEmpty(filepath.Join(dir, "user.pcm"))
	hasAgent := nonEmpty(filepath.Join(dir, "agent.pcm"))

	if !hasVideo && !hasUser && !hasAgent {
		return statusFailed, "" // nothing was captured
	}

	off := videoOffsetSeconds(dir)
	args := buildFFmpegArgs(dir, hasVideo, hasUser, hasAgent, off, out)
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		log.Printf("recorder: ffmpeg combine failed in %s: %v\n%s", dir, err, b)
		// Video may be the problem (e.g. truncated mid-fragment). If we still
		// have audio, fall back to an audio-only artifact rather than lose the
		// whole recording (invariant 4: degrade, don't fail).
		if hasVideo && (hasUser || hasAgent) {
			a2 := buildFFmpegArgs(dir, false, hasUser, hasAgent, 0, out)
			if _, e2 := exec.Command("ffmpeg", a2...).CombinedOutput(); e2 == nil {
				return statusAudioOnly, out
			}
		}
		return statusFailed, ""
	}

	switch {
	case hasVideo && hasUser && hasAgent:
		return statusComplete, out
	case hasVideo:
		return statusPartial, out // video + at most one audio track
	default:
		return statusAudioOnly, out // no video, audio combined
	}
}

// buildFFmpegArgs assembles the ffmpeg argument vector for the inputs present.
// Raw PCM needs its format flags (-f s16le -ar 48000 -ac 1) BEFORE its -i.
// off > 0 means video started later than audio → delay video. (With the call_us
// scheme audio always starts at t=0, so off is never negative; the audioOff
// branch is kept defensive.) Input indices are tracked explicitly so the filter
// graph references the right streams regardless of which inputs exist.
func buildFFmpegArgs(dir string, hasVideo, hasUser, hasAgent bool, off float64, out string) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-y"}
	idx := 0
	videoIdx, userIdx, agentIdx := -1, -1, -1

	if hasVideo {
		if off > 0 {
			a = append(a, "-itsoffset", fmt.Sprintf("%.3f", off)) // delay video
		}
		a = append(a, "-i", filepath.Join(dir, "video_raw.mp4"))
		videoIdx = idx
		idx++
	}

	audioOff := ""
	if off < 0 {
		audioOff = fmt.Sprintf("%.3f", -off) // delay audio instead
	}
	if hasUser {
		if audioOff != "" {
			a = append(a, "-itsoffset", audioOff)
		}
		a = append(a, "-f", "s16le", "-ar", "48000", "-ac", "1", "-i", filepath.Join(dir, "user.pcm"))
		userIdx = idx
		idx++
	}
	if hasAgent {
		if audioOff != "" {
			a = append(a, "-itsoffset", audioOff)
		}
		a = append(a, "-f", "s16le", "-ar", "48000", "-ac", "1", "-i", filepath.Join(dir, "agent.pcm"))
		agentIdx = idx
		idx++
	}

	// Audio routing: both tracks → stereo (user left, agent right); one track →
	// that mono track as-is.
	var filter, amap string
	switch {
	case hasUser && hasAgent:
		filter = fmt.Sprintf("[%d:a][%d:a]amerge=inputs=2[m];[m]pan=stereo|FL=c0|FR=c1[a]", userIdx, agentIdx)
		amap = "[a]"
	case hasUser:
		amap = fmt.Sprintf("%d:a", userIdx)
	case hasAgent:
		amap = fmt.Sprintf("%d:a", agentIdx)
	}
	if filter != "" {
		a = append(a, "-filter_complex", filter)
	}
	if hasVideo {
		a = append(a, "-map", fmt.Sprintf("%d:v", videoIdx))
	}
	if amap != "" {
		a = append(a, "-map", amap)
	}
	if hasVideo {
		a = append(a, "-c:v", "copy") // passthrough — never re-encode video
	}
	if hasUser || hasAgent {
		a = append(a, "-c:a", "aac")
	}
	if hasVideo && (hasUser || hasAgent) {
		a = append(a, "-shortest")
	}
	return append(a, out)
}

func nonEmpty(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// recordingMeta carries the one timing value the finalize pass needs from the
// recording pass (session.go): the first kept keyframe's call_us. It's persisted
// to meta.json because finalize runs later — possibly after a process restart —
// so the offset must survive on disk.
type recordingMeta struct {
	VideoFirstCallUS uint64 `json:"video_first_call_us"`
}

// videoOffsetSeconds reads meta.json and returns how far to delay video at
// combine. Audio sample 0 is call_us 0 by construction, so the offset is simply
// the video's first-keyframe call_us in seconds. 0 when meta is missing.
func videoOffsetSeconds(dir string) float64 {
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return 0
	}
	var m recordingMeta
	if json.Unmarshal(b, &m) != nil {
		return 0
	}
	return float64(m.VideoFirstCallUS) / 1e6
}
