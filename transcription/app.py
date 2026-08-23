"""Local Whisper transcription and translation runtime for EveryAPI Edge."""

import asyncio
import os
import tempfile
from contextlib import asynccontextmanager, suppress
from functools import lru_cache
from pathlib import Path
from threading import Event, Lock, Thread

import numpy as np
import soundfile as sf
from fastapi import FastAPI, File, Form, HTTPException, Request, UploadFile
from fastapi.responses import JSONResponse, PlainTextResponse

from model_config import (
    DEFAULT_MODEL,
    MAX_AUDIO_BYTES,
    MAX_PROMPT_CHARACTERS,
    SUPPORTED_CONTENT_TYPES,
    SUPPORTED_EXTENSIONS,
    SUPPORTED_LANGUAGES,
    SUPPORTED_MODELS,
    SUPPORTED_RESPONSE_FORMATS,
)

RUNTIME_VERSION = os.getenv("EVERYAPI_TRANSCRIPTION_RUNTIME_VERSION", "1.0.0")
runtime_lock = Lock()
transcription_ready = False
transcription_status = "starting"
transcription_error = "transcription model is still loading"


@asynccontextmanager
async def lifespan(_app: FastAPI):
    worker = Thread(target=preload, name="transcription-runtime-warmup", daemon=True)
    worker.start()
    yield


app = FastAPI(title="EveryAPI Edge Transcription Runtime", lifespan=lifespan)


def select_device() -> tuple[str, int | str]:
    import torch

    if torch.cuda.is_available():
        return "cuda", 0
    if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
        return "mps", "mps"
    return "cpu", -1


@lru_cache(maxsize=1)
def transcriber():
    from transformers import pipeline

    _, device = select_device()
    return pipeline("automatic-speech-recognition", model=DEFAULT_MODEL, device=device)


def allocated_memory_bytes() -> int:
    import torch

    if torch.cuda.is_available():
        return int(torch.cuda.memory_allocated())
    if hasattr(torch, "mps") and torch.backends.mps.is_available():
        return int(torch.mps.current_allocated_memory())
    return 0


def cancellation_criteria(cancelled: Event):
    from transformers import StoppingCriteria, StoppingCriteriaList

    class RequestCancelled(StoppingCriteria):
        def __call__(self, _input_ids, _scores, **_kwargs):
            return cancelled.is_set()

    return StoppingCriteriaList([RequestCancelled()])


def infer(path: str, task: str, language: str | None, prompt: str, temperature: float, cancelled: Event):
    generate_kwargs: dict[str, object] = {
        "task": task,
        "temperature": temperature,
        "stopping_criteria": cancellation_criteria(cancelled),
    }
    if language:
        generate_kwargs["language"] = language
    if prompt:
        generate_kwargs["prompt"] = prompt
    return transcriber()(path, generate_kwargs=generate_kwargs, return_timestamps=True)


def preload():
    global transcription_error, transcription_ready, transcription_status
    transcription_ready = False
    transcription_status = "warming"
    transcription_error = "transcription model is still loading"
    warmup_path = ""
    try:
        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as handle:
            warmup_path = handle.name
        sf.write(warmup_path, np.zeros(16_000, dtype=np.float32), 16_000)
        with runtime_lock:
            infer(warmup_path, "transcribe", "en", "", 0.0, Event())
    except Exception as error:  # noqa: BLE001 — surfaced through local-only health
        transcription_status = "degraded"
        transcription_error = str(error)
        return
    finally:
        if warmup_path:
            with suppress(OSError):
                os.remove(warmup_path)
    transcription_ready = True
    transcription_status = "ready"
    transcription_error = ""


def capability(capability_id: str, path: str, status: str, reason: str = ""):
    item = {
        "id": capability_id,
        "status": status,
        "models": [DEFAULT_MODEL],
        "paths": [path],
        "limits": {
            "max_input_bytes": MAX_AUDIO_BYTES,
            "formats": sorted(SUPPORTED_EXTENSIONS),
            "languages": sorted(SUPPORTED_LANGUAGES),
        },
    }
    if reason:
        item["reason"] = reason
    return item


@app.get("/health")
def health():
    device, _ = select_device()
    content = {
        "status": transcription_status,
        "version": RUNTIME_VERSION,
        "device": device,
        "vram_bytes": allocated_memory_bytes(),
        "models": [DEFAULT_MODEL],
        "capabilities": [
            capability("audio.transcription", "/v1/audio/transcriptions", transcription_status, transcription_error),
            capability("audio.translation", "/v1/audio/translations", transcription_status, transcription_error),
        ],
    }
    if not transcription_ready:
        content["error"] = transcription_error
        return JSONResponse(status_code=503, content=content)
    return content


async def persist_upload(upload: UploadFile) -> str:
    suffix = Path(upload.filename or "").suffix.lower().lstrip(".")
    if suffix not in SUPPORTED_EXTENSIONS:
        raise HTTPException(status_code=422, detail="audio file format is not supported")
    content_type = (upload.content_type or "").lower()
    if content_type and content_type not in SUPPORTED_CONTENT_TYPES and content_type != "application/octet-stream":
        raise HTTPException(status_code=422, detail="audio content type is not supported")
    handle = tempfile.NamedTemporaryFile(suffix=f".{suffix}", delete=False)
    path = handle.name
    total = 0
    try:
        while chunk := await upload.read(1024 * 1024):
            total += len(chunk)
            if total > MAX_AUDIO_BYTES:
                raise HTTPException(status_code=413, detail="audio file exceeds 25 MiB")
            handle.write(chunk)
        if total == 0:
            raise HTTPException(status_code=422, detail="audio file is empty")
        handle.close()
        return path
    except Exception:
        handle.close()
        with suppress(OSError):
            os.remove(path)
        raise
    finally:
        await upload.close()


async def recognize(request: Request, upload: UploadFile, model: str, language: str | None, prompt: str, response_format: str, temperature: float, task: str):
    if model not in SUPPORTED_MODELS:
        raise HTTPException(status_code=404, detail="transcription model is not supported")
    if language and language not in SUPPORTED_LANGUAGES:
        raise HTTPException(status_code=422, detail="language is not supported")
    if len(prompt) > MAX_PROMPT_CHARACTERS:
        raise HTTPException(status_code=422, detail=f"prompt exceeds {MAX_PROMPT_CHARACTERS} characters")
    if response_format not in SUPPORTED_RESPONSE_FORMATS:
        raise HTTPException(status_code=422, detail="response_format is not supported")
    if temperature < 0 or temperature > 1:
        raise HTTPException(status_code=422, detail="temperature must be between 0 and 1")
    if not transcription_ready:
        raise HTTPException(status_code=503, detail=transcription_error)

    path = await persist_upload(upload)
    cancelled = Event()
    finished = Event()

    async def monitor_disconnect():
        while not finished.is_set():
            if await request.is_disconnected():
                cancelled.set()
                return
            await asyncio.sleep(0.05)

    monitor = asyncio.create_task(monitor_disconnect())
    try:
        with runtime_lock:
            result = await asyncio.to_thread(infer, path, task, language, prompt, temperature, cancelled)
        if cancelled.is_set():
            raise HTTPException(status_code=499, detail="request was cancelled")
    finally:
        finished.set()
        monitor.cancel()
        with suppress(asyncio.CancelledError):
            await monitor
        with suppress(OSError):
            os.remove(path)

    text = str(result.get("text", "")).strip()
    if response_format == "text":
        return PlainTextResponse(text)
    if response_format == "verbose_json":
        segments = []
        for index, chunk in enumerate(result.get("chunks") or []):
            timestamp = chunk.get("timestamp") or (0.0, 0.0)
            segments.append({"id": index, "start": timestamp[0] or 0.0, "end": timestamp[1] or 0.0, "text": str(chunk.get("text", "")).strip()})
        return {"task": task, "language": language or "", "text": text, "segments": segments}
    return {"text": text}


@app.post("/v1/audio/transcriptions")
async def create_transcription(request: Request, file: UploadFile = File(...), model: str = Form(DEFAULT_MODEL), language: str | None = Form(None), prompt: str = Form(""), response_format: str = Form("json"), temperature: float = Form(0.0)):
    return await recognize(request, file, model, language, prompt, response_format, temperature, "transcribe")


@app.post("/v1/audio/translations")
async def create_translation(request: Request, file: UploadFile = File(...), model: str = Form(DEFAULT_MODEL), prompt: str = Form(""), response_format: str = Form("json"), temperature: float = Form(0.0)):
    return await recognize(request, file, model, None, prompt, response_format, temperature, "translate")
