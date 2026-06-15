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
| B3 | `storage/` + recap port + `routes/sessions.py` + `main.py` | app imports, TestClient | ⏳ |
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
