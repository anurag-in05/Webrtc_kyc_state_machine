# Worklog — Recorder Step 4 (finalize) + audio-timeline correctness fix

Build journal for bringing the recorder's **Step 4** (finalize combine + HTTP
status, S3 deferred) into the repo *with* a correctness fix to the audio
timeline. Written and maintained per the "Goal-driven execution" principle:
each phase has a success check and a recorded result.

Scope boundary: this is recorder build-order **step 1** in `CLAUDE.md`
(recorder standalone, no gateway). S3 upload is explicitly out of scope.

---

## The defect we are fixing (not a "limitation")

The Step-4 code presented by an earlier session feeds `user.pcm` and
`agent.pcm` into ffmpeg as two continuous streams **aligned at sample 0**. But
the gateway only tees agent audio *while the agent speaks* — the silent gaps
between utterances are absent from `agent.pcm`. So `agent.pcm` sample 0 is the
agent's first word, while `user.pcm` sample 0 is the start of the call. The two
files share no timeline → cumulative drift that grows for the whole call. In a
multi-minute KYC consent recording the agent's question lands tens of seconds
out of place against the user's answer and the video. `-itsoffset` does **not**
fix this — it corrects one fixed start offset, not drift accumulating during the
call. For a regulated consent recording where Q/A alignment is the evidence,
this is broken output, not an acceptable limitation.

---

## Findings (investigation, before any code)

1. **Root cause is the *contract*, deeper than `finalize.go`.** The proto and
   `CONTRACTS.md` §3 define the timestamp as **per-stream-relative**:
   `proto/recorder.proto:11` — "monotonic microseconds since *this stream's*
   first frame". Under that definition, `user.pcm` t=0 and `agent.pcm` t=0 are
   *different real moments*, each reset to zero at its own first frame. So
   silence-padding cannot align the streams: per-stream timestamps carry **zero
   cross-stream timing information**. The fix therefore requires a contract
   change, not just a recorder change.
2. **Step 4 was never applied to this repo.** Disk held step-2/3 state only:
   `session.go` = old raw-append (no meta, no offset), `videowriter.go` = no
   first-frame capture, `cmd/recorder/main.go` = gRPC only, and **no
   `finalize.go`/`httpapi.go`**. The five files were *presented*, not dropped in
   (and used module path `kyc/proto`; this repo is `kyc-monorepo/proto`). So
   "fix before we commit" is clean — we bring Step 4 in already corrected and
   never commit the buggy version.
3. **Not a git repo** — needed `git init` for the "commit each step" workflow.
4. **Tooling present** (user's machine): `protoc` 35.0 + `protoc-gen-go`
   v1.36.11 + `protoc-gen-go-grpc` v1.6.2 (regen feasible, minimal diff),
   `go build ./...` succeeds offline, `ffmpeg`/`ffprobe` available.
5. `test.h264` is an input fixture for `cmd/videotest` (keep); `out.mp4` is its
   generated output (gitignore).

---

## Decisions (forks confirmed with the user)

- **Fork 1 — express the shared clock as a NEW field**, not by redefining
  `ts_us`. Add `uint64 call_us = 5` to the proto: monotonic microseconds since a
  single per-call origin, identical across `VIDEO_AU` / `USER_PCM` / `AGENT_PCM`.
  `ts_us` stays per-stream (still the video writer's internal pacing clock).
  Requires regenerating `recorder.pb.go`.
- **Fork 2 — no first-frame anchor state. Position by absolute `call_us`.**
  Every PCM frame is written at byte offset `call_us * 48000 / 1_000_000 * 2`.
  Sample 0 of both files is `call_us == 0` *by definition*; the later-starting
  stream gets leading silence for free (its first `call_us` is larger). The
  recorder holds no first-frame/origin state and infers nothing.
  - **Video uses the same rule.** `-itsoffset` is derived directly from the
    first kept keyframe's `call_us` (persisted to `meta.json`). This *deletes*
    the `audioFirstWall`/`firstFrameWall` wall-clock comparison entirely —
    one origin, one offset mechanism — and makes the offset **exact**
    (capture-time), retiring the old "receive-time is approximate" limitation.
  - **Jitter/reorder:** if a frame's `call_us` maps to a byte offset ≤ bytes
    already written, clamp to a no-op pad — append in place, **never seek
    backward, never truncate**. (Single ordered gRPC stream makes this rare;
    purely defensive.)
- **Fork 3 — `git init` baseline, then commit per phase.**
- **Untouched, brought in as presented (module path fixed only):**
  `finalize.go` combine logic, `-c:v copy`, the `amerge + pan FL/FR` user→L /
  agent→R mapping, the four status branches (complete/partial/audio_only/failed),
  and the audio-only fallback. Only the *offset source* changes (call_us, via
  `videoOffsetSeconds` + `meta.json`).

### Byte math (positioning rule)

```
offset_bytes(call_us) = call_us * 48000 / 1_000_000 * 2   // integer; snaps to whole s16le samples, always even
on each PCM frame:
    pad = offset_bytes(call_us) - bytes_written_to_this_stream
    if pad > 0: write pad zero bytes (silence); bytes_written += pad
    write frame data; bytes_written += len(data)
    // pad <= 0  → reorder/jitter: append in place, never seek back
```

Absolute positioning (offset computed from `call_us` each frame, not summed per
gap) ⇒ rounding never accumulates ⇒ no drift.

---

## Phase log

| # | Phase | Success check | Status |
|---|-------|---------------|--------|
| 0 | git baseline (steps 1-3) | repo inits, baseline commit clean | ✅ done |
| 1 | four principles in CLAUDE.md + this worklog | section added, committed | ✅ done |
| 2 | contract: add `call_us`, regen pb.go, update CONTRACTS §3 | `go build ./...` clean, diff = new field only | ✅ done |
| 3 | apply Step 4 w/ fix (finalize, httpapi, session, videowriter, main) | `go build` + `go vet` clean | ✅ done |
| 4 | targeted alignment test | gapped agent burst lands at correct offset | ✅ done |

### Phase 0 — git baseline
- `git init` (branch `master`), `user.email=pawan@unleashx.ai`.
- `.gitignore` += `out.mp4`, `.DS_Store` (hygiene); kept `test.h264` fixture.
- Baseline commit `83ed91c`: 47 files, the pristine pre-Step-4 "before" snapshot.
- Note: committing on `master` (greenfield bootstrap, linear "build order"
  history). Say the word if you'd prefer a feature branch.

### Phase 1 — principles + worklog
- Added `## How to work here (four principles — always)` to `CLAUDE.md`
  (think-before-coding / simplicity / surgical / goal-driven), cross-referencing
  the existing anti-overengineering rules instead of duplicating them.
- Created this `WORKLOG.md`.

### Phase 2 — contract: `call_us`
- `proto/recorder.proto`: added `uint64 call_us = 5` (shared per-call origin);
  `ts_us` left verbatim (Fork 1: keep it per-stream).
- Regenerated with the documented command
  (`protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. ...`).
  Diff was minimal and expected: only `recorder.pb.go` (+`CallUs` field, getter,
  descriptor); `recorder_grpc.pb.go` unchanged (no service change).
- `CONTRACTS.md` §3: added the field + a "Timestamps (load-bearing)" block
  pinning `ts_us` per-stream vs `call_us` shared, the silence-fill rule, and the
  explicit guarantee that the gateway need NOT send continuous audio.
- Result: `go build ./...` exit 0, `go vet` exit 0.

### Phase 3 — apply Step 4 with the fix baked in
- **`session.go`** (the fix): PCM frames positioned by `call_us` via
  `appendPCM` (offset `call_us*48000/1e6*2`, gap→silence, clamp-no-pad on
  reorder); tracks `userBytes`/`agentBytes` cursors; no first-frame/origin state.
  `writeMeta` persists `video_first_call_us`.
- **`videowriter.go`**: threaded `callUS` through `send`/`auFrame`/`handle`;
  captures `firstCallUS` at the first kept keyframe (read after the mux goroutine
  ends). No wall-clock fields — the offset is now exact capture-time.
- **`finalize.go`** (new): combine/`buildFFmpegArgs` brought in verbatim (stream-
  copy video, `amerge+pan` user→L/agent→R, four status branches, audio-only
  fallback). Only `recordingMeta`/`videoOffsetSeconds` changed — offset now reads
  `video_first_call_us` (audio is at call_us 0, so offset = video's first
  keyframe time).
- **`httpapi.go`** (new): `POST /finalize` (202, async) + `GET /status`, verbatim.
- **`main.go`**: starts HTTP (:9090) alongside gRPC (:9091).
- **`server.go`** / **`testhooks.go`**: pass `frame.CallUs`; test hook keeps its
  2-arg signature (passes tsUS as callUS) so `cmd/videotest` is untouched.
- Result: `go build ./...` exit 0, `go vet ./...` exit 0. My edited lines + all
  new files are gofmt-clean. Pre-existing gofmt issues (import order / struct
  packing / missing EOF newlines) in `server.go`, `testhooks.go`,
  `videowriter.go`, `cmd/feedtest`, `cmd/videotest` were left untouched per
  Surgical Changes — they predate this work (baseline already flags them).

### Phase 4 — targeted alignment test
- `internal/recorder/session_test.go` :: `TestAudioTimelineCallUsAlignment`.
  Feeds continuous user mic + a gapped agent track (burst 0–200 ms, **1 s of
  nothing sent**, burst 1200–1400 ms) straight through `session.write`, closes,
  and asserts on `agent.pcm`:
  - length == `pcmByteOffset(1400ms)` = 134 400 B (raw-append would be 38 400 B,
    so this alone catches the bug),
  - three exact regions: burst1 marker | **silence gap** | burst2 marker starting
    precisely at `pcmByteOffset(1200ms)` = 115 200 B (not slid earlier).
  - Then drives the **real `combine`** (→ `audio_only`, no video) and `ffprobe`
    asserts the output is 2-channel stereo (agent → right).
- Why it proves the fix: agent → right channel is the already-tested combine
  mapping, so the asserted `agent.pcm` placement *is* the right-channel
  placement; and the length/region checks fail under the old raw-append.
- ffmpeg `silencedetect` on the RIGHT channel (logged by the test) confirms it
  end-to-end:
  ```
  silence_start: 0.200146
  silence_end:   1.199771 | silence_duration: 0.999625
  ```
  → the right channel is silent across the gap and burst 2 resumes at 1.20 s,
  its true `call_us`. Drift eliminated.
- Result: `go test ./...` green; `go build`/`go vet` clean.

## Outcome

The audio-timeline defect is fixed at the contract layer (`call_us`) and the
recorder layer (silence-positioned PCM + shared video offset), proven by a
deterministic test plus an ffmpeg end-to-end check. All four finalize status
branches, the stereo L/R mapping, `-c:v copy`, and the audio-only fallback are
unchanged. Commits: baseline → principles+worklog → contract → Step 4+fix →
test, one per phase.

---

# Brain — Step 2 (text-only control plane)

Build-order step 2: a thin FastAPI text API over the already-copied logic
(state machine, turn service, phrase builder, i18n, intent client). The brain
owns session state + the consent flow and never touches media.

## Findings
- Most logic was already copied into `brain/app/`. What's missing/new: `main.py`,
  `routes/sessions.py`, `services/tts_plan.py`, the transcript writer, packaging.
- The copied `session_manager`/`turn_service`/`tts_reference` didn't import clean:
  they referenced missing `app.services.metrics`, a Redis `session_store`,
  `webrtc_video`, and `utils/audio`. `tts_reference.py` was the old
  ElevenLabs `pipeline/tts.py` (HTTP/audio) — the spec says delete it.
- `language_processor` degrades gracefully with no `OPENAI_API_KEY` (English
  pass-through), so `/start` is curl-testable with zero external services.
- Only `english` + `hindi` i18n packs are registered; other languages raise
  `NotImplementedError` (test with English).

## Decisions (confirmed)
- **session_manager → in-memory only** (Option 1): dropped the Redis store,
  claim/persist/snapshot/rehydrate, and every media field. The text-only
  `Session` keeps ids/language/customer/phrases/flow/turn_index/events/
  transcript/recording_status/pending_agent_text/timestamps.
- **Use the old-repo zip** (`~/Downloads/videokyc-wsfu-dev.zip`): port
  `storage/recap.py` (transcript half only — drop the WAV merge) and the
  verbatim tests (`test_state_machine.py`, `test_intent.py`).
- Proceeding by default: a no-op `metrics.py` shim (keeps the verbatim
  turn_service/session_manager imports), delete `tts_reference.py`, and
  best-effort gateway `/close` + recorder `/status` calls (brain runs standalone).

## Phase log
| # | Phase | Check | Status |
|---|-------|-------|--------|
| B1 | trims: config, schemas, session_manager, metrics shim, rm tts_reference, reqs | all modules import clean | ✅ done |
| B2 | `services/tts_plan.py` (split + resolve helpers) | plan matches CONTRACTS §4 | ✅ done |
| B3 | `storage/` + recap port + `routes/sessions.py` + `main.py` | app imports, TestClient | ✅ done |
| B4 | verbatim tests (zip) + new API smoke test | pytest green | ⏳ |

### Phase B1 — trims + scaffolding
- `config.py`: slimmed to the control-flow/i18n/intent/storage/echo set (29 keys,
  was 40+); dropped redis/webrtc/mux/audio-rate/loop-monitor; added
  `gateway_url`/`recorder_url`.
- `schemas/session.py`: `StartSessionRequest` kept verbatim; response models
  rewritten to CONTRACTS §1 (`tts_plan`/`voice_id`/`model_id`/`gateway_offer_url`;
  no `agent_audio_*`).
- `session_manager.py`: in-memory rewrite per Option 1.
- `metrics.py`: no-op shim. `tts_reference.py`: deleted. `requirements.txt`: added.
- Verified: every kept/trimmed module imports clean (`openai` absent → graceful).

### Phase B2 — tts_plan
- `services/tts_plan.py`: `build_tts_plan(agent_text)` ports the old
  `synthesize_stream` split (`re.split(r"(<var>.*?</var>)")` + `_spoken_len` /
  `_speed_for` / `_slow_down_var_text`) to emit segments instead of calling
  ElevenLabs. `<var>` → silence/slow-speech/silence; else normal-pace speech.
  Plus `resolve_voice_id`/`resolve_model_id` (pack → env → default).
- Verified the plan byte-for-byte against the CONTRACTS §4 example, the empty /
  no-var cases, and the resolvers (english→default voice, hindi→pack voice).

### Phase B3 — storage + routes + app entry
- `storage/`: `base.py` (a one-method `StorageBackend` Protocol), `local.py`
  (writes under `recordings_dir`, returns `/recordings/{sid}/{file}`), `s3.py`
  (ported verbatim — local-copy-first, boto3 chain, falls back to a local URL on
  missing creds/upload error), `__init__.py` factory picking the backend from
  `AWS_STORAGE` at import. Note: `s3.py` only uploads `.mp4`; the brain writes
  only `.txt`/`.json`, so transcripts always resolve to local URLs (faithful to
  the old repo — `aws_url` fronts the same dir in prod). The MP4-upload path is
  unused here but kept as the spec's "S3 with local-dir fallback the code has".
- `storage/recap.py`: ported the **transcript half only** of the old recap —
  `_format_transcript_txt` / `_format_transcript_json` + `finalize_call`. The
  WAV-merge half is dropped (recorder owns audio, CONTRACTS §3).
- `routes/sessions.py`: the four spec session routes — `POST /start`,
  `GET /{id}`, `POST /{id}/turn`, `POST /{id}/end` (prefix `/api/v1/sessions`) +
  `GET /health` in `main.py`. `/turn` is transcript-in → `classify_safely_async`
  → `finalize_turn` → text + `tts_plan` out. `/end` forces a terminal state,
  writes transcripts (idempotent via `mark_session_ended`), best-effort
  `gateway /close`, returns `recording_status: pending`. GET proxies
  `recorder /status` post-end. Downstream gateway/recorder calls are best-effort
  (2 s timeout) so the brain answers standalone.
- `main.py`: FastAPI app + permissive CORS + a `/recordings` static mount (so
  transcript URLs are GETtable) + the sessions router.
- Verified (TestClient): app imports clean, `/health` → 200, all routes mounted;
  full `/start → /turn → /end` smoke returns the CONTRACTS §1 shapes and writes
  `transcript.txt` + `.json`. With no intent service up, `/turn` folds to
  `please_repeat` (hard-invariant #2: degrade, never break) — the expected path.

---

# Gateway — Step 3 (media plane)

Build-order step 3: port the old `app/services/webrtc_video.py` (aiortc → Pion)
into `cmd/gateway` + `internal/gateway/`. Terminates the one send-only WebRTC
peer per call, runs Sarvam STT / ElevenLabs TTS, drives the turn loop
(interpretation A), tees media to the recorder. Not an SFU.

## Milestone plan (each independently committable + verifiable)

| # | Milestone | Verify |
|---|-----------|--------|
| G0 | scaffold + the one-origin call clock | `go build` clean; `time.Now()` grep == 1 |
| G1 | `recordclient` (gRPC RecordStream + /finalize) | canned frames → real recorder → call-aligned output |
| G2 | `peer` (Pion, OnTrack video/audio, depacketize, drop-until-keyframe, Opus decode); **SPS/PPS caching fix** | canned SDP + synthetic RTP → keyframe-led AU stream finalizes playable |
| G3 | `stt` (Sarvam WS) + resampler | offline canned-frame request/parse |
| G4 | `tts` (ElevenLabs) + **AgentSink seam** | canned plan → byte-exact PCM |
| G5 | `control` WS (JSON in, binary audio out) | scripted WS exchange |
| G6 | `brainclient` | against running brain |
| G7 | `turnloop` + idempotent teardown | full loop; folds to please_repeat on STT/intent failure |
| G8 | e2e: retarget `web/index.html`, live keys | real browser call, Q/A lip-sync correct |

Carry-overs from earlier sessions, mapped: **call_us honesty** → G0 (structural,
below); **SPS/PPS caching** → G2 (depacketizer caches param sets, guarantees first
forwarded keyframe AU carries them inline — unblocks the recorder's wait-forever);
**AgentSink seam** → G4 (the one interface in the gateway: WS-audio impl now, the
future Opus-track swap is a second impl).

Decisions confirmed with the user: WS lib = `gorilla/websocket` (both the
/control server and the Sarvam client). Vendor clients (STT/TTS) unit-tested
offline with canned bytes — live keys only at G8 e2e; build phases never gated on
credentials or network.

## Phase log
| # | Phase | Check | Status |
|---|-------|-------|--------|
| G0 | scaffold (config, session/clock, main) + one-origin gate | `go build`/`vet`/`gofmt` clean, grep gate == 1 | ✅ done |
| G1 | `recordclient` (gRPC tee + Finalize) | canned frames → real recorder → call-aligned output | ✅ done |
| G2a | `peer` H.264 depacketizer + SPS/PPS cache | synthetic-RTP tests: drop-until-keyframe, inline param sets, FU-A, teardown flush | ✅ done |
| G2b | Pion peer (offer/answer, ICE restart) + OnTrack video/audio → recordclient | wired into offerHandler; SDP/OnTrack validated at G8 e2e | ⏳ |

### Phase G0 — scaffold + the one-origin call clock
- **The clock is made structural, not conventional.** `internal/gateway/session/`
  holds `Session{ID, start}` with `CallUS()` = µs since `start`. `start` is set
  exactly once, in `newSession` — the **only** `time.Now()` in all of
  `internal/gateway` (grep-verifiable). Every VIDEO_AU/USER_PCM/AGENT_PCM frame
  in G2+ stamps `s.CallUS()`; "one origin, all three streams" (CONTRACTS §3) is
  thus a property the diff's grep catches, not a discipline to remember. Two
  origins → silent drift; the gate is the first thing reviewed in every gateway diff.
- `session.Registry` (mutex map) = live sessions; `Create` stamps the clock,
  `Remove` is the idempotent teardown handle.
- `internal/gateway/config/`: the exact env set from GATEWAY.md, localhost
  defaults, vendor keys default empty.
- `cmd/gateway/main.go`: HTTP surface (CONTRACTS §2) — `offer` creates the session
  + stamps the clock (501 until the G2 peer), `control` (501 until the G5 WS),
  `close` real + idempotent (200). Go 1.22 method-routed ServeMux.
- Added `session/` to GATEWAY.md's package layout (it isn't in the original list;
  it's the shared per-call hub `peer`/`turnloop`/sinks attach to — can't live in
  `package main` without an import cycle. Name `session/` per user, matches domain).
- Verified: `go build ./...`, `go vet`, `gofmt -l` all clean; one-origin grep == 1;
  runtime smoke — boots, offer→501 (logs `call_us=0`), control→501, close→200
  (idempotent), `GET offer`→405 (method routing). go.mod untouched (G0 is
  stdlib-only; Pion/websocket/opus resolve via proxy, enter as later phases import).

### Phase G1 — recordclient (the recorder tee)
- `internal/gateway/recordclient/`: owns the per-call gRPC `RecordStream` +
  `POST /finalize`. **call_us has exactly one source:** `Send(kind, data)` stamps
  `call_us = sess.CallUS()` internally — callers pass no timestamp, so they can't
  pass a wrong one. `ts_us` is *derived from the same clock* (`call_us` − this
  stream's first-frame `call_us`), adding no second time source; the one-origin
  grep stays == 1, and the only real `CallUS()` call in non-test code is this
  stamping site.
- **Never block media** (invariant 4 / GATEWAY.md): a buffered channel (512) + one
  sender goroutine — the recorder's own `videoWriter` pattern. `Send` enqueues
  non-blocking and drops on a full queue; a broken recorder stream sets a `broken`
  flag and silently drains (degrades the recording, never stalls the call). `Close`
  drains, half-closes (recorder sees `io.EOF` → flushes files), logs the Ack count.
  The dial is lazy, so a recorder that's down doesn't fail call setup. **[Corrected
  later:** as first written, `New` *opened the stream* eagerly, so a down recorder
  actually DID fail the call — an invariant-4 bug fixed in commit "recordclient:
  open recorder stream lazily" (stream-open moved into the sender goroutine), with
  a `TestRecorderDownDegradesNotFails` regression guard.**]**
- **No new module deps** — `grpc` was already in go.mod (the recorder uses it).
- Tests (`recordclient_test.go`), all offline against the **real recorder service**
  on a localhost gRPC listener — real code, real gRPC, no external/live recorder,
  no keys, no ffmpeg:
  - `TestSendStreamsCallAlignedToRecorder` — a gapped agent track (burst 0–200 ms,
    1 s silent, burst 1200–1400 ms) streamed through the gRPC path is reconstructed
    by the recorder call-aligned: burst 2 at exactly `115200`, total `134400`, three
    regions (burst1 | silence | burst2). The exact offsets from recorder's
    `TestAudioTimelineCallUsAlignment` — proves `call_us` survives the gateway→recorder
    hop intact.
  - `TestSendStampsCallUsFromClock` — after a 300 ms real delay, a single `Send`
    lands at the byte offset the clock reported (not 0) → `call_us` is sourced from
    `CallUS()`, not arrival order.
  - `TestFinalizePostsToRecorder` — `Finalize` issues `POST /sessions/{id}/finalize`
    and treats 202 as success (httptest stub; no ffmpeg).
- Verified: `go build`/`vet`/`gofmt` clean, full repo `go test ./...` green (recorder
  suite untouched), one-origin grep still == 1.

### Phase G2a — H.264 depacketizer + SPS/PPS cache (peer, part 1)
- `internal/gateway/peer/h264.go`: `h264Assembler` turns a video track's RTP
  packets into Annex-B access units for `recordclient.Send(VIDEO_AU)`. Pion's
  `codecs.H264Packet` does STAP-A/FU-A reassembly → NALs; the assembler groups
  NALs into one AU per RTP timestamp, drops until the first keyframe, and caches
  SPS/PPS.
- **SPS/PPS fix (the carry-over):** every forwarded keyframe AU is guaranteed to
  carry parameter sets inline. Prepend only the *missing* cached sets (no duplicate
  SPS in the recorder's avcC); a keyframe with no sets seen anywhere is held back
  until a complete one arrives. This retires the recorder's "first keyframe lacks
  SPS/PPS; waiting forever" stall (`videowriter.go:130`) on the gateway side.
- **AU boundary = RTP timestamp change + `flush()`**, not the marker bit (robust to
  missing markers; one-frame latency is irrelevant to a recorder tee — the mux
  already does one-sample lookahead). **Teardown ownership (wired into G2b):** the
  `OnTrack(video)` goroutine owns the assembler and calls `flush()` when its
  `ReadRTP` loop exits (track end / peer close), forwarding the final buffered AU;
  no other code touches the assembler. `flush()` is idempotent.
- Reused `mp4ff/avc` (already a dep) for NAL parsing, so gateway and recorder agree
  byte-for-byte on "what is an SPS / a keyframe."
- New deps: `github.com/pion/rtp v1.10.2` (+ `pion/randutil` indirect). go.sum also
  carries pion/rtp's test-only chain (testify) for graph completeness — not in our
  `require` set.
- Tests (`h264_test.go`), all offline synthetic RTP, no browser: inline SPS+PPS+IDR
  passes through unchanged; a bare keyframe gets SPS/PPS from cache; pre-keyframe
  slice dropped, P-frames pass after; FU-A-fragmented IDR reassembled (exact bytes
  asserted); keyframe with no sets held back then recovers; **final AU flushed at
  teardown** (proves the buffered tail frame isn't dropped on stream close, and
  flush is idempotent against a double /close).
- Verified: `go build`/`vet`/`gofmt` clean, full repo `go test ./...` green,
  one-origin grep still == 1 (peer adds no `time.Now`).

