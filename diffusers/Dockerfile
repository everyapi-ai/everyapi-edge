FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
COPY requirements.txt .
# pip gives a network read 15 seconds and 5 attempts by default, which is not enough for a 12.8 MB wheel over a slow or filtered link — the ROCm speech image failed twice in a row on exactly that, while the CUDA one survived only because its second attempt landed. Suppliers build these images themselves, so the same ceiling applies wherever they are.
RUN PIP_DEFAULT_TIMEOUT=120 PIP_RETRIES=10 pip install --no-cache-dir -r requirements.txt
COPY app.py model_config.py runtime.py .

ENV HF_HOME=/models/cache
EXPOSE 8188
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8188"]
