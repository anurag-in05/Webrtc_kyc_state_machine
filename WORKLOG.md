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
| 3 | apply Step 4 w/ fix (finalize, httpapi, session, videowriter, main) | `go build` + `go vet` clean | ⏳ |
| 4 | targeted alignment test | gapped agent burst lands at correct offset | ⏳ |

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
