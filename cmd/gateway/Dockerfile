# syntax=docker/dockerfile:1
# Gateway (media plane) — pure-Go, static binary: no cgo, no ffmpeg.
# Build from the REPO ROOT (single module kyc-monorepo):
#   docker build -f cmd/gateway/Dockerfile -t kyc-gateway .

############################  build  ############################
FROM golang:1.26.3-bookworm AS build
WORKDIR /src

# Module graph first so deps cache across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off → a fully static binary that runs on distroless/static.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

############################  runtime  ############################
# distroless/static carries ca-certificates (TLS to Sarvam/ElevenLabs) and a
# nonroot user; nothing else. Pure-Go means we need nothing more.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
# The gateway serves the browser client from ./web (http.FileServer(http.Dir("web"))).
COPY --from=build /src/web /app/web

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
