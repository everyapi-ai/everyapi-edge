"""Durable asynchronous video runtime for EveryAPI Edge."""

import json
import os
import secrets
import subprocess
import tempfile
import time
from contextlib import asynccontextmanager, suppress
from functools import lru_cache
from pathlib import Path
from threading import Event, Lock, Thread

from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse, JSONResponse
from pydantic import BaseModel, Field

from model_config import (
    DEFAULT_MODEL,
    DEFAULT_MODEL_REVISION,
    DEFAULT_SECONDS,
    DEFAULT_SIZE,
    FRAMES_PER_SECOND,
    MAX_OUTPUT_BYTES,
    MAX_PROMPT_CHARACTERS,
    MAX_SECONDS,
    MIN_SECONDS,
    SUPPORTED_MODELS,
    SUPPORTED_SIZES,
)

RUNTIME_VERSION = os.getenv("EVERYAPI_VIDEO_RUNTIME_VERSION", "1.0.0")
DATA_ROOT = Path(os.getenv("EVERYAPI_VIDEO_DATA_PATH", "/models/cache/jobs"))
runtime_lock = Lock()
state_lock = Lock()
cancel_events: dict[str, Event] = {}
video_ready = False
video_status = "starting"
video_error = "video model is still loading"


class VideoRequest(BaseModel):
    prompt: str = Field(min_length=1, max_length=MAX_PROMPT_CHARACTERS)
    model: str = DEFAULT_MODEL
    seconds: int = Field(default=DEFAULT_SECONDS, ge=MIN_SECONDS, le=MAX_SECONDS)
    size: str = DEFAULT_SIZE


@asynccontextmanager
async def lifespan(_app: FastAPI):
    DATA_ROOT.mkdir(parents=True, exist_ok=True)
    recover_jobs()
    Thread(target=preload, name="video-runtime-warmup", daemon=True).start()
    yield


app = FastAPI(title="EveryAPI Edge Video Runtime", lifespan=lifespan)


def select_device():
    import torch

    if torch.cuda.is_available():
        return "cuda", "rocm" if getattr(torch.version, "hip", None) else "cuda"
    if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
        return "mps", "mps"
    raise RuntimeError("a CUDA, ROCm, or Apple MPS accelerator is required")


def allocated_memory_bytes() -> int:
    import torch

    device, _ = select_device()
    if device == "cuda":
        return int(torch.cuda.memory_reserved())
    return int(torch.mps.current_allocated_memory())


@lru_cache(maxsize=1)
def video_pipeline():
    import torch
    from diffusers import DiffusionPipeline

    device, _ = select_device()
    loaded = DiffusionPipeline.from_pretrained(DEFAULT_MODEL, revision=DEFAULT_MODEL_REVISION, torch_dtype=torch.float16)
    if device == "cuda":
        loaded.enable_model_cpu_offload()
        return loaded
    if device == "mps":
        loaded.enable_attention_slicing()
    return loaded.to(device)


def preload():
    global video_error, video_ready, video_status
    video_status = "warming"
    try:
        with runtime_lock:
            video_pipeline()
    except Exception as error:  # noqa: BLE001 — surfaced through local-only health
        video_ready = False
        video_status = "degraded"
        video_error = str(error)
        return
    video_ready = True
    video_status = "ready"
    video_error = ""


def job_dir(job_id: str) -> Path:
    return DATA_ROOT / job_id


def state_path(job_id: str) -> Path:
    return job_dir(job_id) / "state.json"


def read_state(job_id: str) -> dict:
    if not job_id.startswith("vid_"):
        raise HTTPException(status_code=404, detail="video job was not found")
    try:
        with state_path(job_id).open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except (OSError, json.JSONDecodeError) as error:
        raise HTTPException(status_code=404, detail="video job was not found") from error


def write_state(state: dict):
    directory = job_dir(state["id"])
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / f"state.{secrets.token_hex(8)}.tmp"
    with temporary.open("x", encoding="utf-8") as handle:
        json.dump(state, handle, separators=(",", ":"))
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, state_path(state["id"]))


def public_state(state: dict) -> dict:
    result = {key: state[key] for key in ("id", "object", "model", "status", "progress", "created_at")}
    for key in ("completed_at", "error", "seconds", "size"):
        if key in state:
            result[key] = state[key]
    return result


def recover_jobs():
    if not DATA_ROOT.exists():
        return
    for path in DATA_ROOT.glob("vid_*/state.json"):
        try:
            with path.open("r", encoding="utf-8") as handle:
                state = json.load(handle)
            if state.get("status") in {"queued", "in_progress"}:
                state["status"] = "queued"
                state["progress"] = 0
                write_state(state)
                start_job(state["id"])
        except (OSError, KeyError, json.JSONDecodeError):
            continue


def start_job(job_id: str):
    cancelled = Event()
    with state_lock:
        cancel_events[job_id] = cancelled
    Thread(target=run_job, args=(job_id, cancelled), name=f"video-{job_id}", daemon=True).start()


def generate_video(state: dict, output_path: Path, cancelled: Event):
    from PIL import Image

    if cancelled.is_set():
        return
    width, height = SUPPORTED_SIZES[state["size"]]
    frames = video_pipeline()(
        prompt=state["prompt"],
        width=width,
        height=height,
        num_frames=state["seconds"] * FRAMES_PER_SECOND + 1,
        num_inference_steps=30,
    ).frames[0]
    if cancelled.is_set():
        return
    with tempfile.TemporaryDirectory(prefix="frames-", dir=job_dir(state["id"])) as frames_dir:
        for index, frame in enumerate(frames):
            if cancelled.is_set():
                return
            if hasattr(frame, "save"):
                image = frame
            else:
                if hasattr(frame, "detach"):
                    frame = frame.detach().cpu().numpy()
                if "float" in str(getattr(frame, "dtype", "")):
                    frame = (frame * 255).round().clip(0, 255).astype("uint8")
                image = Image.fromarray(frame)
            image.save(Path(frames_dir) / f"frame-{index:05d}.png", format="PNG")
        subprocess.run(
            [
                "ffmpeg",
                "-hide_banner",
                "-loglevel",
                "error",
                "-y",
                "-framerate",
                str(FRAMES_PER_SECOND),
                "-i",
                str(Path(frames_dir) / "frame-%05d.png"),
                "-c:v",
                "libx264",
                "-pix_fmt",
                "yuv420p",
                "-movflags",
                "+faststart",
                str(output_path),
            ],
            check=True,
            timeout=180,
        )


def run_job(job_id: str, cancelled: Event):
    try:
        with state_lock:
            state = read_state(job_id)
            if cancelled.is_set() or state.get("status") == "cancelled":
                return
            state["status"] = "in_progress"
            state["progress"] = 10
            write_state(state)
        temporary = job_dir(job_id) / f"output.{secrets.token_hex(8)}.tmp.mp4"
        with runtime_lock:
            generate_video(state, temporary, cancelled)
        if cancelled.is_set():
            with suppress(OSError):
                temporary.unlink()
            return
        if not temporary.is_file() or temporary.stat().st_size <= 0 or temporary.stat().st_size > MAX_OUTPUT_BYTES:
            raise RuntimeError("video output is empty or exceeds 64 MiB")
        os.replace(temporary, job_dir(job_id) / "output.mp4")
        with state_lock:
            state = read_state(job_id)
            if cancelled.is_set() or state.get("status") == "cancelled":
                with suppress(OSError):
                    (job_dir(job_id) / "output.mp4").unlink()
                return
            state["status"] = "completed"
            state["progress"] = 100
            state["completed_at"] = int(time.time())
            state.pop("prompt", None)
            write_state(state)
    except Exception as error:  # noqa: BLE001 — persisted as a bounded task failure
        with suppress(HTTPException):
            with state_lock:
                state = read_state(job_id)
                if state.get("status") != "cancelled":
                    state["status"] = "failed"
                    state["error"] = {"code": "generation_failed", "message": "video generation failed"}
                    state["completed_at"] = int(time.time())
                    state.pop("prompt", None)
                    write_state(state)
        for temporary in job_dir(job_id).glob("output.*.tmp.mp4"):
            with suppress(OSError):
                temporary.unlink()
    finally:
        with state_lock:
            cancel_events.pop(job_id, None)


@app.get("/health")
def health():
    try:
        device, backend = select_device()
    except RuntimeError as error:
        return JSONResponse(status_code=503, content={"status": "unavailable", "version": RUNTIME_VERSION, "models": [], "capabilities": [video_capability("unavailable", str(error))], "error": str(error), "vram_bytes": 0})
    content = {"status": video_status, "version": RUNTIME_VERSION, "device": device, "backend": backend, "models": [DEFAULT_MODEL], "capabilities": [video_capability(video_status, video_error)], "vram_bytes": allocated_memory_bytes()}
    if not video_ready:
        content["error"] = video_error
        return JSONResponse(status_code=503, content=content)
    return content


def video_capability(status: str, reason: str = ""):
    item = {"id": "video.generate", "status": status, "models": [DEFAULT_MODEL], "paths": ["/v1/videos"], "limits": {"max_input_characters": MAX_PROMPT_CHARACTERS, "formats": ["mp4"]}}
    if reason:
        item["reason"] = reason
    return item


@app.post("/v1/videos")
def create_video(request: VideoRequest):
    if not video_ready:
        raise HTTPException(status_code=503, detail=video_error)
    if request.model not in SUPPORTED_MODELS:
        raise HTTPException(status_code=404, detail="video model is not supported")
    if request.size not in SUPPORTED_SIZES:
        raise HTTPException(status_code=422, detail="video size is not supported")
    job_id = "vid_" + secrets.token_urlsafe(18)
    state = {"id": job_id, "object": "video", "model": request.model, "prompt": request.prompt, "seconds": str(request.seconds), "size": request.size, "status": "queued", "progress": 0, "created_at": int(time.time())}
    write_state(state)
    start_job(job_id)
    return public_state(state)


@app.get("/v1/videos/{job_id}")
def get_video(job_id: str):
    return public_state(read_state(job_id))


@app.delete("/v1/videos/{job_id}")
def cancel_video(job_id: str):
    with state_lock:
        state = read_state(job_id)
        if state["status"] in {"completed", "failed", "cancelled"}:
            return public_state(state)
        cancelled = cancel_events.get(job_id)
        if cancelled is not None:
            cancelled.set()
        state["status"] = "cancelled"
        state["progress"] = 0
        state["completed_at"] = int(time.time())
        state.pop("prompt", None)
        write_state(state)
    return public_state(state)


@app.get("/v1/videos/{job_id}/content")
def video_content(job_id: str):
    state = read_state(job_id)
    if state["status"] != "completed":
        raise HTTPException(status_code=409, detail="video job is not completed")
    output = job_dir(job_id) / "output.mp4"
    if not output.is_file() or output.stat().st_size <= 0 or output.stat().st_size > MAX_OUTPUT_BYTES:
        raise HTTPException(status_code=410, detail="video content is unavailable")
    return FileResponse(output, media_type="video/mp4", filename=f"{job_id}.mp4")
