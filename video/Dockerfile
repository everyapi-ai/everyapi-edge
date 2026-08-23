FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*
COPY requirements.txt .
RUN PIP_DEFAULT_TIMEOUT=120 PIP_RETRIES=10 pip install --no-cache-dir -r requirements.txt
COPY app.py model_config.py .

ENV HF_HOME=/models/cache
ENV EVERYAPI_VIDEO_DATA_PATH=/models/cache/jobs
EXPOSE 8191
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8191"]
