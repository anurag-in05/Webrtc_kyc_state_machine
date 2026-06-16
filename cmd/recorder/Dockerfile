# syntax=docker/dockerfile:1
# Recorder (media plane, durable capture) — Go binary + ffmpeg/ffprobe.
# Build from the REPO ROOT (single module kyc-monorepo):
#   docker build -f cmd/recorder/Dockerfile -t kyc-recorder .

############################  build  ############################
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/recorder ./cmd/recorder

############################  runtime  ############################
FROM debian:bookworm-slim AS runtime
# ffmpeg ships ffprobe too; the ONE finalize combine shells out to both
# (CONTRACTS §3). Nothing else runs a subprocess.
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Non-root; owns the recordings data dir (the compose volume mounts over it).
RUN useradd --system --uid 10001 --no-create-home recorder \
    && mkdir -p /recordings && chown recorder:recorder /recordings

COPY --from=build /out/recorder /usr/local/bin/recorder

USER recorder
ENV RECORDINGS_DIR=/recordings
EXPOSE 9090 9091
ENTRYPOINT ["recorder"]
