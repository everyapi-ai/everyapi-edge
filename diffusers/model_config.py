"""Persistent, allow-listed image editor selection for the local runtime."""

import json
import os
import tempfile
from pathlib import Path


DEFAULT_MODEL = "Qwen/Qwen-Image-Edit-2511"
SUPPORTED_IMAGE_EDITORS = frozenset(
    {
        "Qwen/Qwen-Image-Edit",
        "Qwen/Qwen-Image-Edit-2509",
        "Qwen/Qwen-Image-Edit-2511",
    }
)


def _validated(model: str) -> str:
    if model not in SUPPORTED_IMAGE_EDITORS:
        raise ValueError("that image editor is not supported by this runtime")
    return model


def active_model(config_path: Path, fallback: str = DEFAULT_MODEL) -> str:
    fallback = fallback if fallback in SUPPORTED_IMAGE_EDITORS else DEFAULT_MODEL
    try:
        payload = json.loads(config_path.read_text(encoding="utf-8"))
    except (FileNotFoundError, OSError, ValueError):
        return fallback
    configured = payload.get("model") if isinstance(payload, dict) else None
    return configured if isinstance(configured, str) and configured in SUPPORTED_IMAGE_EDITORS else fallback


def select_model(config_path: Path, model: str) -> str:
    model = _validated(model)
    config_path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".image-runtime-", dir=str(config_path.parent))
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump({"model": model}, output)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, config_path)
    except Exception:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise
    return model
