FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
COPY requirements.txt .
RUN PIP_DEFAULT_TIMEOUT=120 PIP_RETRIES=10 pip install --no-cache-dir -r requirements.txt
COPY app.py .

ENV HF_HOME=/models/cache
EXPOSE 8193
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8193"]
