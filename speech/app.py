"""Local Kokoro runtime for EveryAPI Edge speech synthesis.

The service has no public port in Compose. The local control room and the Edge request forwarder are its only callers; the agent discovers models through /health.

Only text-to-speech is served. Transcription is deliberately absent because this runtime bundles synthesis models only.
"""

import io
import os
from contextlib import asynccontextmanager
from functools import lru_cache
from threading import Lock, Thread

import numpy as np
import soundfile as sf
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse, Response
from pydantic import BaseModel, Field

from model_config import (
    CONTENT_TYPES,
    DEFAULT_MODEL,
    DEFAULT_RESPONSE_FORMAT,
    DEFAULT_VOICE,
    MAX_SPEED,
    MIN_SPEED,
    SAMPLE_RATE,
    SOUNDFILE_FORMATS,
    SUPPORTED_MODELS,
    SUPPORTED_RESPONSE_FORMATS,
    SUPPORTED_VOICES,
    WARMUP_PHRASES,
    language_for_voice,
    resolve_voice,
    voice_for_language,
)
from runtime import allocated_memory_bytes, select_device

MAX_INPUT_CHARACTERS = 4096
RUNTIME_VERSION = os.getenv("EVERYAPI_SPEECH_RUNTIME_VERSION", "1.0.0")


@asynccontextmanager
async def lifespan(_app: FastAPI):
    worker = Thread(target=preload, name="speech-runtime-warmup", daemon=True)
    worker.start()
    try:
        yield
    finally:
        if not worker.is_alive():
            pipeline.cache_clear()
            model.cache_clear()


app = FastAPI(title="EveryAPI Edge Speech Runtime", lifespan=lifespan)
runtime_lock = Lock()
speech_ready = False
speech_error = "speech model is still loading"
speech_status = "starting"


class SpeechRequest(BaseModel):
    input: str = Field(min_length=1, max_length=MAX_INPUT_CHARACTERS)
    model: str = DEFAULT_MODEL
    voice: str = DEFAULT_VOICE
    response_format: str = DEFAULT_RESPONSE_FORMAT
    speed: float = Field(default=1.0, ge=MIN_SPEED, le=MAX_SPEED)


@lru_cache(maxsize=1)
def model():
    """One KModel holds the weights; every language pipeline shares it.

    Constructing a KPipeline without an explicit model loads a second copy of the network per language, which would multiply resident memory for no gain.
    """
    from kokoro import KModel

    device = select_device()
    return KModel(repo_id=DEFAULT_MODEL).to(device.name).eval()


@lru_cache(maxsize=8)
def pipeline(language: str):
    from kokoro import KPipeline

    return KPipeline(lang_code=language, repo_id=DEFAULT_MODEL, model=model())


def preload():
    """Warm every advertised language and voice so /health only reports ready once the node can serve any request it advertises.

    Kokoro downloads a voice tensor the first time that voice is requested, so a node that only warmed its default would make whichever buyer asks for another voice first pay the download — and fail outright if the host cannot reach Hugging Face at that moment, which is how the first live validation of this runtime failed. Each locale is synthesised once to build its G2P frontend, then every allow-listed voice tensor is fetched.
    """
    global speech_error, speech_ready, speech_status
    speech_ready = False
    speech_status = "warming"
    speech_error = "speech model is still loading"
    try:
        with runtime_lock:
            for language, phrase in sorted(WARMUP_PHRASES.items()):
                synthesize(voice_for_language(language), phrase, 1.0)
            for voice in sorted(SUPPORTED_VOICES):
                pipeline(language_for_voice(voice)).load_voice(voice)
    except Exception as error:  # noqa: BLE001 — surfaced verbatim through /health
        speech_ready = False
        speech_error = str(error)
        speech_status = "degraded"
        return
    speech_ready = True
    speech_error = ""
    speech_status = "ready"


def speech_capability(status="ready", reason=""):
    capability = {
        "id": "audio.tts",
        "status": status,
        "models": [DEFAULT_MODEL],
        "paths": ["/v1/audio/speech"],
        "limits": {
            "max_input_characters": MAX_INPUT_CHARACTERS,
            "formats": sorted(SUPPORTED_RESPONSE_FORMATS),
        },
    }
    if reason:
        capability["reason"] = reason
    return capability


def synthesize(voice: str, text: str, speed: float) -> np.ndarray:
    """Return mono float32 samples at SAMPLE_RATE for the whole input."""
    import torch

    segments = list(pipeline(language_for_voice(voice))(text, voice=voice, speed=speed))
    chunks = [segment.audio for segment in segments if segment.audio is not None]
    if not chunks:
        raise RuntimeError("the speech model produced no audio for that input")
    return torch.cat(chunks).detach().cpu().numpy().astype(np.float32)


def encode(samples: np.ndarray, response_format: str) -> bytes:
    if response_format == "pcm":
        clipped = np.clip(samples, -1.0, 1.0)
        return (clipped * 32767.0).astype("<i2").tobytes()
    buffer = io.BytesIO()
    sf.write(buffer, samples, SAMPLE_RATE, format=SOUNDFILE_FORMATS[response_format])
    return buffer.getvalue()


@app.get("/health")
def health():
    device = select_device()
    if not speech_ready:
        return JSONResponse(
            status_code=503,
            content={
                "status": speech_status,
                "version": RUNTIME_VERSION,
                "device": device.name,
                "backend": device.backend,
                "vram_bytes": allocated_memory_bytes(device),
                "models": [DEFAULT_MODEL],
                "capabilities": [speech_capability(speech_status, speech_error)],
                "error": speech_error,
            },
        )
    return {
        "status": "ready",
        "version": RUNTIME_VERSION,
        "device": device.name,
        "backend": device.backend,
        "vram_bytes": allocated_memory_bytes(device),
        "models": [DEFAULT_MODEL],
        "capabilities": [speech_capability()],
    }


@app.post("/v1/audio/speech")
def create_speech(request: SpeechRequest):
    if request.model not in SUPPORTED_MODELS:
        raise HTTPException(status_code=404, detail="speech model is not supported")
    if request.response_format not in SUPPORTED_RESPONSE_FORMATS:
        raise HTTPException(
            status_code=422,
            detail=f"response_format must be one of {sorted(SUPPORTED_RESPONSE_FORMATS)}",
        )
    try:
        voice = resolve_voice(request.voice)
    except ValueError as error:
        raise HTTPException(status_code=422, detail=str(error)) from error
    if not speech_ready:
        raise HTTPException(status_code=503, detail=speech_error)

    try:
        with runtime_lock:
            samples = synthesize(voice, request.input, request.speed)
    except RuntimeError as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    return Response(
        content=encode(samples, request.response_format),
        media_type=CONTENT_TYPES[request.response_format],
    )
