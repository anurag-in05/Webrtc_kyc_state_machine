# syntax=docker/dockerfile:1
# Brain (text-only control plane). Build from ./brain:
#   docker build -t kyc-brain ./brain
# Multi-stage: install deps into a venv in the builder, copy only the venv +
# source to a slim runtime so compilers/pip cache never ship.

############################  builder  ############################
FROM python:3.12-slim AS builder
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH"
RUN python -m venv "$VIRTUAL_ENV"
WORKDIR /app
COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt

############################  runtime  ############################
FROM python:3.12-slim AS runtime
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    VIRTUAL_ENV=/opt/venv \
    PATH="/opt/venv/bin:$PATH"
COPY --from=builder /opt/venv /opt/venv
WORKDIR /app
COPY app ./app

# Non-root; owns the local transcript dir (settings.recordings_dir default).
RUN useradd --system --uid 10001 --no-create-home brain \
    && mkdir -p /app/recordings && chown brain:brain /app/recordings
USER brain

EXPOSE 8000
# /health is a real route (app/main.py). urllib avoids apt-installing curl.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/health').status==200 else 1)"

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
