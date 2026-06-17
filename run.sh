#!/usr/bin/env bash
# Run the VideoKYC stack locally via Docker Compose.
#
# Starts the four services (intent, brain, recorder, gateway) and skips the
# prod-only coturn. Open http://localhost:8080 once the gateway is up.
#
#   ./run.sh          build + start in the foreground (Ctrl-C to stop)
#   ./run.sh down     stop and remove the containers
#   ./run.sh logs     follow logs
set -euo pipefail

cd "$(dirname "$0")"

SERVICES=(intent brain recorder gateway)

case "${1:-up}" in
  up|"") ;;
  down)  exec docker compose down ;;
  logs)  exec docker compose logs -f "${SERVICES[@]}" ;;
  *)     echo "usage: $0 [up|down|logs]" >&2; exit 2 ;;
esac

# Compose reads vendor keys + AWS/TURN from .env. Bootstrap it from the example;
# a localhost run only needs SARVAM_API_KEY + ELEVENLABS_API_KEY for live STT/TTS.
if [[ ! -f .env ]]; then
  echo ">> no .env found — copying .env.example (add SARVAM_API_KEY + ELEVENLABS_API_KEY for live STT/TTS)"
  cp .env.example .env
fi

echo ">> starting intent + brain + recorder + gateway (coturn is prod-only, skipped)"
echo ">> open http://localhost:8080 once the gateway logs 'listening on :8080'"
exec docker compose up --build "${SERVICES[@]}"
