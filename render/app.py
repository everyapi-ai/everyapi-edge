"""Allow-listed ComfyUI workflow adapter for EveryAPI Edge."""

import json
import os
import re
import secrets
import time
from contextlib import asynccontextmanager, suppress
from pathlib import Path
from threading import Event, Lock, Thread
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urljoin
from urllib.request import Request, urlopen

from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse, JSONResponse
from pydantic import BaseModel, ConfigDict, Field

from templates import TemplateError, load_templates

RUNTIME_VERSION = os.getenv("EVERYAPI_RENDER_RUNTIME_VERSION", "1.0.0")
COMFYUI_URL = os.getenv("EVERYAPI_COMFYUI_URL", "http://host.docker.internal:8188").rstrip("/") + "/"
WORKFLOW_ROOT = Path(os.getenv("EVERYAPI_RENDER_WORKFLOW_PATH", "/workflows"))
DATA_ROOT = Path(os.getenv("EVERYAPI_RENDER_DATA_PATH", "/data"))
MAX_OUTPUT_BYTES = 64 * 1024 * 1024
MAX_JSON_BYTES = 2 * 1024 * 1024
JOB_ID = re.compile(r"^render_[A-Za-z0-9_-]{20,64}$")
OUTPUT_TYPES = {"input", "output", "temp"}
OUTPUT_CONTENT_TYPES = {"application/octet-stream", "image/gif", "image/jpeg", "image/png", "image/webp", "video/mp4", "video/webm"}
templates_lock = Lock()
state_lock = Lock()
cancel_events: dict[str, Event] = {}


class RenderRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    model: str = Field(min_length=1, max_length=64)
    parameters: dict = Field(default_factory=dict)


@asynccontextmanager
async def lifespan(_app: FastAPI):
    DATA_ROOT.mkdir(parents=True, exist_ok=True)
    recover_jobs()
    yield


app = FastAPI(title="EveryAPI Edge Render Runtime", lifespan=lifespan)


def request_json(method: str, path: str, payload=None, timeout=10):
    body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    request = Request(urljoin(COMFYUI_URL, path.lstrip("/")), data=body, method=method, headers={"Content-Type": "application/json"})
    try:
        with urlopen(request, timeout=timeout) as response:
            body = response.read(MAX_JSON_BYTES + 1)
            if len(body) > MAX_JSON_BYTES:
                raise RuntimeError("ComfyUI returned an invalid response")
            result = json.loads(body)
            if not isinstance(result, dict):
                raise RuntimeError("ComfyUI returned an invalid response")
            return result
    except (HTTPError, URLError, TimeoutError) as error:
        raise RuntimeError("ComfyUI is unavailable") from error
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise RuntimeError("ComfyUI returned an invalid response") from error


def installed_templates():
    with templates_lock:
        return load_templates(WORKFLOW_ROOT)


def job_dir(job_id: str):
    return DATA_ROOT / job_id


def state_path(job_id: str):
    return job_dir(job_id) / "state.json"


def write_state(state: dict):
    directory = job_dir(state["id"])
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / f"state.{secrets.token_hex(8)}.tmp"
    with temporary.open("x", encoding="utf-8") as handle:
        json.dump(state, handle, separators=(",", ":"))
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, state_path(state["id"]))


def read_state(job_id: str):
    if not JOB_ID.fullmatch(job_id):
        raise HTTPException(status_code=404, detail="render job was not found")
    try:
        return json.loads(state_path(job_id).read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise HTTPException(status_code=404, detail="render job was not found") from error


def public_state(state: dict):
    result = {key: state[key] for key in ("id", "object", "template", "status", "progress", "created_at")}
    for key in ("completed_at", "error", "outputs"):
        if key in state:
            result[key] = state[key]
    return result


def recover_jobs():
    if not DATA_ROOT.is_dir():
        return
    for path in DATA_ROOT.glob("render_*/state.json"):
        try:
            state = json.loads(path.read_text(encoding="utf-8"))
            if state.get("status") in {"queued", "in_progress"}:
                start_job(state["id"])
        except (OSError, json.JSONDecodeError, KeyError):
            continue


def cleanup_outputs(job_id: str):
    for output in job_dir(job_id).glob("output-*.bin"):
        with suppress(OSError):
            output.unlink()


def start_job(job_id: str):
    cancelled = Event()
    with state_lock:
        cancel_events[job_id] = cancelled
    Thread(target=watch_job, args=(job_id, cancelled), name=f"render-{job_id}", daemon=True).start()


def download_output(job_id: str, index: int, descriptor: dict):
    allowed = {"filename", "subfolder", "type"}
    if not isinstance(descriptor, dict) or set(descriptor) - allowed or not isinstance(descriptor.get("filename"), str):
        raise RuntimeError("ComfyUI returned an invalid output descriptor")
    filename = descriptor["filename"]
    subfolder = descriptor.get("subfolder", "")
    kind = descriptor.get("type", "output")
    if kind not in OUTPUT_TYPES or any(value.startswith(("/", "\\")) or ".." in Path(value).parts or "\x00" in value for value in (filename, subfolder)):
        raise RuntimeError("ComfyUI returned an unsafe output descriptor")
    request = Request(urljoin(COMFYUI_URL, "view?") + urlencode({"filename": filename, "subfolder": subfolder, "type": kind}), method="GET")
    target = job_dir(job_id) / f"output-{index}.bin"
    temporary = job_dir(job_id) / f"output-{index}.{secrets.token_hex(8)}.tmp"
    total = 0
    try:
        with urlopen(request, timeout=60) as response, temporary.open("xb") as handle:
            content_type = response.headers.get_content_type()
            if content_type not in OUTPUT_CONTENT_TYPES:
                content_type = "application/octet-stream"
            while chunk := response.read(1 << 20):
                total += len(chunk)
                if total > MAX_OUTPUT_BYTES:
                    raise RuntimeError("render output exceeds 64 MiB")
                handle.write(chunk)
            handle.flush()
            os.fsync(handle.fileno())
        if total == 0:
            raise RuntimeError("render output is empty")
        os.replace(temporary, target)
        return {"index": index, "content_type": content_type, "bytes": total}
    finally:
        with suppress(OSError):
            temporary.unlink()


def watch_job(job_id: str, cancelled: Event):
    try:
        with state_lock:
            state = read_state(job_id)
            if state.get("status") == "cancelled" or cancelled.is_set():
                return
            state["status"] = "in_progress"
            state["progress"] = 10
            write_state(state)
        deadline = time.monotonic() + 30 * 60
        while time.monotonic() < deadline and not cancelled.wait(1):
            history = request_json("GET", f"history/{state['prompt_id']}", timeout=10)
            item = history.get(state["prompt_id"])
            if not item:
                continue
            status = item.get("status", {})
            if status.get("status_str") == "error":
                raise RuntimeError("ComfyUI workflow failed")
            outputs = item.get("outputs", {})
            descriptors = []
            template = installed_templates().get(state["template"])
            if template is None:
                raise RuntimeError("render template was removed")
            for output in template.outputs:
                node_output = outputs.get(output["node"], {})
                values = node_output.get(output["key"], [])
                descriptors.extend(values if isinstance(values, list) else [])
            if not descriptors:
                continue
            downloaded = [download_output(job_id, index, descriptor) for index, descriptor in enumerate(descriptors[:8])]
            with state_lock:
                latest = read_state(job_id)
                if latest.get("status") == "cancelled" or cancelled.is_set():
                    cleanup_outputs(job_id)
                    return
                latest["outputs"] = downloaded
                latest["status"] = "completed"
                latest["progress"] = 100
                latest["completed_at"] = int(time.time())
                latest.pop("prompt_id", None)
                write_state(latest)
            return
        if cancelled.is_set():
            return
        raise RuntimeError("render job timed out")
    except Exception:  # noqa: BLE001 — public job errors stay generic
        with suppress(HTTPException):
            with state_lock:
                state = read_state(job_id)
                if state.get("status") != "cancelled":
                    state["status"] = "failed"
                    state["error"] = {"code": "render_failed", "message": "render workflow failed"}
                    state["completed_at"] = int(time.time())
                    state.pop("prompt_id", None)
                    write_state(state)
                    cleanup_outputs(job_id)
    finally:
        with state_lock:
            cancel_events.pop(job_id, None)


@app.get("/health")
def health():
    templates = installed_templates()
    try:
        request_json("GET", "system_stats", timeout=3)
    except RuntimeError as error:
        return JSONResponse(status_code=503, content={"status": "unavailable", "version": RUNTIME_VERSION, "models": sorted(templates), "capabilities": [render_capability("unavailable", str(error), templates)], "error": str(error), "vram_bytes": 0})
    if not templates:
        reason = "no render templates are installed"
        return JSONResponse(status_code=503, content={"status": "unsupported", "version": RUNTIME_VERSION, "models": [], "capabilities": [render_capability("unsupported", reason, templates)], "error": reason, "vram_bytes": 0})
    return {"status": "ready", "version": RUNTIME_VERSION, "models": sorted(templates), "capabilities": [render_capability("ready", "", templates)], "vram_bytes": 0}


def render_capability(status: str, reason: str, templates: dict):
    item = {"id": "render.execute", "status": status, "models": sorted(templates), "paths": ["/v1/render/jobs"], "limits": {"max_input_bytes": 1 << 20, "formats": ["binary"]}}
    if reason:
        item["reason"] = reason
    return item


@app.post("/v1/render/jobs")
def create_render(request: RenderRequest):
    if len(request.parameters) > 64:
        raise HTTPException(status_code=413, detail="render parameters exceed 64 fields")
    if len(json.dumps(request.parameters)) > 1 << 20:
        raise HTTPException(status_code=413, detail="render parameters exceed 1 MiB")
    template = installed_templates().get(request.model)
    if template is None:
        raise HTTPException(status_code=404, detail="render template is not installed")
    try:
        workflow = template.render(request.parameters)
        response = request_json("POST", "prompt", {"prompt": workflow}, timeout=10)
    except TemplateError as error:
        raise HTTPException(status_code=422, detail=str(error)) from error
    except RuntimeError as error:
        raise HTTPException(status_code=503, detail=str(error)) from error
    prompt_id = response.get("prompt_id")
    if not isinstance(prompt_id, str) or not prompt_id:
        raise HTTPException(status_code=502, detail="ComfyUI returned an invalid job id")
    job_id = "render_" + secrets.token_urlsafe(18)
    state = {"id": job_id, "object": "render.job", "template": template.id, "prompt_id": prompt_id, "status": "queued", "progress": 0, "created_at": int(time.time())}
    write_state(state)
    start_job(job_id)
    return public_state(state)


@app.get("/v1/render/jobs/{job_id}")
def get_render(job_id: str):
    return public_state(read_state(job_id))


@app.delete("/v1/render/jobs/{job_id}")
def cancel_render(job_id: str):
    state = read_state(job_id)
    if state["status"] in {"completed", "failed", "cancelled"}:
        return public_state(state)
    with state_lock:
        cancelled = cancel_events.get(job_id)
        if cancelled is not None:
            cancelled.set()
        state = read_state(job_id)
        if state["status"] in {"completed", "failed", "cancelled"}:
            return public_state(state)
        prompt_id = state.get("prompt_id")
        state["status"] = "cancelled"
        state["progress"] = 0
        state["completed_at"] = int(time.time())
        state.pop("prompt_id", None)
        write_state(state)
        cleanup_outputs(job_id)
    if prompt_id:
        with suppress(RuntimeError):
            request_json("POST", "queue", {"delete": [prompt_id]}, timeout=5)
    return public_state(state)


@app.get("/v1/render/jobs/{job_id}/content/{index}")
def render_content(job_id: str, index: int):
    state = read_state(job_id)
    if state["status"] != "completed":
        raise HTTPException(status_code=409, detail="render job is not completed")
    outputs = state.get("outputs", [])
    if index < 0 or index >= len(outputs):
        raise HTTPException(status_code=404, detail="render output was not found")
    output = job_dir(job_id) / f"output-{index}.bin"
    if not output.is_file() or output.stat().st_size != outputs[index]["bytes"]:
        raise HTTPException(status_code=410, detail="render output is unavailable")
    return FileResponse(output, media_type=outputs[index]["content_type"], filename=f"{job_id}-{index}")
