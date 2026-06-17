# syntax=docker/dockerfile:1
# Brain — text-only control plane (FastAPI). Build from ./brain.
FROM python:3.12-slim
ENV PYTHONUNBUFFERED=1

WORKDIR /app
COPY requirements.txt ./
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt
COPY app ./app

# Non-root; pre-create the transcript dir (root owns /app).
RUN useradd --system --uid 10001 --no-create-home brain \
    && mkdir -p /app/recordings && chown brain:brain /app/recordings
USER brain

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/health').status==200 else 1)"

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]

