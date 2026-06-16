# Deployment

How the four services run as containers — locally via `docker compose`, or on a
single box. Read `ARCHITECTURE.md` for what each service does; this doc is only
the wiring: images, env, ports, and the two things that differ between localhost
and prod (the browser-facing gateway URL and coturn).

## Images

| Service  | Context  | Dockerfile                | Base / runtime                       |
|----------|----------|---------------------------|--------------------------------------|
| intent   | `./intent` | `intent/Dockerfile`     | python:3.12-slim, CPU torch, baked MiniLM |
| brain    | `./brain`  | `brain/Dockerfile`      | python:3.12-slim, venv                |
| recorder | repo root  | `cmd/recorder/Dockerfile` | debian-slim + ffmpeg, static Go binary |
| gateway  | repo root  | `cmd/gateway/Dockerfile`  | distroless/static (pure-Go, no cgo)   |

The Go images build from the **repo root** (single module); `.dockerignore`
trims the context to `go.{mod,sum}`, `cmd/`, `internal/`, `proto/`, `web/`. The
Python images build from their own dir and ship a flat layout (no `app.*` /
`services.*` imports). The intent build re-bakes the sentence encoder, so the
local `models/sentence_encoder/` is dropped from its context.

## 1. Configure secrets

```bash
cp .env.example .env   # .env is gitignored
```

Fill in what you use; blanks degrade gracefully (no S3 → local URLs, no TURN →
host-candidate-only ICE, no `OPENAI_API_KEY` → transliteration is a no-op).

| Variable | Read by | Purpose |
|----------|---------|---------|
| `SARVAM_API_KEY` | gateway | STT |
| `ELEVENLABS_API_KEY` | gateway | TTS |
| `OPENAI_API_KEY` | brain | name/address transliteration (optional) |
| `AWS_S3_BUCKET` | brain, recorder | artifact bucket; unset → local-dir fallback |
| `AWS_MEDIA_FOLDER` | brain, recorder | key prefix (blank = bucket root) |
| `AWS_REGION` | brain, recorder | e.g. `ap-south-1` |
| `AWS_URL` | brain, recorder | public/CDN base for object URLs |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | brain, recorder | omit on EC2 → IAM role |
| `TURN_URL` / `TURN_USERNAME` / `TURN_CREDENTIAL` | gateway (used), brain (echoed to browser) | NAT traversal |

The recorder uploads `full_call.mp4`; the brain uploads the transcripts
(`.json` / `.txt`). Both use the same `{folder}/{session_id}/` prefix and the
standard AWS credential chain — so on EC2 you leave the access keys blank and
attach an IAM role with `s3:PutObject` on the bucket.

## 2. Build & run

```bash
docker compose up --build        # intent, brain, recorder, gateway
```

Published ports: brain `8000`, gateway `8080`, recorder `9090` (http) / `9091`
(grpc). intent has **no** published port — the brain reaches it in-network at
`http://intent:8000`. Open the client at `http://localhost:8080` (the gateway
serves `web/index.html`).

coturn is **not** started by default (it needs prod config — see below). Skip it
on localhost: WebRTC uses host candidates over loopback.

## The browser-facing gateway URL (localhost vs prod)

The brain returns `gateway_offer_url` to the browser, which POSTs its WebRTC
offer there. This is **not** the same address the brain itself uses to reach the
gateway:

- `GATEWAY_URL` — brain→gateway, server to server (`POST /close`). In compose
  this is the in-network name `http://gateway:8080`.
- `GATEWAY_PUBLIC_URL` — the address the **browser** uses. The browser runs
  outside the Docker network, so it can't resolve `gateway`; it must hit the
  published host port. Compose defaults this to `http://localhost:8080`.

**In prod, set `GATEWAY_PUBLIC_URL` to the gateway's public address** (e.g.
`https://kyc.example.com`), terminated by your TLS reverse proxy. Getting these
two confused is the classic "offer POST fails with a DNS/connection error" bug.

## coturn (prod NAT traversal)

`coturn` relays SRTP when the browser and gateway can't reach each other
directly (the common case across the internet). It's defined in
`docker-compose.yml` with `network_mode: host` so it can advertise the box's
public IP, but it's commented as PROD-ONLY and needs configuration:

- Run coturn with `--external-ip=<public-ip>` (and `--realm`, a static
  `--user`, or a shared `--static-auth-secret` for time-limited credentials).
- Point `TURN_URL` / `TURN_USERNAME` / `TURN_CREDENTIAL` in `.env` at it; the
  brain echoes these to the browser in `/start`, and the gateway uses them for
  its own ICE.
- Open UDP `3478` (+ the relay port range) on the box's firewall/security group.

Skip all of this on localhost.
