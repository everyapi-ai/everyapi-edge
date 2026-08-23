FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg libsndfile1 && rm -rf /var/lib/apt/lists/*
COPY requirements.txt .
RUN PIP_DEFAULT_TIMEOUT=120 PIP_RETRIES=10 pip install --no-cache-dir -r requirements.txt
COPY app.py model_config.py .

ENV HF_HOME=/models/cache
EXPOSE 8190
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8190"]
