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
| G2b | Pion peer (offer/answer, ICE restart) + OnTrack video/audio → recordclient | wired into offerHandler; build + boot smoke; SDP/OnTrack at G8 e2e | ✅ done |
| G3 | `stt` (Sarvam WS) + `audio` resampler | offline WS-server tests: framing fidelity, vendor error, wall-clock timeout, drop-oldest, sample-exact resample | ✅ done |
| G4 | `tts` (ElevenLabs) + AgentSink seam + upsampler | stub-HTTP tests: plan exec, per-chunk call_us, silence bytes, normal/slow payload, sample-exact upsample | ✅ done |
| G5 | `control` /control WS + real AgentSink (SendAt) | WS-client tests: text/binary discipline, sink call_us passthrough, tee-survives-WS-drop | ✅ done |
| G6 | `brainclient` (GET snapshot, POST turn) | round-trips canned CONTRACTS §1 responses field-for-field; failures → error | ✅ done |
| G7a | live audio wiring: peer `SetMic` gate + 48k→16k downsample in `readAudio` | gate + concurrency (`-race`) tests; full path at G8 | ✅ done |
| G7b | `turnloop` driver (greeting → loop → degradation) + wiring | integration tests (`-race`): both degradation paths; build/boot smoke | ✅ done |
| G7c | teardown integration (`call.Close` ordering) + checklist close | mid-utterance unwind test (`-race`); full Close at G8 e2e | ✅ done |

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

### Phase G2b — Pion peer + OnTrack tees (peer, part 2)
- `internal/gateway/peer/peer.go`: `Peer` wraps a Pion `PeerConnection`. `New`
  builds it from the browser's send-only offer, registers OnTrack, and returns the
  answer SDP (non-trickle — blocks on `GatheringCompletePromise` so the answer
  carries all candidates; single POST round-trip per CONTRACTS §2). `Reoffer`
  re-runs the answer flow on the same PC for ICE restart. No outbound track, no
  Opus encoder (send-only peer).
  - `OnTrack(video)` → `h264Assembler` (G2a) → `recordclient.Send(VIDEO_AU)`; the
    read goroutine owns the assembler and `flush()`es the tail when `ReadRTP`
    returns (its sole flush site, per G2a's teardown test). No decode.
  - `OnTrack(audio)` → `pion/opus` decode (defaults to 48 kHz mono — exactly
    USER_PCM) → `recordclient.Send(USER_PCM)`. Bad packet → drop one frame. The
    48k→16k STT feed is G3.
  - `Close` calls `pc.Close()` then `wg.Wait()`s the read goroutines, so their final
    Sends (incl. the flushed video tail) are enqueued before the recordclient
    stream closes — the teardown ordering recordclient.Close documents.
- **Teardown wired both ways.** `OnConnectionStateChange` invokes a once-guarded
  `onClose` on **Failed/Closed only** (Disconnected can recover — no timers/grace,
  G7 refines). `onClose` = `func(){ go r.End(id) }` (fresh goroutine so it can't
  re-enter `Call.Close` on the callback goroutine). So both `POST /close` and an
  unexpected disconnect tear the call down via the same `Registry.End` (Get→Close→
  Remove), idempotent through `Call`'s `sync.Once`.
- **Import-cycle resolution.** Keeping `recordclient` a clean leaf (it imports
  `session` for the clock) means `session` can't import `peer` (cycle via
  recordclient). So `session` stayed the pure clock (its G0 `Registry` removed —
  nothing else used it), and a new `internal/gateway/call/` package became the
  per-call hub: `Call` owns `{session, peer, recordclient}` + a `sync.Once`
  teardown; `Registry` maps id→`*Call`. DAG: `session ← recordclient ← peer ← call
  ← main`, no cycle, **G1's recordclient signature unchanged.**
- `cmd/gateway/main.go`: `offerHandler` now real — first offer → `call.Registry.Start`
  (session clock → recorder tee → peer → answer); a re-offer for a known id →
  `Call.Reoffer` (ICE restart, recording not split). `closeHandler` → `Registry.End`.
  `/control` still a G5 stub.
- New deps (direct): `pion/webrtc/v4`, `pion/opus` (+ the Pion WebRTC stack as
  indirect: dtls/ice/sctp/srtp/stun/turn/sdp/interceptor/…).
- Verified: `go build`/`vet`/`gofmt` clean; full repo `go test ./...` green
  (G2a + G1 suites still pass after the session→`call` move); one-origin grep == 1;
  boot smoke — boots, empty offer→400, garbage SDP→500 (SDP parse), down recorder
  degrades not fails, `/control`→501, `/close` unknown→200, terminal-state callback
  fires cleanly. SDP/OnTrack media path validated at G8 e2e (no Pion-loopback test,
  per the agreed scope).

### Phase G3 — stt (Sarvam WS) + audio resampler
- `internal/gateway/stt/`: port of `app/pipeline/stt.py:transcribe_stream`
  (CONTRACTS §5). `Client.Run` streams 16 kHz mono s16le over the Sarvam WS and
  calls `emit` with vad events then one terminal final/error. `sarvam_lang.go` is
  the bcp47 map ported **verbatim** (full 12-lang map + default en-IN).
- **Fidelity (side-by-side with stt.py), asserted by an offline gorilla WS test
  server:** exact query string (params + order via manual build, not `url.Values`
  which sorts; `saarika:v2.5`→`saarika%3Av2.5`); `Api-Subscription-Key` header;
  base64 `{"audio":{…}}` framing; `{"type":"flush"}` on end-of-audio; `data`→final
  (trimmed), `events`→vad, `error`→error, binary/non-JSON ignored.
- **15 s timeout = wall-clock from BEFORE connect, never idle-reset** (stt.py sets
  `t0` before `websockets.connect`). Implemented as ONE `context.WithTimeout`
  covering connect+stream; a vad event does not reset it. `TestRunTimeoutIsWallClock`
  proves it fires ~500 ms (the budget) not ~800 ms (vad-time + budget). Notably this
  uses `context.WithTimeout`, not `time.Now()` — the one-origin grep stays == 1.
- **Drop-oldest mic buffer** (`MicBuffer`, ~400×20 ms ≈ 8 s): `Push` is fully
  non-blocking (single producer = the audio reader), drops the oldest under
  backpressure, counts drops in an atomic, logged **once per turn** via `Run`'s
  defer (not per frame) — same stance as the recorder tee.
- `internal/gateway/audio/`: the resampler. `Downsampler.Down48to16` decimates
  48k→16k by keeping every 3rd sample, **stateful** so the phase carries across
  chunk boundaries. `TestDown48to16PhaseAcrossChunks` asserts sample-exact output
  over a 4-then-5-sample boundary (not a multiple of 3); a per-chunk reset is caught.
- **Deviations from stt.py (intentional, called out):** REST `transcribe()` not
  ported (gateway needs only streaming); `certifi` TLS context dropped (a
  macOS-Python quirk; Go uses system roots); `metrics.*` calls omitted (no metrics
  infra; the drop log is the only observability); bcp47 map kept full per "copy
  verbatim".
- **Not yet wired (G7):** the live path `OnTrack(audio)` → resample → `MicBuffer`
  → `stt.Run`, gated per turn, is the turn loop's job; `peer.readAudio` still only
  tees USER_PCM. The `audio`+`stt` packages are built and tested standalone.
- New dep (direct): `github.com/gorilla/websocket` (first use; also the `/control`
  WS in G5).
- Verified: `go build`/`vet`/`gofmt` clean; full `go test ./...` green; one-origin
  grep == 1.

### Phase G4 — tts (ElevenLabs) + AgentSink seam + upsampler
- `internal/gateway/tts/`: executes a brain-built `tts_plan` (CONTRACTS §4) into
  agent audio. `elevenlabs.go` ports `tts.py:_stream_one` (HTTP stream → 24k PCM);
  `tts.go` holds the `AgentSink` seam, `PlanItem`, and `Speak` (the executor). No
  markup/`<var>` splitting, no voice/model resolution — those are the brain's job;
  the gateway gets the resolved plan + `voiceID`/`modelID`.
- **Stamping rule (pinned with the user):** `Speak` samples `clock()` ONCE per
  chunk and passes that `call_us` into `WriteAgentAudio(pcm48, callUS)`; the sink
  must not sample the clock, so the recorder tee and the browser send carry the
  identical timestamp for the same bytes. Test asserts `call_us` = 1000,2000,3000…
  (one sample per sink call).
- **Silence bypasses the upsampler:** zeros upsample to zeros, so the executor
  emits `48000·ms/1000·2` zero bytes directly at 48k (no HTTP, no interpolation);
  test asserts a 200 ms item → exactly 19200 zero bytes.
- **AgentSink seam (the third carry-over):** one method
  `WriteAgentAudio(pcm48, callUS) error` — the only interface in the gateway. Doc
  pins today's impl (control-WS binary frame + `AGENT_PCM` tee) and the planned
  A→B swap (outbound Opus track) as a second impl. The real impl + `recordclient.
  SendAt` land with the control WS in **G5** (no dead code now).
- **Fidelity (stub-HTTP tests):** exact path `/{voice}/stream?output_format=pcm_24000`,
  `xi-api-key` + JSON headers, normal payload carries `speed` & `stability:0.5`,
  slow payload OMITS `speed` (`Speed *float64` + `omitempty`) & uses `stability:0.7`,
  non-200 → error.
- `internal/gateway/audio/upsampler.go`: stateful ×2 linear-interp `Upsampler`
  (`a, mid(a,b)`; `Flush` duplicates the final held sample → exactly 2×). Sample-
  exact test splits at an odd (1-sample) boundary and asserts byte-identical output
  to the whole segment.
- **Deviations from tts.py (intentional):** markup splitting + voice/model
  resolution are the brain's; `metrics.*` omitted; pooled `httpx` → `http.DefaultClient`;
  **no `http.Client.Timeout`** (it bounds the whole request and would truncate
  streaming audio) — the request is bounded by the `ctx` the turn loop passes (G7).
- No new deps (stdlib `net/http`). Not wired live until G7 (the turn loop calls
  `Speak`). Verified: build/vet/gofmt clean; full `go test ./...` green (incl.
  `-race`); one-origin grep == 1.

### Phase G5 — /control WS + the first real AgentSink
- `internal/gateway/control/`: the browser ⇄ gateway control socket (CONTRACTS §2).
  `Conn` wraps one WebSocket carrying two frame kinds — TEXT = JSON control, BINARY
  = agent audio out. A single reader goroutine dispatches inbound by type (text →
  `Inbox`; binary inbound ignored) and closes `Inbox` on the socket drop. Outbound
  `SendVAD/SendFinal/SendAgentDone/SendError` (text) and `SendAudio` (binary) are
  serialized by a **`writeMu`** — gorilla permits only one concurrent writer, and
  the turn loop's events race the sink's audio otherwise.
- **First real `AgentSink` (`control.Sink`):** `WriteAgentAudio(pcm48, callUS)` tees
  to the recorder THEN writes the browser frame. **Tee-first** so the recording —
  the evidence — survives a mid-utterance WS drop; the browser write then errors and
  the executor ends the turn. call_us is used as-is (no clock sampling), wired in
  `call.AttachControl` to `rec.SendAt(AGENT_PCM, callUS, pcm)`.
- **`recordclient.SendAt(kind, callUS, data)`:** the explicit-call_us path for the
  agent tee; `Send` is now sugar over it (video/user sample the clock internally).
  One-origin grep stays == 1. Test positions an AGENT_PCM frame at
  `pcmByteOffset(500ms)` from an explicit call_us.
- `cmd/gateway/main.go`: `controlHandler` → 404 if `/control` precedes `/offer`,
  else upgrade + `AttachControl`. `Call.Close` closes the socket first.
- Tests (offline, real gorilla WS client): frame discipline (start_turn in → Inbox;
  vad/agent_done/error out as text; audio out as binary, byte-exact); sink call_us
  passthrough; tee-survives-WS-drop (tee happens, `SendAudio` errors). Run with `-race`.
- **Not wired until G7:** the turn loop drives `conn.Inbox()` + the sink (greeting →
  turns → agent_done). A G5 browser attaches but gets no turn response; `Inbox`
  buffers (8) until G7 drains it.
- No new deps (gorilla already present). Verified: build/vet/gofmt clean; full
  `go test ./...` green (incl. `-race`); one-origin grep == 1; boot smoke
  (control-before-offer → 404).

### Phase G6 — brainclient (control-plane HTTP)
- `internal/gateway/brainclient/`: thin client for the brain (CONTRACTS §1).
  `Get(ctx,id)` → `GET /api/v1/sessions/{id}` (bootstrap snapshot); `Turn(ctx,id,
  transcript)` → `POST /api/v1/sessions/{id}/turn`. Paths match the brain's actual
  router (`prefix=/api/v1/sessions`).
- `Snapshot`/`TurnResponse` mirror the brain's Pydantic models field-for-field;
  `tts_plan` decodes into `[]tts.PlanItem` (one source of truth, handed straight to
  `tts.Speak`). Doc note: the brain's `TurnResponse` carries `session_id` though
  CONTRACTS §1's *example* omits it — we mirror the brain.
- **No retries, no circuit breakers** (hard-invariant 2): unreachable / non-200 /
  malformed-JSON each return an error; the turn loop folds it to please_repeat.
  `ctx` is passed through (the turn loop sets the deadline — G7 checklist item 1).
- Tests: round-trip the CONTRACTS §1 turn + snapshot examples (with the §4 tts_plan
  inlined) verbatim — every plan item, voice/model, events, status asserted — plus
  the request shape (`POST …/turn` with `{"transcript":…}`), and all three failure
  modes → error. `-race`.
- No new deps. Verified: build/vet/gofmt clean; full `go test ./...` green; one-origin
  grep == 1.

### Phase G7a — live audio wiring (peer mic gate)
- `peer.go`: a mutex-guarded settable mic handler (`SetMic`/`feedMic`). `readAudio`
  now Opus-decodes → tees USER_PCM → downsamples 48k→16k (`audio.Downsampler`) →
  `feedMic`, which delivers to the handler ONLY while one is set (the
  listening-window gate). The turn loop (G7b) sets it to the turn's `MicBuffer.Push`
  at start_turn, clears it at END_SPEECH; idle audio between turns is discarded.
- One Downsampler per track (phase continuous across turns — correctness over the
  micro-optimization of skipping decimation when idle, since the user's first word
  lives at a turn start).
- Tests (`mic_test.go`): gate (frames outside the window discarded) + `SetMic`
  racing `feedMic` 1000× under `-race`. Full Opus→downsample→feed path at G8 e2e.
- `docs/GATEWAY.md`: documented the listening-window gate in OnTrack(audio).
- Verified: build/vet/gofmt clean; full `go test -race ./internal/gateway/...` green;
  one-origin grep == 1.

### Phase G7b — the turn loop (interpretation A)
- `internal/gateway/turnloop/`: the driver. `Run(ctx, Deps)` greets (Get → speak →
  agent_done{turn:0}), then loops over start_turn/end. `Deps` is concrete (conn,
  sink, clock, SetMic, brain/tts/stt clients); the only interface is AgentSink.
- **Degradation (checklist #2), accurate rule:** the recovery must not require the
  failed component. Failures the brain CAN respond to → please_repeat (STT failure →
  empty transcript → brain → please_repeat, same as an intent-classifier failure;
  the turn advances, attempt_count++). Failures of reaching the brain (transport) or
  voicing (TTS) → silence-plus-signal (control error naming the unvoiced text +
  agent_done(active); call continues) — the gateway holds no phrases.
- **END_SPEECH ordering (checklist #5):** SetMic(nil) THEN mic.CloseInput(). The
  peer's feedMic now calls the handler UNDER the lock, so SetMic(nil) is a barrier —
  no Push can be in flight after it returns, so CloseInput (closing the channel)
  never races a push.
- **Inbox overflow (checklist #3):** the control reader's send is non-blocking —
  on a full buffer-of-8 (only if the browser sent control before agent_done, a
  protocol violation) it drops + logs and keeps reading, so it can always detect a
  disconnect.
- **TTS ctx deadline (checklist #1):** speak wraps Speak in
  context.WithTimeout(ctx, 30s) — bounds connect + streaming; inherits the call ctx
  so teardown aborts it.
- Wiring: `call.Registry` builds the gateway clients (brain/tts/stt) once;
  `StartTurnLoop` builds the concrete Deps and runs the loop; the Call gains a
  ctx/cancel (Close cancels it, aborting in-flight brain/STT/TTS). `main.go`
  controlHandler starts the loop after AttachControl.
- **Endpoint seam:** `stt.Client.WSURL` / `tts.Client.Base` exported (default to the
  vendor URL) so tests point real clients at stubs — no new interfaces.
- Tests (`turnloop_test.go`, `-race`): a stub brain/Sarvam-WS/ElevenLabs + control
  WS harness. `TestSTTFailureFoldsToPleaseRepeat` (STT unreachable → brain.Turn("")
  asserted → please_repeat spoken, audio frames asserted) and
  `TestTTSFailureSignalsAndContinues` (TTS 500 → "could not be voiced" error +
  agent_done(active), no audio, brain called exactly twice over two turns → no
  double-advance, next turn works).
- `docs/GATEWAY.md`: deleted the contradictory no-brain pseudocode branch; added the
  per-side degradation rule.
- Verified: build/vet/gofmt clean; full `go test -race ./internal/gateway/...` green;
  one-origin grep == 1; boot smoke (control 404 pre-offer, offer 400).

### Phase G7c — teardown integration + checklist close
- `call.Close` now tears down in one strict, `sync.Once`-idempotent order: (1)
  cancel the call ctx (aborts in-flight brain/STT/TTS → the turn loop unwinds); (2)
  close the control socket; (3) **wait for the turn loop to exit** (`waitLoop` on a
  `loopDone` channel set by `StartTurnLoop`) — by here its last `AGENT_PCM` tee is
  done, so no agent producer remains and `rec.Close` can't race a `SendAt`; (4)
  `peer.Close` (flushes the final video AU); (5) `rec.Close`; (6) `rec.Finalize`.
- **Disconnect == /close:** both `POST /close` (`closeHandler`) and a terminal peer
  state (`OnConnectionStateChange` → `onClose` → `Registry.End`) route to the same
  `call.Close`. Stated + verified by inspection.
- **Mid-utterance teardown** is clean: cancel aborts `Speak`; the agent audio
  already streamed was tee'd to the recorder BEFORE the browser send (G5 tee-first),
  so the recording survives. Test `TestMidUtteranceTeardownUnwinds` (`-race`): a
  hanging TTS holds the greeting mid-stream, then a cancel makes `Run` unwind
  promptly (the gateway's request ctx aborts the HTTP call). The full `Call.Close`
  (real peer/rec) is exercised at G8 e2e.
- `docs/GATEWAY.md`: rewrote the Teardown section to the 6-step order; fixed the last
  copy of the contradictory STT-timeout rule (STT-rules section) to route through
  the brain.
- Verified: build/vet/gofmt clean; full `go test -race ./...` green; one-origin
  grep == 1.

## G7 checklist — CLOSED
1. **TTS ctx deadline** ✅ G7b — `speak` wraps `context.WithTimeout(ctx, 30s)`
   (bounds connect + streaming; inherits the call ctx).
2. **Degradation** ✅ G7b — accurate per-side rule: failures the brain can respond
   to (STT/intent) → please_repeat; failures of reaching the brain or voicing (TTS)
   → silence-plus-signal. Both paths tested.
3. **Live audio wiring** ✅ G7a — `OnTrack(audio)` → `Downsampler` → `MicBuffer` →
   `stt.Run`, gated by the listening window.

---

# Deploy — containerization + S3 upload + compose

Step 4 of the build order, the deploy plane: package all four services as images,
wire them with one `docker compose`, and close the last real S3 gap (the recorder
was still writing local-only URLs). No new product behavior — this is the box the
existing code runs in.

## Findings (before code)

- The recorder's `runFinalize` had a `// no S3 yet` local-dir fallback; the brain's
  `s3.py` still uploaded `*.mp4`. But per CONTRACTS §3 the **recorder** owns the
  MP4 now and the **brain** owns the transcripts — the S3 ownership was split in
  the design but not in the upload code.
- The intent service still imported `from services.intent.classifier` (the old
  nested layout) via a gitignored `services/intent → ../intent` symlink shim. Fine
  for local dev, wrong for a self-contained image that ships only `intent/`.
- The two Go services are one module rooted at the repo; their images must build
  from the repo root, the Python ones from their own dirs.

## Decisions (confirmed with the user)

- **Split the gateway URL the brain hands out.** `gateway_offer_url` is consumed by
  the **browser** (it POSTs its WebRTC offer there); `/close` is consumed by the
  **brain** (server→server). Under Docker these are different addresses — the
  browser is outside the network and can't resolve `gateway`, so it needs the
  published host port. New `gateway_public_url` config (browser-facing, default
  `http://localhost:8080`) feeds `gateway_offer_url`; `gateway_url` (internal,
  `http://gateway:8080` in compose) still feeds `/close`. Without this the offer
  POST fails with a DNS error on localhost-compose — a real defect, not a caveat.
- **Write `docs/DEPLOYMENT.md`** (compose + `.env.example` reference it): env-var
  table, build/run, the localhost-vs-prod gateway-URL note, coturn prod config,
  S3/IAM.

## Phase log

### Recorder — S3 upload at finalize
- `internal/recorder/s3.go`: `S3Uploader` (aws-sdk-go-v2 + `manager.Uploader`).
  `NewS3Uploader` returns **nil** when `AWS_S3_BUCKET` is unset or AWS config fails
  to load → finalize keeps local URLs (invariant 4: a missing/failed upload
  degrades the recording, never fails the call). `upload` streams the file handle
  (never whole-into-memory), `video/mp4` + SSE-AES256.
- `s3Key`/`s3URL` mirror `brain/app/storage/s3.py` exactly — `{folder}/{sid}/{file}`
  (folder omitted when empty), each URL segment `url.PathEscape`d — so recorder MP4
  URLs and brain transcript URLs share the per-session prefix and the same encoding.
- `httpapi.go`: `runFinalize` uploads the produced mp4 when S3 is configured, else
  keeps the local path; `NewHTTPAPI(dir, s3)` gains the uploader; `main.go` builds
  it from the `AWS_*` env. Credentials via the standard chain (env / shared config
  / IAM role) → blank keys on EC2 use the instance role.
- `s3_test.go`: local-fallback (nil uploader → URL = local path, no network) over
  nothing-captured + audio-only; and `TestS3KeyAndURL` pins the key/URL scheme
  (incl. a folder with a space → `%20`) against the brain's.
- Deps: aws-sdk-go-v2 `config` + `feature/s3/manager` + `service/s3` (+ indirects).
  `go mod tidy` is a no-op afterwards.

### Brain — transcripts to S3 (MP4 ownership moved out)
- `s3.py`: `S3_UPLOAD_SUFFIXES` flipped `(".mp4",)` → `(".json", ".txt")` — the
  brain pushes the transcript artifacts it writes (`recap.py`); the recorder pushes
  the MP4. Docstring updated; logic otherwise unchanged (same boto3 chain + local
  fallback). `boto3` uncommented in `requirements.txt`.

### Intent — flat, self-contained layout
- `service.py` / image now use `from classifier import …` and run
  `uvicorn service:app` (was `services.intent.service:app`). `requirements.txt`
  dropped the `-r ../../requirements-base.txt` indirection for an explicit list
  (fastapi, uvicorn, sentence-transformers, scikit-learn, numpy, joblib, loguru).
  `models/` resolves via `Path(__file__).parent` → correct flat **and** in-image.
  The local `services/` symlink shim stays gitignored for `uvicorn` dev runs.

### Containers + compose
- Dockerfiles: **gateway** — distroless/static, `CGO_ENABLED=0` static binary,
  ships `web/`; **recorder** — debian-slim + ffmpeg (the one finalize combine),
  non-root, owns `/recordings`; **brain** — slim venv multi-stage, non-root;
  **intent** — builder bakes CPU-only torch (PyTorch CPU index, first, so
  sentence-transformers resolves against it) + the MiniLM encoder, slim runtime.
  All non-root, all with healthchecks. `.dockerignore` per context (repo-root one
  trims to the Go inputs; intent's drops the re-baked `sentence_encoder/`).
- `docker-compose.yml`: control plane (intent unpublished + brain `:8000`) and
  media plane (recorder `:9090/:9091`, gateway `:8080`); coturn host-net, PROD-ONLY,
  not started by default. `.env.example` documents every secret; blanks degrade
  (no S3 → local, no TURN → host candidates, no OpenAI → transliteration no-op).
- `gateway_public_url` split: `config.py` adds the field, `routes/sessions.py`
  builds `gateway_offer_url` from it, compose sets `GATEWAY_PUBLIC_URL:
  http://localhost:8080` alongside the internal `GATEWAY_URL`. Env binding verified
  (a `GATEWAY_PUBLIC_URL` override reaches the field; `gateway_url` stays internal).
- `docs/DEPLOYMENT.md`: the deploy wiring doc the compose + `.env.example` point at.

### Verified
- `go build`/`vet` clean; full `go test ./...` green (incl. the new recorder S3
  tests); `go mod tidy` no-op; new `s3.go`/`s3_test.go` gofmt-clean; one-origin
  grep still == 1 (this phase doesn't touch the gateway clock).
- Brain `config`/`routes.sessions` import; both gateway URLs bind from env.
- `docker compose config` parses (warnings are just unset secrets in the test shell).

### Dockerfile hardening pass (build + run all four)
- Cleaned + shortened every Dockerfile (brain 39→21, intent 81→41, gateway/recorder
  ~30→18). **brain went single-stage** — its deps are pure-Python wheels (no
  compilers), so the builder/venv multi-stage bought nothing; a cache-mounted
  `pip install` is the whole image. intent KEEPS multi-stage (it genuinely needs
  the model bake + keeping build out of runtime); dropped its `build-essential`
  (all deps ship cp312 wheels — the build confirmed).
- **CPU-only torch verified, not just intended:** the intent build installs
  `torch-2.12.0+cpu` from the PyTorch CPU index, and the later `pip install -r`
  reuses it (`torch already satisfied … +cpu`) — zero `nvidia-*`/CUDA packages.
  Final image **814 MB** (a CUDA build would be 5–7 GB).
- **sklearn pinned to 1.5.0 to match the `.pkl` training version** (hard-invariant
  #1 — a provable, byte-identical match for the consent classifier, not "passed the
  cases tested"). Eliminates the `InconsistentVersionWarning` (verified: 0 in the
  service logs; outputs still yes/no/please_repeat). **numpy stays on 2.x** — the
  pickles embed a numpy-2 BitGenerator, so numpy<2 hard-fails loading (`MT19937 is
  not a known BitGenerator module`), while sklearn 1.5.0 runs cleanly on numpy 2.4.6
  (cp312/aarch64 wheel, no source compile) — both verified in an isolated container
  before touching the image. sentence-transformers pinned 5.5.1.
- **Bug found by smoke-testing (not by building):** `SentenceTransformer.save()`
  writes `model.safetensors` mode 0600/root, so the non-root runtime user got
  `PermissionError` loading it → the service silently folded **every** classify to
  please_repeat while `/health` stayed 200. Fix: `chmod -R a+rX /opt/models` in the
  builder (COPY preserves it). Also restored the HF cache mount on the bake step.
- **All four built AND ran (smoke-tested), not just `compose config`:** brain
  `/health` 200; intent `/classify` → yes / no / please_repeat (real, post-fix);
  recorder boots HTTP :9090 + gRPC :9091 with ffmpeg 5.1.9 and the S3 local-fallback
  log; gateway boots and listens on :8080. Images: gateway 28.9 MB (distroless
  static), brain 366 MB, recorder 728 MB, intent 814 MB.

