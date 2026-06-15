# Architecture

## Services and planes

```
                          control plane (text, slow path)
   ┌─────────┐  POST /start, /end, GET /sessions/{id}   ┌──────────────────┐
   │ Browser │ ────────────────────────────────────────▶│  brain (Python)  │
   │ cam+mic │                                           │  state machine   │
   └────┬────┘                                           │  phrases / i18n  │
        │  POST /offer (SDP) + WS /control               │  transcript      │
        │  ═══ SRTP media (H264 + Opus) ═══              └───┬────────┬─────┘
        ▼                                          GET session│        │POST /classify
   ┌──────────────────────────────────┐  POST /turn {text}  │        ▼
   │  gateway (Go)   MEDIA PLANE       │◀────────────────────┘  ┌──────────────┐
   │  - Pion 1:1 peer (terminate)      │                        │ intent (Py)  │
   │  - Opus→PCM→Sarvam STT (Go)       │                        │  UNCHANGED   │
   │  - ElevenLabs TTS→track (Go)      │                        └──────────────┘
   │  - turn loop (driver)             │
   └───┬──────────────────────────────┘
       │ gRPC frame stream + POST /finalize   (loopback, same host)
       ▼
   ┌──────────────────────────────────┐
   │  recorder (Go)  MEDIA PLANE       │ → full_call.mp4 → S3
   │  - crash-safe append (video+2 PCM)│
   │  - ffmpeg -c copy combine @ end   │
   └──────────────────────────────────┘
```

- **Control plane** = decisions. Text only, one message per utterance. brain + intent.
- **Media plane** = bytes. Per-frame, real-time. gateway + recorder.
- The seam between them is **text, once per turn**. Guard it: if you ever add a
  2nd/3rd Go↔brain call per turn, the design is rotting into a distributed
  monolith. Don't.

## The four packet paths

```
(1) VIDEO — record only, NO decode:
  cam ─H264/SRTP→ Pion OnTrack(video) ─depacketize (public API)→ H264 access units
       └─ gRPC {VIDEO_AU, ts} ─→ recorder (append to video_raw.mp4, fragmented)

(2) AUDIO IN — STT + record:
  mic ─Opus/SRTP→ Pion OnTrack(audio) ─decode→ 48k PCM
       ├─ resample 16k ─→ Sarvam STT WS (Go) ─→ vad / final transcript
       └─ gRPC {USER_PCM 48k, ts} ─→ recorder (append user.pcm)
  on END_SPEECH → POST brain /turn {transcript} → {agent_text, tts_plan, ...}
                → WS /control → browser {vad}/{final}/{agent_done}

(3) AUDIO OUT — TTS + record (peer is SEND-ONLY; no outbound media track):
  tts_plan → ElevenLabs (Go) → 24k PCM → resample 48k
       ├─ binary frames on control WS ─→ browser plays (Web Audio)
       └─ gRPC {AGENT_PCM 48k, ts} ─→ recorder (append agent.pcm)

(4) FINALIZE — once, at end:
  gateway → recorder POST /finalize → ffmpeg: video copy + stereo(userL,agentR)
          → full_call.mp4 → S3 → status complete|partial|audio_only|failed
```

The fragile thing in the old code — reaching into aiortc's private
`_RTCRtpReceiver__jitter_buffer` — is **gone**. Pion's `OnTrack` hands you
depacketized access units through a public, stable API.

## What is "on the loop" now

The old world had one Python asyncio loop + the GIL: every call's media work
was serialized through one interpreter. There is no equivalent shared
serialization point now.

| Work | Old (Python, 1 proc, GIL) | New |
|---|---|---|
| WebRTC RTP/SRTP per packet | on the loop, GIL-serialized across calls | gateway: per-call goroutines, parallel across cores |
| Audio decode / resample | on the loop (numpy/frame) | gateway goroutines |
| STT/TTS streaming | async on the loop | gateway goroutines (Go net I/O) |
| Video write | already on a thread | recorder process |
| Intent | already a remote service | unchanged |
| State machine / phrases | on the loop (µs) | brain: trivial async handler (µs) |

Decisive change: call A's packet no longer waits behind call B's resample.

## Run trace — 1 user

```
t0  Browser → brain POST /sessions/start {customer data}
      brain: build phrases, create session{flow=greeting}, build greeting tts_plan.
      ← {session_id, agent_text, tts_plan, language, ice_servers, gateway_offer_url, ...}
      brain process: one coroutine, ~µs CPU, then idle.

t1  Browser → gateway POST /sessions/{id}/offer {sdp}
      gateway: Pion creates peer, setRemoteDescription, createAnswer, opens gRPC
               RecordStream to recorder. GET brain /sessions/{id} → language + greeting plan.
      gateway spawns, for THIS call: ICE/DTLS handler, OnTrack(video), OnTrack(audio),
               outbound agent-track pump, turn-loop driver — each a goroutine.
      recorder: one goroutine + opens video_raw.mp4 + user.pcm + agent.pcm.

t2  Greeting plays (TTS→track, teed to recorder). Then loop:
      STT streams mic→Sarvam; on END_SPEECH → brain POST /turn {transcript}
      → brain: classify (→intent svc), flow.step, build next tts_plan → returns.
      gateway speaks reply. Loop until status != active.

t3  Browser → brain POST /end. brain writes transcript.{txt,json}, calls gateway /close.
      gateway closes peer, calls recorder /finalize. recorder: ffmpeg combine → S3 → status.
```

CPU for 1 user: a fraction of one core (SRTP + Opus decode + resample), near-zero
for H.264 passthrough (no decode), bursts of net I/O for STT/TTS. brain + intent
idle between turns.

## Run trace — 2 users at once (4-core box)

```
OLD (Python, 1 worker, GIL):
  core0: [A.rtp][B.rtp][A.resample][B.resample]...  ← all media on ONE core, serialized
  core1..3: idle      → 3rd call → loop lag → ~2-call ceiling

NEW (Go gateway, GOMAXPROCS=4):
  core0: A.audio decode+resample (goroutine)
  core1: B.audio decode+resample (goroutine)   ← A and B at the SAME TIME
  core2: A+B video tee (cheap, no decode)
  core3: STT/TTS net I/O + recorder gRPC for both
  brain: both /turn calls concurrent async handlers, µs each — no contention
  recorder: A and B each own goroutine + own files — no shared lock
```

The new ceiling is real shared resources, not the GIL — and none is fixed by Go:

1. **Cores.** ~`cores / per-call-media-CPU` calls. H.264 passthrough (no
   decode/encode) keeps per-call CPU small, so this ceiling is high.
2. **NIC / TURN bandwidth.** N × ~0.5–2 Mbps in + recorder writes. On a cloud VM
   with TURN relaying, this is the most likely real wall. **Measure it early.**
3. **Vendor concurrency.** Sarvam/ElevenLabs plan limits. The architecture
   cannot raise these — check the contract before assuming Go unlocks 100 calls.
