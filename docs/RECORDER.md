# Recorder (Go) — durable capture

Binary: `cmd/recorder`, gRPC `:9091`, HTTP `:9090`. Same Go module as the gateway.
This replaces the old recorder logic + `app/services/mux.py`. Read
`docs/CONTRACTS.md` §3 first. Build and test this FIRST (it needs nothing else).

## Job

Receive tagged media frames, append to crash-safe files during the call, run one
ffmpeg combine at finalize, upload to S3, report status. **Never fails the call**
(it's a separate process; if it dies, the conversation continues).

## During the call — three append-only files per session

`recordings/{session_id}/`:
- `video_raw.mp4` — H.264 access units muxed as a **fragmented MP4**
  (`movflags = frag_keyframe+empty_moov+default_base_moof`, exactly the old
  crash-safety flag). A killed process still leaves a playable file.
- `user.pcm`  — raw s16le mono 48 kHz. Raw PCM has no header → append is
  trivially crash-safe; nothing to fix up on crash.
- `agent.pcm` — same format.

Record the wall-clock (or first `ts_us`) of the first kept video frame and the
first audio frame → the A/V start offset for lip-sync at combine.

### Video writer rules (port of `PassthroughVideoWriter`)

- Drop access units until the first keyframe (IDR/SPS), else the MP4 won't open.
- H.264 stream, `time_base = 1/90000`, `pts = dts = ts_90k - first_ts`
  (WebRTC H.264 is B-frame-free, so dts == pts). Unwrap the 32-bit RTP timestamp.
- Do the mux on a dedicated goroutine fed by a channel — never block the gRPC
  handler. A single bad AU is logged and dropped, not fatal.
- Pure-Go MP4 muxer (e.g. `github.com/abema/go-mp4` or equivalent). **No ffmpeg
  subprocess during the call** — a per-call subprocess rebuilds the CPU/process
  wall we're removing. If a pure-Go fragmented-MP4 writer turns out infeasible,
  STOP and ask — don't silently fall back to a per-call ffmpeg.

## At finalize — one ffmpeg, video stream-copied

`POST /sessions/{id}/finalize` kicks this async (returns immediately):

1. Build the stereo audio: user.pcm → Left, agent.pcm → Right (port the merge
   from `mux.py`: pad the shorter side with silence; resample if needed).
2. One ffmpeg: `-c:v copy` the fragmented MP4 video + the stereo audio (encode
   AAC once) + apply the A/V start offset (`-itsoffset`) → `full_call.mp4`.
   ffmpeg runs **once per call, at the end** — off every hot path. That's fine.
3. Upload `full_call.mp4` to S3 (`AWS_S3_BUCKET` under `AWS_MEDIA_FOLDER/{sid}/`),
   stream the upload. Local-dir fallback if S3 unset (mirror the old behavior).
4. Set status (see taxonomy) and write `recording_meta.json` (status + sizes +
   offset) next to the file, so a poll can read it.

### Status taxonomy (port from `mux.py`, keep the exact words)

| status | meaning |
|---|---|
| `complete` | video present and within ~5 s of audio length |
| `partial` | video truncated, or some frames missing |
| `audio_only` | no usable video → MP4 is audio only |
| `failed` | ffmpeg returned non-zero (PCM files + transcript are unaffected) |

## Package layout (`internal/recorder/`)

```
ingest/   gRPC RecordStream server: route Frame.kind → the right writer
writer/   fragmented-MP4 video writer (goroutine) + two PCM appenders
combine/  stereo build + ffmpeg invocation + status taxonomy
upload/   S3 streaming upload (+ local fallback)
status/   in-memory {session_id → status,url}, served by GET /status
```

## Config (env)

`GRPC_ADDR=:9091`, `HTTP_ADDR=:9090`, `RECORDINGS_DIR=./recordings`,
`AWS_S3_BUCKET`, `AWS_MEDIA_FOLDER`, `AWS_REGION`, `AWS_URL`, AWS creds (or IAM).
Nothing else.

## Do NOT

- decode/transcode video at any point (passthrough copy only),
- run ffmpeg during the call (only at finalize),
- add a database, queue, or per-frame ack beyond the gRPC stream's own flow control,
- let any error propagate to the gateway as a call-failing error — log, degrade
  the recording status, move on.
