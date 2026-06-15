# VideoKYC — monorepo build context

This repo is a rewrite of an existing Python/aiortc video-KYC server. The
business logic (consent flow, phrases, intent model) is **kept verbatim**; the
**media layer is moved to Go** so one container holds many concurrent calls
instead of ~2.

Read these in order before writing code:

1. `docs/ARCHITECTURE.md` — the four services, packet paths, what runs where.
2. `docs/CONTRACTS.md` — every inter-service contract, pinned exactly. Load-bearing.
3. `docs/SETUP.md` — module path, dependencies, proto codegen, compose, env.
   Authoritative for build wiring; locks the pure-Go / send-only-peer decisions.
4. The per-service spec for whatever you're building:
   - `docs/GATEWAY.md` (Go, media plane)
   - `docs/RECORDER.md` (Go, durable capture) + `docs/RECORDER_MP4.md` (the
     high-risk fragmented-MP4 writer — read before touching `writer/`)
   - `brain/CLAUDE.md` (Python, control plane — mostly copied)
   - `intent/CLAUDE.md` (Python, copied unchanged)

## What we are building (one sentence each)

- **brain** (Python, `brain/`) — text-only control plane. Owns session state,
  the consent state machine, phrase building, intent classification, transcript
  artifacts. Never touches media.
- **gateway** (Go, `cmd/gateway`) — media plane. Terminates the **one** WebRTC
  peer per call (Pion), streams mic→Sarvam STT, plays agent TTS→track, **drives
  the turn loop** (calls the brain per utterance), tees media to the recorder.
- **recorder** (Go, `cmd/recorder`) — durable capture. Receives tagged media
  frames over gRPC, appends to crash-safe files, runs one ffmpeg combine at
  finalize, uploads to S3.
- **intent** (Python, `intent/`) — the existing ensemble classifier service,
  **copied unchanged**. The brain calls it.

This is **not an SFU**. One human + one bot = zero fan-out. The gateway is a 1:1
WebRTC termination point with a recorder tap. No forwarding, no simulcast, no
layer selection. Do not add any.

## Hard invariants (do not violate)

1. **Preserve verbatim** (copy the Python files, do not rewrite or "improve"):
   the state machine, turn-service step logic, phrase builder, i18n packs,
   `language_processor`, the intent service, the `StartSessionRequest` schema,
   all event types and the three intents (`yes` / `no` / `please_repeat`).
   These are correct and reviewed. Changing a phrase or an event name is a bug.
2. **Any pipeline failure folds to `please_repeat`** (empty transcript, STT
   error, intent error, timeout). The call degrades; it never breaks.
3. **The brain never sees media bytes.** The only thing crossing Go↔brain is
   text: a transcript in, agent text + a TTS plan out. One round-trip per turn.
4. **The recorder never fails the call.** A recorder panic/OOM degrades the
   recording (`partial` / `audio_only`); the conversation continues.
5. **Crash-safe recording:** a killed recorder process must leave a playable
   video file (fragmented MP4) and recoverable audio (raw PCM).
6. **The turn loop lives in the gateway** (next to the audio it sequences). The
   brain decides *what to say*; the gateway decides *when*. Do not move turn
   sequencing into the brain — it splits timing across the network.

## Anti-overengineering rules (enforced)

- Build only what these specs describe. No features, endpoints, config flags, or
  abstractions that aren't requested here.
- No interfaces/factories for single-implementation code. Concrete types.
- No error handling for impossible states. Handle the failures named in the
  specs; let genuine bugs panic/500.
- No "pluggable" anything. One STT vendor (Sarvam), one TTS vendor (ElevenLabs),
  one storage backend (S3, with a local-dir fallback the existing code has).
- If a file is getting long, it's probably doing two jobs — split by job, not by
  speculative layer.
- Test against this question: *would a senior engineer call this overcomplicated?*

## Run (dev)

```bash
# Python services
cd brain  && uvicorn app.main:app --reload --port 8000
cd intent && uvicorn services.intent.service:app --port 8001

# Go services (single module at repo root)
go run ./cmd/gateway     # :8080
go run ./cmd/recorder    # :9090 http + :9091 grpc

# coturn for media across NAT (prod); skip on localhost
```

Or `docker compose up` (see `docker-compose.yml`). The browser client
(`web/index.html`) is copied from the old repo with **one change**: its
`RTCPeerConnection` offer POSTs to the gateway, not the Python app.

## Build order (each step ships independently)

1. **recorder** standalone: gRPC ingest → crash-safe files → ffmpeg combine →
   S3. Test by feeding it canned frames. No gateway needed.
2. **brain**: copy the Python logic, slim the API to text-only (see
   `brain/CLAUDE.md`). Test with curl — no media.
3. **gateway**: Pion peer + Sarvam/ElevenLabs port + turn loop + recorder tee.
   Wire to brain + recorder. Test against a browser.
4. Retarget `web/index.html` offer URL to the gateway. End-to-end.
