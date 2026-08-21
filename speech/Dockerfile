FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime

WORKDIR /app
# espeak-ng is Kokoro's fallback phonemiser for words its lexicon does not cover. Without it, synthesis fails on ordinary out-of-vocabulary input.
RUN apt-get update \
    && apt-get install -y --no-install-recommends espeak-ng \
    && rm -rf /var/lib/apt/lists/*
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
# misaki's English G2P loads this spaCy pipeline lazily and pip-installs it on first use. Baking it in keeps the container from reaching the network while a buyer request is already in flight.
RUN python -m spacy download en_core_web_sm
COPY app.py model_config.py runtime.py .

ENV HF_HOME=/models/cache
EXPOSE 8189
CMD ["fastapi", "run", "app.py", "--host", "0.0.0.0", "--port", "8189"]
