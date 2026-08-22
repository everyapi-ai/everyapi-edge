"""Local Diffusers runtime for EveryAPI Edge image generation and editing.

The service intentionally has no public port in Compose. The local control room
and the Edge request forwarder are its only callers; the agent discovers models
through /health.
"""

import base64
import io
import os
import time
from contextlib import asynccontextmanager
from functools import lru_cache
from pathlib import Path
from threading import Lock, Thread

from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse
from PIL import Image
from pydantic import BaseModel, Field

from model_config import (
    DEFAULT_GENERATION_MODEL,
    DEFAULT_MODEL,
    SUPPORTED_GENERATION_MODELS,
    SUPPORTED_IMAGE_EDITORS,
    active_model,
    select_model,
)
from runtime import DeviceUnavailableError, allocated_memory_bytes, select_device


@asynccontextmanager
async def lifespan(_app: FastAPI):
    global editor_error, editor_ready, editor_status
    global resident_editor_model, resident_editor_pipeline
    worker = Thread(
        target=preload_generation_model,
        name="image-runtime-warmup",
        daemon=True,
    )
    worker.start()
    try:
        yield
    finally:
        if not worker.is_alive():
            with runtime_lock:
                generation_pipeline.cache_clear()
                edit_pipeline.cache_clear()
                with editor_state_lock:
                    resident_editor_model = None
                    resident_editor_pipeline = None
                    editor_ready = False
                    editor_status = "stopped"
                    editor_error = "editor runtime is stopped"


app = FastAPI(title="EveryAPI Edge Diffusers Runtime", lifespan=lifespan)
CONFIG_PATH = Path(os.getenv("EVERYAPI_DIFFUSERS_CONFIG_PATH", "/models/cache/everyapi-image-runtime.json"))
STARTUP_MODEL = os.getenv("EVERYAPI_DIFFUSERS_MODEL", DEFAULT_MODEL)
runtime_lock = Lock()
editor_state_lock = Lock()
generation_ready = False
generation_error = "generation model is still loading"
generation_status = "starting"
editor_ready = False
editor_error = "editor model is still loading"
editor_status = "starting"
resident_editor_model = None
resident_editor_pipeline = None
SUPPORTED_SIZES = {"512x512": (512, 512), "1024x1024": (1024, 1024)}
MINIMUM_EDITOR_MEMORY_GB = 48
MAX_REQUEST_BODY_BYTES = 32 * 1024 * 1024
RUNTIME_VERSION = os.getenv("EVERYAPI_IMAGE_RUNTIME_VERSION", "1.0.0")


class ModelSelection(BaseModel):
    model: str


class ImageGenerationRequest(BaseModel):
    prompt: str = Field(min_length=1, max_length=4000)
    model: str = DEFAULT_GENERATION_MODEL
    n: int = Field(default=1, ge=1, le=1)
    size: str = "1024x1024"
    response_format: str = "b64_json"


def selected_model() -> str:
    return active_model(CONFIG_PATH, STARTUP_MODEL)


def editing_enabled() -> bool:
    try:
        return int(os.getenv("EVERYAPI_VRAM_GB", "0")) >= MINIMUM_EDITOR_MEMORY_GB
    except ValueError:
        return False


def editor_runtime_snapshot():
    with editor_state_lock:
        ready = editor_ready and resident_editor_model is not None and resident_editor_pipeline is not None
        if ready:
            return {
                "ready": True,
                "status": "ready",
                "error": "",
                "model": resident_editor_model,
            }
        status = editor_status
        error = editor_error
        if editor_ready:
            status = "degraded"
            error = "the active image editor is not resident"
        return {"ready": False, "status": status, "error": error, "model": None}


def available_models(editor_snapshot=None):
    snapshot = editor_snapshot or editor_runtime_snapshot()
    models = []
    if generation_ready:
        models.append(DEFAULT_GENERATION_MODEL)
    if editing_enabled() and snapshot["ready"]:
        models.append(snapshot["model"])
    return sorted(models)


def runtime_capabilities(status=None, reason="", editor_snapshot=None):
    snapshot = editor_snapshot or editor_runtime_snapshot()
    current_generation_status = status or ("ready" if generation_ready else generation_status)
    current_generation_reason = reason or generation_error
    generation = {
        "id": "image.generate",
        "status": current_generation_status,
        "models": [DEFAULT_GENERATION_MODEL],
        "paths": ["/v1/images/generations"],
        "limits": {"max_input_bytes": MAX_REQUEST_BODY_BYTES},
    }
    if current_generation_status != "ready" and current_generation_reason:
        generation["reason"] = current_generation_reason
    capabilities = [generation]
    if editing_enabled():
        current_editor_status = status or snapshot["status"]
        current_editor_reason = reason or snapshot["error"]
        editing = {
            "id": "image.edit",
            "status": current_editor_status,
            "models": [snapshot["model"]] if snapshot["ready"] else [],
            "paths": ["/v1/images/edits"],
            "limits": {"max_input_bytes": MAX_REQUEST_BODY_BYTES},
        }
        if current_editor_status != "ready" and current_editor_reason:
            editing["reason"] = current_editor_reason
        capabilities.append(editing)
    else:
        capabilities.append(
            {
                "id": "image.edit",
                "status": "unsupported",
                "models": [],
                "paths": ["/v1/images/edits"],
                "reason": f"image editing requires at least {MINIMUM_EDITOR_MEMORY_GB} GiB",
                "limits": {"max_input_bytes": MAX_REQUEST_BODY_BYTES},
            }
        )
    return capabilities


@lru_cache(maxsize=1)
def generation_pipeline(model_id: str):
    import torch
    from diffusers import SanaPipeline

    device = select_device()
    loaded = SanaPipeline.from_pretrained(
        model_id,
        variant="fp16",
        torch_dtype=torch.float16,
    )
    stable_dtype = torch.float32
    if device.name == "cuda" and torch.cuda.is_bf16_supported():
        stable_dtype = torch.bfloat16
    loaded.text_encoder.to(dtype=stable_dtype)
    loaded.vae.to(dtype=stable_dtype)
    if device.name == "cuda":
        loaded.enable_model_cpu_offload()
    else:
        loaded.to(device.name)
        loaded.enable_attention_slicing()
    return loaded


@lru_cache(maxsize=1)
def edit_pipeline(model_id: str):
    import torch
    from diffusers import QwenImageEditPlusPipeline

    device = select_device()
    return QwenImageEditPlusPipeline.from_pretrained(
        model_id, torch_dtype=torch.float16
    ).to(device.name)


def preload_generation_model():
    global editor_error, editor_ready, editor_status
    global generation_error, generation_ready, generation_status
    global resident_editor_model, resident_editor_pipeline
    generation_ready = False
    generation_status = "warming"
    generation_error = "generation model is still loading"
    try:
        generation_pipeline(DEFAULT_GENERATION_MODEL)
    except Exception as error:
        generation_ready = False
        generation_error = str(error)
        generation_status = "degraded"
    else:
        generation_ready = True
        generation_error = ""
        generation_status = "ready"
    if not editing_enabled():
        with runtime_lock:
            with editor_state_lock:
                resident_editor_model = None
                resident_editor_pipeline = None
                editor_ready = False
                editor_error = f"image editing requires at least {MINIMUM_EDITOR_MEMORY_GB} GiB"
                editor_status = "unsupported"
        return
    with runtime_lock:
        with editor_state_lock:
            resident_editor_model = None
            resident_editor_pipeline = None
            editor_ready = False
            editor_status = "warming"
            editor_error = "editor model is still loading"
        try:
            model = selected_model()
            pipeline = edit_pipeline(model)
        except Exception as error:
            with editor_state_lock:
                editor_error = str(error)
                editor_status = "degraded"
            return
        with editor_state_lock:
            resident_editor_model = model
            resident_editor_pipeline = pipeline
            editor_ready = True
            editor_error = ""
            editor_status = "ready"


def _png_base64(image: Image.Image) -> str:
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    return base64.b64encode(buffer.getvalue()).decode("ascii")


@app.get("/health")
def health():
    try:
        device = select_device()
    except DeviceUnavailableError as error:
        editor_snapshot = editor_runtime_snapshot()
        return JSONResponse(
            status_code=503,
            content={
                "status": "unavailable",
                "version": RUNTIME_VERSION,
                "vram_bytes": 0,
                "models": [],
                "capabilities": runtime_capabilities("unavailable", str(error), editor_snapshot),
                "error": str(error),
            },
        )
    editor_snapshot = editor_runtime_snapshot()
    if not generation_ready:
        return JSONResponse(
            status_code=503,
            content={
                "status": generation_status,
                "version": RUNTIME_VERSION,
                "device": device.name,
                "backend": device.backend,
                "vram_bytes": allocated_memory_bytes(device),
                "models": available_models(editor_snapshot),
                "capabilities": runtime_capabilities(editor_snapshot=editor_snapshot),
                "error": generation_error,
            },
        )
    runtime_status = "ready"
    runtime_error = ""
    if editing_enabled() and not editor_snapshot["ready"]:
        runtime_status = editor_snapshot["status"]
        runtime_error = editor_snapshot["error"]
    payload = {
        "status": runtime_status,
        "version": RUNTIME_VERSION,
        "device": device.name,
        "backend": device.backend,
        "vram_bytes": allocated_memory_bytes(device),
        "models": available_models(editor_snapshot),
        "capabilities": runtime_capabilities(editor_snapshot=editor_snapshot),
    }
    if runtime_error:
        payload["error"] = runtime_error
    return payload


@app.post("/v1/models/select")
def select_image_editor(selection: ModelSelection):
    global editor_error, editor_ready, editor_status
    global resident_editor_model, resident_editor_pipeline
    if not editing_enabled():
        raise HTTPException(
            status_code=503,
            detail=f"image editing requires at least {MINIMUM_EDITOR_MEMORY_GB} GiB",
        )
    if selection.model not in SUPPORTED_IMAGE_EDITORS:
        raise HTTPException(
            status_code=422,
            detail="that image editor is not supported by this runtime",
        )
    with runtime_lock:
        previous_model = resident_editor_model
        previous_pipeline = resident_editor_pipeline
        previous_ready = editor_ready and previous_model is not None and previous_pipeline is not None
        try:
            candidate_pipeline = edit_pipeline(selection.model)
        except (DeviceUnavailableError, RuntimeError, OSError, ValueError) as error:
            raise HTTPException(status_code=503, detail=str(error)) from error
        try:
            model = select_model(CONFIG_PATH, selection.model)
        except (OSError, ValueError) as error:
            edit_pipeline.cache_clear()
            with editor_state_lock:
                if previous_ready:
                    resident_editor_pipeline = previous_pipeline
                    resident_editor_model = previous_model
                    editor_ready = True
                    editor_status = "ready"
                    editor_error = ""
                else:
                    resident_editor_pipeline = None
                    resident_editor_model = None
                    editor_ready = False
                    editor_status = "degraded"
                    editor_error = str(error)
            raise HTTPException(status_code=503, detail=str(error)) from error
        with editor_state_lock:
            resident_editor_pipeline = candidate_pipeline
            resident_editor_model = model
            editor_ready = True
            editor_error = ""
            editor_status = "ready"
    return {"status": "ready", "models": [model]}


@app.post("/v1/images/generations")
def generate_image(request: ImageGenerationRequest):
    if request.model not in SUPPORTED_GENERATION_MODELS:
        raise HTTPException(status_code=404, detail="generation model is not supported")
    if request.size not in SUPPORTED_SIZES:
        raise HTTPException(status_code=422, detail="size must be 512x512 or 1024x1024")
    if request.response_format != "b64_json":
        raise HTTPException(status_code=422, detail="only b64_json responses are supported")
    if not generation_ready:
        raise HTTPException(status_code=503, detail=generation_error)

    width, height = SUPPORTED_SIZES[request.size]
    loaded = generation_pipeline(request.model)
    try:
        with runtime_lock:
            try:
                images = loaded(
                    prompt=request.prompt,
                    width=width,
                    height=height,
                    num_images_per_prompt=request.n,
                ).images
            finally:
                if hasattr(loaded, "maybe_free_model_hooks"):
                    loaded.maybe_free_model_hooks()
    except (DeviceUnavailableError, RuntimeError) as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    return {
        "created": int(time.time()),
        "model": request.model,
        "data": [{"b64_json": _png_base64(image)} for image in images],
    }


@app.post("/v1/images/edits")
async def edit_image(
    image: UploadFile = File(...), prompt: str = Form(...), model: str = Form(DEFAULT_MODEL)
):
    if not editing_enabled():
        raise HTTPException(
            status_code=503,
            detail=f"image editing requires at least {MINIMUM_EDITOR_MEMORY_GB} GiB",
        )
    try:
        source = Image.open(io.BytesIO(await image.read())).convert("RGB")
        with runtime_lock:
            if not editor_ready:
                raise HTTPException(status_code=503, detail=editor_error)
            active = resident_editor_model
            if active is None or resident_editor_pipeline is None:
                raise HTTPException(status_code=503, detail="the active image editor is not resident")
            if model != active:
                raise HTTPException(status_code=404, detail="model is not the active image editor")
            output = resident_editor_pipeline(image=[source], prompt=prompt).images[0]
    except (DeviceUnavailableError, RuntimeError) as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    encoded = _png_base64(output)
    return {
        "created": int(time.time()),
        "model": active,
        "b64_json": encoded,
        "data": [{"b64_json": encoded}],
    }
