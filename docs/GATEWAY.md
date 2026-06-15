# Gateway (Go) — media plane

Binary: `cmd/gateway`, port `:8080`. Single Go module at repo root. This replaces
the old `app/services/webrtc_video.py`. Read `docs/CONTRACTS.md` first.

## Job

Terminate the one WebRTC peer per call, run STT/TTS, drive the turn loop, tee
media to the recorder. **Not an SFU** — no forwarding, no subscribers.

## Dependencies (only these)

- `github.com/pion/webrtc/v4` — peer, OnTrack, depacketize.
- `github.com/pion/rtp`, `.../codecs` — H.264/Opus depacketize if needed beyond v4 helpers.
- `google.golang.org/grpc` + the generated `proto/recorder.pb.go` — recorder client.
- `gorilla/websocket` (or `nhooyr.io/websocket`) — the `/control` WS + Sarvam WS client.
- stdlib `net/http`, `encoding/json`, `encoding/base64` — brain client + ElevenLabs.
- `github.com/pion/opus` — **pure-Go Opus DECODE** for the inbound mic. There is
  **no Opus encoder** in this service: the peer is **send-only** (browser pushes
  mic+camera up, subscribes to nothing) and the agent voice goes to the browser
  as binary frames over the control WS (see CONTRACTS §2). Do not add a WebRTC
  outbound audio track and do not add an Opus encoder (no pure-Go one exists;
  cgo is out).
- A tiny resampler — the only ratios are fixed integers (48k→16k decimate-by-3
  for STT, 24k→48k upsample-by-2 for the agent stream). Write ~30 lines; do not
  pull a resampling library.

No SFU libs. No media frameworks. No DI container. No Opus encoder.

## Package layout (`internal/gateway/`)

```
session/     the per-call clock — the one call_us origin (one time.Now in the gateway)
call/        per-call orchestrator + live-call registry: owns session+peer+recordclient, teardown
peer/        Pion peer create/answer/close, OnTrack wiring, ICE restart
stt/         Sarvam WS client  (CONTRACTS §5) — PCM in → vad/final out
tts/         ElevenLabs client (CONTRACTS §5) — executes a tts_plan → PCM frames
turnloop/    the driver (interpretation A)
brainclient/ GET /sessions/{id}, POST /sessions/{id}/turn
recordclient/ gRPC RecordStream + POST /finalize
control/     the /control websocket (CONTRACTS §2)
config/      env config
```

One file per job. If `peer` exceeds ~300 lines it's doing two jobs — split.

## Per-call goroutines (spawned on offer)

- ICE/DTLS handled by Pion internally.
- `OnTrack(video)`: read RTP → depacketize H.264 access units → drop until first
  keyframe → `recordclient.Send(VIDEO_AU, ts_us, au)`. **No decode.**
- `OnTrack(audio)`: read RTP → Opus decode (pure-Go) → 48k PCM. Fan out:
  (a) `recordclient.Send(USER_PCM, ts_us, pcm48)`,
  (b) resample → 16k → into the STT stream **for the active turn only**. The
  16k feed goes to a mutex-guarded mic handler on the peer (`SetMic`): the turn
  loop sets it to the turn's `stt.MicBuffer.Push` at `start_turn` and clears it at
  `END_SPEECH`. This is the listening-window gate — STT never hears the idle audio
  between turns. One `Downsampler` per track keeps the decimation phase continuous.
- agent audio out: the peer is **send-only**, so there is no outbound media
  track. `tts` produces 48k PCM that is (a) teed to the recorder as AGENT_PCM and
  (b) written as **binary frames on the control WS**; the browser plays them via
  Web Audio. No RTP, no Opus encode.
- `turnloop`: the sequencing below.

`ts_us` per stream = monotonic microseconds since that stream's first frame.

## Turn loop (interpretation A — port of the old `_run_turn_loop`)

```
on offer accepted:
  bootstrap = brainclient.Get(session_id)        // language, greeting plan, voice ids
  speak(bootstrap.greeting_plan, turn=0)         // tts → WS binary + tee AGENT_PCM
  control.Send(agent_done{turn:0, status: bootstrap.status})

loop until peer closed:
  msg = <-control.Inbox
  if msg.type == "start_turn":
     transcript, vadEvents = stt.Run(micPCM16k)   // forward vad to control as they arrive
     // STT failure (timeout/error) → transcript = "". DO NOT short-circuit: still
     // call the brain, which returns please_repeat for an empty transcript.
     resp = brainclient.Turn(session_id, transcript)   // {agent_text, tts_plan, status, events,...}
     control.Send(final{...resp...})
     if resp.agent_text != "": speak(resp.tts_plan, resp.turn_index)
     control.Send(agent_done{turn: resp.turn_index, status: resp.status})
     if resp.status != "active": break
  if msg.type == "end": break
```

**Degradation — the recovery must not require the failed component.** Failures the
brain *can respond to* converge on please_repeat: an STT failure yields an empty
transcript, which the brain turns into please_repeat exactly as an intent-classifier
failure does (the turn advances, `attempt_count++`, same step — three-strikes works
for free). Failures of *reaching* the brain (transport error) or of *voicing* a
reply (TTS error) degrade to silence-plus-signal: the gateway holds no phrases, so
it cannot synthesize please_repeat itself — it sends a control `error` (naming the
unvoiced text, so the UI can fall back to the already-sent `final`) + `agent_done`,
and the call continues.

`speak(plan, turn)`: for each plan item — `speech` → `tts.Stream(text, voice_id,
model_id, slow, speed)` → for each PCM chunk: resample 24k→48k, send it as a
**binary frame on the control WS** and `recordclient.Send(AGENT_PCM, ts_us,
pcm48)`; `silence` → send zero-PCM frames + tee the same zeros. After the plan,
send `agent_done` (no track to drain). Optionally hold ~250 ms before accepting
the next `start_turn` so the browser finishes playing the tail.

## STT stream rules (port faithfully)

- Forward `START_SPEECH`/`END_SPEECH` to `/control` as `vad` messages.
- On `END_SPEECH`, stop forwarding mic audio and send Sarvam `{"type":"flush"}`.
- 15 s hard timeout (wall-clock from connect) → treat as failure → empty transcript.
  The loop still calls the brain with `""` (which returns please_repeat) — see the
  degradation rule above; it does NOT short-circuit.
- Bounded mic buffer with drop-oldest under backpressure (the old code used 400 ×
  20 ms frames ≈ 8 s). Reuse that bound. Drop oldest, never block the reader.

## Teardown (`call.Close`, idempotent under `sync.Once`)

Both `POST /close` and a terminal peer state (`OnConnectionStateChange` Failed/Closed
→ `onClose` → `Registry.End`) route to the same `call.Close`, which tears down in
one strict order, each step a prerequisite of the next:

1. **cancel the call ctx** — aborts in-flight brain/STT/TTS so the turn loop unwinds;
2. **close the control socket** — Inbox closes, the loop's sends fail gracefully;
3. **wait for the turn loop to exit** — by here it has cleared `SetMic` and finished
   its last `AGENT_PCM` tee, so no agent producer remains;
4. **close the peer** — its OnTrack read goroutines exit and flush the final video
   access unit (the last video producer);
5. **`recordclient.Close`** — now safe (no producer) → half-close, recorder flushes;
6. **`recordclient.Finalize`** — kick the ffmpeg combine.

A mid-utterance teardown is clean: step 1 aborts `Speak`, and the agent audio already
streamed was tee'd to the recorder BEFORE the browser send (tee-first), so the
recording survives the drop. The recorder owns its own file flush.

## Config (env)

`PORT=8080`, `BRAIN_URL`, `RECORDER_GRPC_ADDR`, `RECORDER_HTTP_URL`,
`SARVAM_API_KEY`, `ELEVENLABS_API_KEY`, `TURN_URL/TURN_USERNAME/TURN_CREDENTIAL`,
`GOMAXPROCS` (leave default = cores). Nothing else.

## Do NOT

- decode or re-encode video (passthrough only),
- hold any phrase/i18n/markup logic (the brain sends resolved text + plan),
- add simulcast/SVC/bandwidth-estimation/forwarding,
- invent retries/circuit-breakers beyond: STT/TTS failure → fold the turn to a
  no-op the brain will see as `please_repeat` (return empty transcript), recorder
  send failure → log + drop the frame (never block media).
