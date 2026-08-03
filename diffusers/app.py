"""Small local Diffusers runtime for EveryAPI Edge image editing.

The service intentionally has no public port in Compose. Edge's local control
room is its sole caller, and the agent discovers it via /health.
"""

import base64
import io
import os
from functools import lru_cache
from pathlib import Path
from threading import Lock

from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from PIL import Image
from pydantic import BaseModel

from model_config import DEFAULT_MODEL, active_model, select_model

app = FastAPI(title="EveryAPI Edge Diffusers Runtime")
CONFIG_PATH = Path(os.getenv("EVERYAPI_DIFFUSERS_CONFIG_PATH", "/models/cache/everyapi-image-runtime.json"))
STARTUP_MODEL = os.getenv("EVERYAPI_DIFFUSERS_MODEL", DEFAULT_MODEL)
runtime_lock = Lock()


class ModelSelection(BaseModel):
    model: str


def selected_model() -> str:
    return active_model(CONFIG_PATH, STARTUP_MODEL)


@lru_cache(maxsize=1)
def pipeline(model_id: str):
    import torch
    from diffusers import QwenImageEditPlusPipeline

    if not torch.cuda.is_available():
        raise RuntimeError("CUDA is required for the configured Qwen image editing runtime")
    return QwenImageEditPlusPipeline.from_pretrained(
        model_id, torch_dtype=torch.bfloat16
    ).to("cuda")


@app.get("/health")
def health():
    return {"status": "ready", "models": [selected_model()]}


@app.post("/v1/models/select")
def select_image_editor(selection: ModelSelection):
    try:
        with runtime_lock:
            model = select_model(CONFIG_PATH, selection.model)
            pipeline.cache_clear()
    except ValueError as error:
        raise HTTPException(status_code=422, detail=str(error)) from error
    return {"status": "ready", "models": [model]}


@app.post("/v1/images/edits")
async def edit_image(
    image: UploadFile = File(...), prompt: str = Form(...), model: str = Form(DEFAULT_MODEL)
):
    active = selected_model()
    if model != active:
        raise HTTPException(status_code=404, detail="model is not the active image editor")
    try:
        source = Image.open(io.BytesIO(await image.read())).convert("RGB")
        with runtime_lock:
            output = pipeline(active)(image=[source], prompt=prompt).images[0]
    except RuntimeError as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    buffer = io.BytesIO()
    output.save(buffer, format="PNG")
    return {"model": active, "b64_json": base64.b64encode(buffer.getvalue()).decode("ascii")}
