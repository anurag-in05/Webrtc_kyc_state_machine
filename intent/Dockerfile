# syntax=docker/dockerfile:1
# Intent service (shared model container), CPU-only. Build from ./intent:
#   docker build -t kyc-intent ./intent
#
# Carries only the model + classification stack: sentence-transformers, sklearn,
# the baked MiniLM encoder, CPU torch. The service is self-contained (flat
# layout, no app.* imports). --workers 1: ONE model copy; scale by replicas.
#
#   Stage 1 (builder)  installs deps into a venv + pre-bakes the encoder.
#   Stage 2 (runtime)  slim image: venv + baked model + service source, non-root.

############################  builder  ############################
FROM python:3.12-slim AS builder
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH"

# build-essential covers any dep without a prebuilt 3.12 wheel; dropped with this stage.
RUN apt-get update && apt-get install -y --no-install-recommends build-essential \
    && rm -rf /var/lib/apt/lists/*
RUN python -m venv "$VIRTUAL_ENV"

WORKDIR /app
COPY requirements.txt ./
# CPU-only torch FIRST (PyPI's default wheel bundles ~3–4 GB of unused CUDA libs);
# sentence-transformers then resolves against the already-installed CPU torch.
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install torch --index-url https://download.pytorch.org/whl/cpu
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install -r requirements.txt

# Pre-bake the multilingual MiniLM (~465 MB) so the service pays no cold-start
# download and runs in network-restricted envs.
RUN --mount=type=cache,target=/root/.cache/huggingface \
    python -c "from sentence_transformers import SentenceTransformer; \
SentenceTransformer('paraphrase-multilingual-MiniLM-L12-v2').save('/opt/models/sentence_encoder')"

############################  runtime  ############################
FROM python:3.12-slim AS runtime
# Runtime-only system deps: libgomp1 (OpenMP for scipy/sklearn), curl (HEALTHCHECK).
# No ffmpeg — the intent service does no media work.
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 curl \
    && rm -rf /var/lib/apt/lists/*

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH" \
    # Keep stray HF/torch cache writes off the app dirs.
    HF_HOME=/tmp/hf \
    XDG_CACHE_HOME=/tmp/.cache

COPY --from=builder /opt/venv /opt/venv

WORKDIR /app
# Flat service source + the ensemble .pkl voters (.dockerignore drops the local
# sentence_encoder so the baked copy below is authoritative — source first).
COPY . /app
COPY --from=builder /opt/models/sentence_encoder /app/models/sentence_encoder

RUN useradd --system --uid 10001 --no-create-home appuser
USER appuser

EXPOSE 8000
# start-period covers the ~3–5 s model warmup in the lifespan before /health serves.
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS http://localhost:8000/health || exit 1

CMD ["uvicorn", "service:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
