# syntax=docker/dockerfile:1
# Gateway — media plane, pure-Go static binary. Build from the repo root:
#   docker build -f cmd/gateway/Dockerfile -t kyc-gateway .
FROM golang:1.26.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# distroless/static carries ca-certificates (TLS to Sarvam/ElevenLabs) + nonroot.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
COPY --from=build /src/web /app/web
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
