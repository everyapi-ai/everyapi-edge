FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py model_config.py .

ENV HF_HOME=/models/cache
EXPOSE 8188
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8188"]
