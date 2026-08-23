FROM python:3.12-slim

WORKDIR /app
COPY requirements.txt .
RUN PIP_DEFAULT_TIMEOUT=120 PIP_RETRIES=10 pip install --no-cache-dir -r requirements.txt
COPY app.py templates.py .

EXPOSE 8192
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8192"]
