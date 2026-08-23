"""Local Kokoro and Whisper runtime for EveryAPI Edge audio APIs.

The service has no public port in Compose. The local control room and the Edge request forwarder are its only callers; the agent discovers models through /health.

"""

import gc
import io
import os
import subprocess
from contextlib import asynccontextmanager
from functools import lru_cache
from threading import Lock, Thread

import numpy as np
import soundfile as sf
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse, PlainTextResponse, Response
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
from runtime import allocated_memory_bytes, select_device, select_tts_device

MAX_INPUT_CHARACTERS = 4096
# Leave room for multipart fields inside the Edge protocol's 32 MiB request-body ceiling.
MAX_AUDIO_BYTES = 30 * 1024 * 1024
MAX_AUDIO_SECONDS = 30 * 60
MAX_ASR_PROMPT_CHARACTERS = 4096
MAX_LANGUAGE_CHARACTERS = 64
ASR_SAMPLE_RATE = 16000
ASR_MODEL = "openai/whisper-large-v3-turbo"
ASR_RESPONSE_FORMATS = {"json", "text", "verbose_json", "srt", "vtt"}
RUNTIME_VERSION = os.getenv("EVERYAPI_SPEECH_RUNTIME_VERSION", "1.0.0")


@asynccontextmanager
async def lifespan(_app: FastAPI):
    workers = [Thread(target=preload, name="speech-runtime-warmup", daemon=True), Thread(target=preload_asr, name="asr-runtime-warmup", daemon=True)]
    for worker in workers:
        worker.start()
    try:
        yield
    finally:
        if not any(worker.is_alive() for worker in workers):
            pipeline.cache_clear()
            model.cache_clear()


app = FastAPI(title="EveryAPI Edge Speech Runtime", lifespan=lifespan)
tts_lock = Lock()
asr_lock = Lock()
speech_ready = False
speech_error = "speech model is still loading"
speech_status = "starting"
asr_ready = False
asr_error = "speech recognition model is still loading"
asr_status = "starting"


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

    device = select_tts_device()
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
        with tts_lock:
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


def preload_asr():
    global asr_error, asr_ready, asr_status
    asr_ready = False
    asr_status = "warming"
    asr_error = "speech recognition model is still loading"
    try:
        from huggingface_hub import snapshot_download

        snapshot_download(repo_id=ASR_MODEL)
    except Exception as error:  # noqa: BLE001 — surfaced verbatim through /health
        asr_error = str(error)
        asr_status = "degraded"
        return
    asr_ready = True
    asr_error = ""
    asr_status = "ready"


def speech_capability(status="ready", reason=""):
    capability = {
        "id": "audio.tts",
        "status": status,
        "models": [DEFAULT_MODEL],
        "paths": ["/v1/audio/speech"],
        "limits": {
            "max_input_characters": MAX_INPUT_CHARACTERS,
            "formats": sorted(SUPPORTED_RESPONSE_FORMATS),
            "voices": sorted(SUPPORTED_VOICES),
            "languages": sorted(WARMUP_PHRASES),
        },
    }
    if reason:
        capability["reason"] = reason
    return capability


def asr_capability(capability_id: str, path: str, status="ready", reason=""):
    capability = {"id": capability_id, "status": status, "models": [ASR_MODEL], "paths": [path], "limits": {"max_input_bytes": MAX_AUDIO_BYTES, "formats": sorted(ASR_RESPONSE_FORMATS)}}
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


def decode_audio(payload: bytes) -> np.ndarray:
    try:
        completed = subprocess.run(["ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-t", str(MAX_AUDIO_SECONDS + 1), "-f", "f32le", "-ac", "1", "-ar", str(ASR_SAMPLE_RATE), "pipe:1"], input=payload, capture_output=True, check=False, timeout=120)
    except subprocess.TimeoutExpired as error:
        raise ValueError("audio decoding timed out") from error
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError(detail or "audio could not be decoded")
    samples = np.frombuffer(completed.stdout, dtype="<f4").copy()
    if samples.size == 0:
        raise ValueError("audio contains no samples")
    if samples.size / ASR_SAMPLE_RATE > MAX_AUDIO_SECONDS:
        raise ValueError(f"audio duration exceeds {MAX_AUDIO_SECONDS} seconds")
    return samples


def transcribe(samples: np.ndarray, *, task: str, language: str | None = None, prompt: str | None = None, temperature: float = 0.0, timestamps: bool = False):
    import torch
    from transformers import AutoModelForSpeechSeq2Seq, AutoProcessor
    from transformers import pipeline as transformers_pipeline

    device = select_device()
    torch_dtype = torch.float16 if device.name == "cuda" else torch.float32
    asr_model = None
    processor = None
    recognizer = None
    try:
        asr_model = AutoModelForSpeechSeq2Seq.from_pretrained(ASR_MODEL, torch_dtype=torch_dtype, low_cpu_mem_usage=True, use_safetensors=True, local_files_only=True)
        if device.name == "cuda":
            asr_model.to("cuda:0")
            pipeline_device = 0
        else:
            asr_model.to(device.name)
            pipeline_device = device.name if device.name == "mps" else -1
        processor = AutoProcessor.from_pretrained(ASR_MODEL, local_files_only=True)
        recognizer = transformers_pipeline("automatic-speech-recognition", model=asr_model, tokenizer=processor.tokenizer, feature_extractor=processor.feature_extractor, chunk_length_s=30, batch_size=8, torch_dtype=torch_dtype, device=pipeline_device)
        generate_kwargs = {"task": task}
        if language:
            generate_kwargs["language"] = language
        if prompt:
            generate_kwargs["prompt_ids"] = processor.get_prompt_ids(prompt, return_tensors="pt").to(asr_model.device)
        if temperature > 0:
            generate_kwargs.update({"temperature": temperature, "do_sample": True})
        return recognizer(samples, return_timestamps=timestamps, generate_kwargs=generate_kwargs)
    finally:
        recognizer = None
        processor = None
        asr_model = None
        gc.collect()
        if device.name == "cuda":
            torch.cuda.empty_cache()


def timestamp(value: float, separator: str) -> str:
    milliseconds = max(0, round(value * 1000))
    hours, remainder = divmod(milliseconds, 3_600_000)
    minutes, remainder = divmod(remainder, 60_000)
    seconds, millis = divmod(remainder, 1000)
    return f"{hours:02d}:{minutes:02d}:{seconds:02d}{separator}{millis:03d}"


def render_asr(result, response_format: str, task: str, language: str | None, duration: float):
    text = result.get("text", "").strip()
    chunks = result.get("chunks", [])
    if response_format == "json":
        return JSONResponse({"text": text})
    if response_format == "text":
        return PlainTextResponse(text)
    segments = [{"id": index, "start": float(chunk.get("timestamp", (0, 0))[0] or 0), "end": float(chunk.get("timestamp", (0, 0))[1] or duration), "text": chunk.get("text", "").strip()} for index, chunk in enumerate(chunks)]
    if response_format == "verbose_json":
        return JSONResponse({"task": task, "language": language or "auto", "duration": duration, "text": text, "segments": segments})
    separator = "," if response_format == "srt" else "."
    lines = ["WEBVTT", ""] if response_format == "vtt" else []
    for index, segment in enumerate(segments, start=1):
        if response_format == "srt":
            lines.append(str(index))
        lines.extend([f"{timestamp(segment['start'], separator)} --> {timestamp(segment['end'], separator)}", segment["text"], ""])
    return PlainTextResponse("\n".join(lines), media_type="text/vtt" if response_format == "vtt" else "application/x-subrip")


@app.get("/health")
def health():
    device = select_device()
    capabilities = [speech_capability("ready" if speech_ready else speech_status, "" if speech_ready else speech_error), asr_capability("audio.transcription", "/v1/audio/transcriptions", "ready" if asr_ready else asr_status, "" if asr_ready else asr_error), asr_capability("audio.translation", "/v1/audio/translations", "ready" if asr_ready else asr_status, "" if asr_ready else asr_error)]
    models = ([DEFAULT_MODEL] if speech_ready else []) + ([ASR_MODEL] if asr_ready else [])
    if not speech_ready and not asr_ready:
        return JSONResponse(
            status_code=503,
            content={
                "status": "degraded" if speech_status == "degraded" or asr_status == "degraded" else "warming",
                "version": RUNTIME_VERSION,
                "device": device.name,
                "backend": device.backend,
                "vram_bytes": allocated_memory_bytes(device),
                "models": models,
                "capabilities": capabilities,
                "error": "; ".join(filter(None, [speech_error, asr_error])),
            },
        )
    return {
        "status": "ready" if speech_ready and asr_ready else "degraded",
        "version": RUNTIME_VERSION,
        "device": device.name,
        "backend": device.backend,
        "vram_bytes": allocated_memory_bytes(device),
        "models": models,
        "capabilities": capabilities,
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
        with tts_lock:
            samples = synthesize(voice, request.input, request.speed)
    except RuntimeError as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    return Response(
        content=encode(samples, request.response_format),
        media_type=CONTENT_TYPES[request.response_format],
    )


def create_asr(file: UploadFile, model_name: str, response_format: str, language: str | None, prompt: str | None, temperature: float, task: str):
    if model_name != ASR_MODEL:
        raise HTTPException(status_code=404, detail="speech recognition model is not supported")
    if response_format not in ASR_RESPONSE_FORMATS:
        raise HTTPException(status_code=422, detail=f"response_format must be one of {sorted(ASR_RESPONSE_FORMATS)}")
    if temperature < 0 or temperature > 1:
        raise HTTPException(status_code=422, detail="temperature must be between 0 and 1")
    if prompt and len(prompt) > MAX_ASR_PROMPT_CHARACTERS:
        raise HTTPException(status_code=422, detail=f"prompt exceeds {MAX_ASR_PROMPT_CHARACTERS} characters")
    if language and len(language) > MAX_LANGUAGE_CHARACTERS:
        raise HTTPException(status_code=422, detail=f"language exceeds {MAX_LANGUAGE_CHARACTERS} characters")
    if not asr_ready:
        raise HTTPException(status_code=503, detail=asr_error)
    payload = file.file.read(MAX_AUDIO_BYTES + 1)
    if len(payload) > MAX_AUDIO_BYTES:
        raise HTTPException(status_code=413, detail=f"audio exceeds {MAX_AUDIO_BYTES} bytes")
    try:
        samples = decode_audio(payload)
        with asr_lock:
            result = transcribe(samples, task=task, language=language, prompt=prompt, temperature=temperature, timestamps=response_format in {"verbose_json", "srt", "vtt"})
    except (RuntimeError, ValueError, OSError) as error:
        raise HTTPException(status_code=422 if isinstance(error, ValueError) else 503, detail=str(error)) from error
    return render_asr(result, response_format, task, language, samples.size / ASR_SAMPLE_RATE)


@app.post("/v1/audio/transcriptions")
def create_transcription(file: UploadFile = File(...), model: str = Form(ASR_MODEL), response_format: str = Form("json"), language: str | None = Form(None), prompt: str | None = Form(None), temperature: float = Form(0.0)):
    return create_asr(file, model, response_format, language, prompt, temperature, "transcribe")


@app.post("/v1/audio/translations")
def create_translation(file: UploadFile = File(...), model: str = Form(ASR_MODEL), response_format: str = Form("json"), language: str | None = Form(None), prompt: str | None = Form(None), temperature: float = Form(0.0)):
    return create_asr(file, model, response_format, language, prompt, temperature, "translate")
