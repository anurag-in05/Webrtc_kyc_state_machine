# syntax=docker/dockerfile:1.7
# Multi-stage, CPU-only image for the SHARED INTENT SERVICE
# (services.intent.service). Build from the REPO ROOT so requirements-base.txt
# is in context:
#
#   docker build -f services/intent/Dockerfile .
#
# This image carries ONLY the model + classification stack — sentence-transformers,
# scikit-learn, the baked MiniLM encoder, and CPU torch. The service package is
# self-contained (no `app.*` imports), so the media/storage source never enters
# this image at all.
#
#   Stage 1 (builder)  installs intent deps into a venv + pre-bakes the encoder.
#   Stage 2 (runtime)  slim image: venv + baked model + service source, non-root.

############################  Stage 1 — builder  ############################
FROM python:3.12-slim-bookworm AS builder

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH"

# build-essential covers any dep without a prebuilt 3.12 wheel; discarded with
# this stage so it never reaches the final image.
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv "$VIRTUAL_ENV"

WORKDIR /app
# Preserve the repo layout so the `-r ../../requirements-base.txt` reference
# inside services/intent/requirements.txt resolves.
COPY requirements-base.txt ./
COPY services/intent/requirements.txt services/intent/

# CPU-only torch from the PyTorch CPU index (PyPI's default wheel bundles ~3–4 GB
# of unused CUDA libs). No torchaudio here — that's only needed by silero-vad,
# which lives in the main image, not this one. Pinned for reproducible rebuilds.
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install torch==2.12.0 --index-url https://download.pytorch.org/whl/cpu
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install -r services/intent/requirements.txt

# Pre-bake the multilingual MiniLM (~465 MB) so the service pays no cold-start
# download and runs in network-restricted envs.
RUN --mount=type=cache,target=/root/.cache/huggingface \
    python -c "from sentence_transformers import SentenceTransformer; \
SentenceTransformer('paraphrase-multilingual-MiniLM-L12-v2').save('/opt/models/sentence_encoder')"

############################  Stage 2 — runtime  ############################
FROM python:3.12-slim-bookworm AS runtime

# Runtime-only system deps:
#   libgomp1 — OpenMP runtime needed by scipy / scikit-learn
#   curl     — used by the HEALTHCHECK
# NOTE: no ffmpeg here — the intent service does no media muxing.
RUN apt-get update && apt-get install -y --no-install-recommends \
        libgomp1 \
        curl \
    && rm -rf /var/lib/apt/lists/*

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH" \
    # Keep any stray HF/torch cache writes off the read-only app dirs.
    HF_HOME=/tmp/hf \
    XDG_CACHE_HOME=/tmp/.cache

# Deps from the builder (no compilers, no pip cache come along).
COPY --from=builder /opt/venv /opt/venv

WORKDIR /app
# Service source + the ensemble .pkl voters (tracked in git, ~500 KB). The
# local sentence_encoder dir is .dockerignore'd, so the baked copy below is
# authoritative — order matters: source first, baked encoder second.
COPY services /app/services
COPY --from=builder /opt/models/sentence_encoder /app/services/intent/models/sentence_encoder

# Non-root runtime. The intent service writes nothing, so no scratch dir needed.
RUN useradd --system --uid 10001 --no-create-home appuser
USER appuser

EXPOSE 8000

# start-period covers the ~3–5 s model warmup that runs in the lifespan before
# /health is served.
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS http://localhost:8000/health || exit 1

# --workers 1: ONE model copy. Scale by replicas (behind a balancer), not workers
# (a 2nd worker would just load the model again).
CMD ["uvicorn", "services.intent.service:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
