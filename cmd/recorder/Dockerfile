# syntax=docker/dockerfile:1
# Recorder — durable capture, Go binary + ffmpeg (finalize combine). Build from repo root:
#   docker build -f cmd/recorder/Dockerfile -t kyc-recorder .
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/recorder ./cmd/recorder

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
RUN useradd --system --uid 10001 --no-create-home recorder && mkdir -p /recordings && chown recorder:recorder /recordings
COPY --from=build /out/recorder /usr/local/bin/recorder
USER recorder
ENV RECORDINGS_DIR=/recordings
EXPOSE 9090 9091
ENTRYPOINT ["recorder"]
