# Recorder — pure-Go fragmented MP4 writer (the high-risk path)

This is the single component most likely to be done wrong. Read it fully before
writing `internal/recorder/writer/`. It owns the **video-only** `video_raw.mp4`.
Audio is separate raw PCM (trivial). Library: `github.com/Eyevinn/mp4ff`
(pure-Go, supports fragmented/CMAF MP4). **No ffmpeg here** — ffmpeg is finalize-only.

## Why fragmented MP4 (the crash-safety requirement)

A normal MP4 puts its index (`moov` with the sample table) at the **end** — if the
process dies mid-call, the file has no index and won't play. KYC requires a
killed process to still leave a playable recording. A **fragmented MP4** solves
this: a small **init segment** (`ftyp` + `moov` with *no* samples) is written
**once up front**, then the media is appended as self-contained **fragments**
(`moof` + `mdat`), each flushed to disk as it completes. A player reads the init
segment plus whatever fragments are present — so every fragment flushed before a
crash is playable. This is the Go equivalent of the old code's
`movflags=frag_keyframe+empty_moov+default_base_moof`.

Two rules that make it crash-safe in practice:
1. Write + `Sync()`/flush the init segment immediately on the first kept keyframe.
2. Flush after **every** fragment. Never buffer fragments in memory waiting for a
   trailer. There is no trailer to write.

## Input

From the gRPC stream: `Frame{kind: VIDEO_AU, ts_us, data}` where `data` is one
H.264 access unit in **Annex-B** (start-code-delimited NALs), 90 kHz RTP
timestamp implied by ordering. WebRTC H.264 is **B-frame-free** → `dts == pts`.

## The writer state machine

```
state: not-started
  for each access unit:
    if not-started:
       if NOT a keyframe (no IDR/SPS): DROP it (an MP4 must open on a keyframe)
       else:
         extract SPS + PPS NALs from this AU
         build the init segment: ftyp + moov(one video trak, codec avc1,
            avcC from SPS/PPS, timescale = 90000, mvex/trex so fragments are legal)
         write + flush init segment
         first_ts = ts_us  (record first kept frame wall-clock for A/V offset)
         start a new fragment, begin-on-this-keyframe
         state = started
    if started:
       append this AU as one sample to the current fragment:
         sample.size  = len(AU as length-prefixed AVCC, see "Annex-B → AVCC")
         sample.dur   = next_ts - this_ts   (in 90 kHz units; last sample: reuse prev dur)
         sample.flags = keyframe ? sync : non-sync
         sample.pts = sample.dts = ts90k - first_ts90k  (unwrap 32-bit wrap)
       if this AU is a keyframe AND the current fragment already has samples:
         finalize the previous fragment (moof+mdat), flush, start a new one here
  on stop: finalize + flush the last fragment.
```

Fragment boundary = each keyframe (one GOP per fragment). Simple, and every
fragment is independently decodable.

## Annex-B → AVCC (the one easy-to-miss conversion)

WebRTC delivers NALs with Annex-B **start codes** (`00 00 00 01`). MP4 sample data
must be **length-prefixed** (4-byte big-endian length before each NAL), and SPS/PPS
go into `avcC` in the `moov`, **not** in the sample data. So per access unit:
- split into NALs on start codes,
- drop SPS(7)/PPS(8) from the sample payload (they live in `avcC`); keep
  VCL NALs (IDR=5, non-IDR=1) and others,
- prepend each kept NAL with its 4-byte length,
- concatenate → that is the sample's `mdat` bytes.

mp4ff has helpers for Annex-B ↔ AVCC and for building `avcC` from SPS/PPS — use
them rather than hand-rolling byte math. Read mp4ff's `examples/` (initsegment,
fragment, and the avc helper packages) for the exact API; the box names above are
stable ISO-BMFF terms that map directly onto its types.

## Timestamps

- 90 kHz throughout (the H.264 RTP clock). `time_base = 1/90000`.
- `pts = dts = ts90k - first_ts90k`, unwrapping the 32-bit space (gaps are tiny
  vs 2^31; if `delta < -2^31` add 2^32).
- Per-sample duration = difference to the next AU's timestamp; for the final
  sample reuse the previous duration.
- Record `first_frame_us` (wall-clock at the first kept keyframe). The combine
  step pairs it with the audio's first-frame time → `-itsoffset` for lip-sync.

## Threading

The mux runs on **one dedicated goroutine** fed by a buffered channel from the
gRPC handler. The handler only enqueues; it never blocks on disk. A single bad
access unit is logged and dropped — never fatal. On channel close, finalize the
last fragment and close the file.

## Hard stop

If you cannot produce a **playable-after-kill** fragmented MP4 with mp4ff
(init-segment + flushed fragments), **STOP and report it.** Do **not** silently
fall back to:
- a per-call ffmpeg subprocess (rebuilds the process/CPU wall this whole project
  removes), or
- a non-fragmented MP4 with `moov` at the end (defeats the crash-safety
  requirement).

Either of those is a design regression, not a fix. Raise it instead.

## Test it directly (before any gateway exists)

1. Feed the writer a canned H.264 Annex-B stream (extract one with
   `ffmpeg -i sample.mp4 -an -c:v copy -bsf:v h264_mp4toannexb out.h264`, then
   chunk it into access units).
2. Kill the process mid-stream. The partial `video_raw.mp4` must still play in
   VLC/ffplay. If it doesn't, the init-segment/flush discipline is wrong.
3. Let it finish normally; confirm duration and frame count match the input.
