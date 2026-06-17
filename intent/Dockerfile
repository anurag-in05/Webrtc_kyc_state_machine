# syntax=docker/dockerfile:1
# Intent — shared CPU-only model container. Build from ./intent.
# Builder installs deps + bakes the MiniLM encoder; runtime keeps only venv + model.
FROM python:3.12-slim AS builder
ENV VIRTUAL_ENV=/opt/venv PATH="/opt/venv/bin:$PATH"
RUN python -m venv "$VIRTUAL_ENV"

WORKDIR /app
COPY requirements.txt ./
# CPU-only torch FIRST from its own index, so sentence-transformers resolves
# against it instead of pulling the multi-GB CUDA build from PyPI.
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install torch --index-url https://download.pytorch.org/whl/cpu \
    && pip install -r requirements.txt
# Bake the encoder (~465 MB) so the runtime needs no network. save() writes the
# weights 0600; chmod a+rX so the non-root runtime user can read them.
RUN --mount=type=cache,target=/root/.cache/huggingface \
    python -c "from sentence_transformers import SentenceTransformer; \
SentenceTransformer('paraphrase-multilingual-MiniLM-L12-v2').save('/opt/models/sentence_encoder')" \
    && chmod -R a+rX /opt/models

FROM python:3.12-slim AS runtime
# libgomp1: OpenMP runtime for sklearn/scipy. curl: healthcheck.
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 curl \
    && rm -rf /var/lib/apt/lists/*
ENV PYTHONUNBUFFERED=1 \
    PATH="/opt/venv/bin:$PATH" \
    HF_HOME=/tmp/hf \
    XDG_CACHE_HOME=/tmp/.cache
COPY --from=builder /opt/venv /opt/venv

WORKDIR /app
COPY . /app
COPY --from=builder /opt/models/sentence_encoder /app/models/sentence_encoder
RUN useradd --system --uid 10001 --no-create-home appuser
USER appuser

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS http://localhost:8000/health || exit 1
CMD ["uvicorn", "service:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
