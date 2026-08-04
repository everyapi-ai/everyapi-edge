"""Local Diffusers runtime for EveryAPI Edge image generation and editing.

The service intentionally has no public port in Compose. The local control room
and the Edge request forwarder are its only callers; the agent discovers models
through /health.
"""

import base64
import io
import os
import time
from functools import lru_cache
from pathlib import Path
from threading import Lock

from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse
from PIL import Image
from pydantic import BaseModel, Field

from model_config import (
    DEFAULT_GENERATION_MODEL,
    DEFAULT_MODEL,
    SUPPORTED_GENERATION_MODELS,
    active_model,
    select_model,
)
from runtime import DeviceUnavailableError, select_device

app = FastAPI(title="EveryAPI Edge Diffusers Runtime")
CONFIG_PATH = Path(os.getenv("EVERYAPI_DIFFUSERS_CONFIG_PATH", "/models/cache/everyapi-image-runtime.json"))
STARTUP_MODEL = os.getenv("EVERYAPI_DIFFUSERS_MODEL", DEFAULT_MODEL)
runtime_lock = Lock()
generation_ready = False
generation_error = "generation model is still loading"
SUPPORTED_SIZES = {"512x512": (512, 512), "1024x1024": (1024, 1024)}
MINIMUM_EDITOR_MEMORY_GB = 48


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


def available_models():
    models = [DEFAULT_GENERATION_MODEL]
    if editing_enabled():
        models.append(selected_model())
    return sorted(models)


@lru_cache(maxsize=1)
def generation_pipeline(model_id: str):
    import torch
    from diffusers import SanaPipeline

    device = select_device()
    loaded = SanaPipeline.from_pretrained(
        model_id,
        variant="fp16",
        torch_dtype=torch.float16,
    ).to(device.name)
    stable_dtype = torch.float32
    if device.name == "cuda" and torch.cuda.is_bf16_supported():
        stable_dtype = torch.bfloat16
    loaded.text_encoder.to(dtype=stable_dtype)
    loaded.vae.to(dtype=stable_dtype)
    if device.name == "mps":
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
    global generation_error, generation_ready
    try:
        generation_pipeline(DEFAULT_GENERATION_MODEL)
    except Exception as error:
        generation_ready = False
        generation_error = str(error)
        return
    generation_ready = True
    generation_error = ""


@app.on_event("startup")
def startup_preload():
    preload_generation_model()


def _png_base64(image: Image.Image) -> str:
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    return base64.b64encode(buffer.getvalue()).decode("ascii")


@app.get("/health")
def health():
    try:
        device = select_device()
    except DeviceUnavailableError as error:
        return JSONResponse(
            status_code=503,
            content={"status": "unavailable", "models": [], "error": str(error)},
        )
    if not generation_ready:
        return JSONResponse(
            status_code=503,
            content={"status": "unavailable", "models": [], "error": generation_error},
        )
    return {
        "status": "ready",
        "device": device.name,
        "backend": device.backend,
        "models": available_models(),
    }


@app.post("/v1/models/select")
def select_image_editor(selection: ModelSelection):
    if not editing_enabled():
        raise HTTPException(
            status_code=503,
            detail=f"image editing requires at least {MINIMUM_EDITOR_MEMORY_GB} GiB",
        )
    try:
        with runtime_lock:
            model = select_model(CONFIG_PATH, selection.model)
            edit_pipeline.cache_clear()
    except ValueError as error:
        raise HTTPException(status_code=422, detail=str(error)) from error
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
    try:
        with runtime_lock:
            images = generation_pipeline(request.model)(
                prompt=request.prompt,
                width=width,
                height=height,
                num_images_per_prompt=request.n,
            ).images
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
    active = selected_model()
    if model != active:
        raise HTTPException(status_code=404, detail="model is not the active image editor")
    try:
        source = Image.open(io.BytesIO(await image.read())).convert("RGB")
        with runtime_lock:
            output = edit_pipeline(active)(image=[source], prompt=prompt).images[0]
    except (DeviceUnavailableError, RuntimeError) as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    encoded = _png_base64(output)
    return {
        "created": int(time.time()),
        "model": active,
        "b64_json": encoded,
        "data": [{"b64_json": encoded}],
    }
