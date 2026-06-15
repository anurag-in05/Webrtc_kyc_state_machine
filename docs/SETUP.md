# Setup & scaffolding

Read after `CLAUDE.md`. This pins the build wiring so nothing is improvised:
module path, dependencies, proto codegen, compose, env. Decisions here are
authoritative; where they touch another doc, that doc was already updated.

## Decisions locked in this project

- **Go module path: `kyc`** (local, no remote host yet). `go mod init kyc`. All
  imports are `kyc/internal/...`. To publish later:
  `go mod edit -module github.com/<org>/kyc` then
  `grep -rl '"kyc/' --include='*.go' . | xargs sed -i '' 's#"kyc/#"github.com/<org>/kyc/#g'`.
- **Opus: pure-Go DECODE only** (`github.com/pion/opus`). **No Opus encoder.**
- **Peer is SEND-ONLY.** Browser publishes mic + camera; subscribes to nothing.
  The agent voice goes to the browser as **binary frames on the control WS**
  (raw s16le mono 48 kHz), played via Web Audio. No outbound WebRTC media track.
  (This is why no encoder is needed. See CONTRACTS §2, GATEWAY.md, ARCHITECTURE §(3).)
- **Recorder video writer: pure-Go fragmented MP4** via `github.com/Eyevinn/mp4ff`.
  See `RECORDER_MP4.md`. ffmpeg is used **only at finalize** (one stream-copy).
- **Resampling: hand-written, fixed integer ratios** (48k→16k /3, 24k→48k ×2).
  ~30 lines. No resampling dependency.

## Prerequisites (host)

- **Go 1.22+** (`brew install go`)
- **ffmpeg + ffprobe** on PATH — recorder finalize only (`brew install ffmpeg`)
- **protoc + Go plugins** for the recorder gRPC contract:
  ```bash
  brew install protobuf
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  # ensure $(go env GOPATH)/bin is on PATH
  ```
- **Python 3.12** for brain + intent (their own venvs / Docker).

## Initialise the Go module (run once at repo root)

```bash
cd ~/Documents/kyc-monorepo
go mod init kyc

go get github.com/pion/webrtc/v4
go get github.com/pion/rtp
go get github.com/pion/opus            # pure-Go Opus DECODE (inbound mic)
go get github.com/Eyevinn/mp4ff        # pure-Go fragmented MP4 (recorder video)
go get github.com/gorilla/websocket    # /control WS server + Sarvam STT client
go get google.golang.org/grpc google.golang.org/protobuf
go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/s3
go get github.com/aws/aws-sdk-go-v2/feature/s3/manager   # streaming upload
```

That is the complete dependency set. Do not add others without a reason in a doc.

## Generate the recorder gRPC stubs

`proto/recorder.proto` is in CONTRACTS §3. Add the Go options and generate:

```proto
// add near the top of proto/recorder.proto
option go_package = "kyc/proto/recorder;recorderpb";
```
```bash
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       proto/recorder.proto
# → proto/recorder.pb.go + proto/recorder_grpc.pb.go
```

## docker-compose.yml (repo root)

```yaml
services:
  intent:
    build: ./intent
    environment:
      - WARM_MODELS_ON_STARTUP=true
    # exposes :8000 inside; brain reaches it as http://intent:8000

  brain:
    build: ./brain
    ports: ["8000:8000"]
    environment:
      - INTENT_SERVICE_URL=http://intent:8000
      - OPENAI_API_KEY=${OPENAI_API_KEY}        # non-English only
      - RECORDER_HTTP_URL=http://recorder:9090   # for /status proxy on GET
      - GATEWAY_URL=http://gateway:8080          # for /close on /end
      - AWS_URL=${AWS_URL}                        # transcript artifact URLs
    depends_on: [intent]

  recorder:
    build: { context: ., dockerfile: cmd/recorder/Dockerfile }
    ports: ["9090:9090", "9091:9091"]            # http, grpc
    environment:
      - HTTP_ADDR=:9090
      - GRPC_ADDR=:9091
      - RECORDINGS_DIR=/recordings
      - AWS_S3_BUCKET=${AWS_S3_BUCKET}
      - AWS_MEDIA_FOLDER=${AWS_MEDIA_FOLDER}
      - AWS_REGION=${AWS_REGION}
      - AWS_URL=${AWS_URL}
    volumes: ["./recordings:/recordings"]

  gateway:
    build: { context: ., dockerfile: cmd/gateway/Dockerfile }
    ports: ["8080:8080"]
    environment:
      - PORT=8080
      - BRAIN_URL=http://brain:8000
      - RECORDER_GRPC_ADDR=recorder:9091
      - RECORDER_HTTP_URL=http://recorder:9090
      - SARVAM_API_KEY=${SARVAM_API_KEY}
      - ELEVENLABS_API_KEY=${ELEVENLABS_API_KEY}
      - TURN_URL=${TURN_URL}
      - TURN_USERNAME=${TURN_USERNAME}
      - TURN_CREDENTIAL=${TURN_CREDENTIAL}
    depends_on: [brain, recorder]

  coturn:
    image: coturn/coturn
    network_mode: host
    # prod only; configure --external-ip etc. as in the old README. Skip on localhost.
```

The two Go Dockerfiles build from the repo root (single module): build
`./cmd/gateway` and `./cmd/recorder` respectively. The recorder image needs
`ffmpeg` installed; the gateway image needs nothing extra (pure-Go, no cgo).

## .env (repo root, gitignored)

```
SARVAM_API_KEY=
ELEVENLABS_API_KEY=
OPENAI_API_KEY=
AWS_S3_BUCKET=
AWS_MEDIA_FOLDER=recordings
AWS_REGION=ap-south-1
AWS_URL=
TURN_URL=
TURN_USERNAME=
TURN_CREDENTIAL=
```

## Dev run (no Docker)

```bash
# terminal 1
cd intent && python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
uvicorn service:app --port 8000        # note: files are at intent/ root, not services/intent/

# terminal 2
cd brain && python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
INTENT_SERVICE_URL=http://localhost:8000 uvicorn app.main:app --reload --port 8001

# terminal 3 + 4 (after the Go code exists)
go run ./cmd/recorder
BRAIN_URL=http://localhost:8001 RECORDER_GRPC_ADDR=localhost:9091 \
RECORDER_HTTP_URL=http://localhost:9090 go run ./cmd/gateway
```

> Note: in this monorepo the intent files live at `intent/` (root), not
> `intent/services/intent/`. If `service.py` still does `from services.intent...`
> imports, either keep a `services/intent/` layout inside `intent/` or adjust the
> imports — pick one and keep `intent/CLAUDE.md`'s "unchanged" spirit (logic
> unchanged; only the import path may move).
