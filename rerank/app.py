"""Bounded local cross-encoder reranking runtime for EveryAPI Edge."""

import asyncio
import os
import secrets
from contextlib import asynccontextmanager, suppress
from functools import lru_cache
from threading import Event, Lock, Thread

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel, ConfigDict, Field, model_validator

DEFAULT_MODEL = "BAAI/bge-reranker-v2-m3"
MODEL_REVISION = "953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e"
MAX_DOCUMENTS = 100
MAX_QUERY_CHARACTERS = 8_000
MAX_DOCUMENT_CHARACTERS = 8_000
MAX_TOTAL_CHARACTERS = 128_000
RUNTIME_VERSION = os.getenv("EVERYAPI_RERANK_RUNTIME_VERSION", "1.0.0")
runtime_lock = Lock()
rerank_ready = False
rerank_status = "starting"
rerank_error = "rerank model is still loading"


class Document(BaseModel):
    model_config = ConfigDict(extra="forbid")

    text: str = Field(min_length=1, max_length=MAX_DOCUMENT_CHARACTERS)


class RerankRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    model: str
    query: str = Field(min_length=1, max_length=MAX_QUERY_CHARACTERS)
    documents: list[str | Document] = Field(min_length=1, max_length=MAX_DOCUMENTS)
    top_n: int | None = Field(default=None, ge=1, le=MAX_DOCUMENTS)
    return_documents: bool = False

    @model_validator(mode="after")
    def validate_documents(self):
        texts = [item if isinstance(item, str) else item.text for item in self.documents]
        if any(not text or len(text) > MAX_DOCUMENT_CHARACTERS for text in texts):
            raise ValueError("each document must contain between 1 and 8000 characters")
        if len(self.query) + sum(len(text) for text in texts) > MAX_TOTAL_CHARACTERS:
            raise ValueError("query and documents exceed 128000 characters")
        if self.top_n is not None and self.top_n > len(texts):
            raise ValueError("top_n cannot exceed the document count")
        return self


@asynccontextmanager
async def lifespan(_app: FastAPI):
    Thread(target=preload, name="rerank-runtime-warmup", daemon=True).start()
    yield


app = FastAPI(title="EveryAPI Edge Rerank Runtime", lifespan=lifespan)


def select_device():
    import torch

    if torch.cuda.is_available():
        return "cuda"
    if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
        return "mps"
    return "cpu"


@lru_cache(maxsize=1)
def tokenizer():
    from transformers import AutoTokenizer

    return AutoTokenizer.from_pretrained(DEFAULT_MODEL, revision=MODEL_REVISION, trust_remote_code=False)


@lru_cache(maxsize=1)
def model():
    from transformers import AutoModelForSequenceClassification

    loaded = AutoModelForSequenceClassification.from_pretrained(DEFAULT_MODEL, revision=MODEL_REVISION, trust_remote_code=False)
    loaded.to(select_device())
    loaded.eval()
    return loaded


def allocated_memory_bytes():
    import torch

    if torch.cuda.is_available():
        return int(torch.cuda.memory_allocated())
    if hasattr(torch, "mps") and torch.backends.mps.is_available():
        return int(torch.mps.current_allocated_memory())
    return 0


def score(query: str, documents: list[str], cancelled: Event):
    scores = []
    for start in range(0, len(documents), 8):
        if cancelled.is_set():
            raise RuntimeError("request cancelled")
        import torch

        batch = documents[start : start + 8]
        encoded = tokenizer()([[query, document] for document in batch], padding=True, truncation=True, max_length=512, return_tensors="pt")
        encoded = {key: value.to(select_device()) for key, value in encoded.items()}
        with torch.no_grad():
            logits = model()(**encoded).logits.reshape(-1).float()
            scores.extend(torch.sigmoid(logits).cpu().tolist())
    return scores


def score_serialized(query: str, documents: list[str], cancelled: Event):
    with runtime_lock:
        return score(query, documents, cancelled)


def preload():
    global rerank_error, rerank_ready, rerank_status
    rerank_ready = False
    rerank_status = "warming"
    rerank_error = "rerank model is still loading"
    try:
        with runtime_lock:
            score("warmup query", ["warmup document"], Event())
    except Exception as error:  # noqa: BLE001 — surfaced through local-only health
        rerank_status = "degraded"
        rerank_error = str(error)
        return
    rerank_ready = True
    rerank_status = "ready"
    rerank_error = ""


@app.get("/health")
def health():
    capability = {"id": "text.rerank", "status": rerank_status, "models": [DEFAULT_MODEL], "paths": ["/v1/rerank"], "limits": {"max_input_characters": MAX_TOTAL_CHARACTERS, "formats": ["text"]}}
    if rerank_error:
        capability["reason"] = rerank_error
    content = {"status": rerank_status, "version": RUNTIME_VERSION, "device": select_device(), "vram_bytes": allocated_memory_bytes(), "models": [DEFAULT_MODEL], "capabilities": [capability]}
    if not rerank_ready:
        content["error"] = rerank_error
        return JSONResponse(status_code=503, content=content)
    return content


@app.post("/v1/rerank")
async def rerank(request: Request, payload: RerankRequest):
    if payload.model != DEFAULT_MODEL:
        raise HTTPException(status_code=404, detail="rerank model is not supported")
    if not rerank_ready:
        raise HTTPException(status_code=503, detail=rerank_error)
    documents = [item if isinstance(item, str) else item.text for item in payload.documents]
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
        try:
            scores = await asyncio.to_thread(score_serialized, payload.query, documents, cancelled)
        except RuntimeError as error:
            if cancelled.is_set():
                raise HTTPException(status_code=499, detail="request was cancelled") from error
            raise
        if cancelled.is_set():
            raise HTTPException(status_code=499, detail="request was cancelled")
    finally:
        finished.set()
        monitor.cancel()
        with suppress(asyncio.CancelledError):
            await monitor
    ranked = sorted(enumerate(scores), key=lambda item: (-item[1], item[0]))[: payload.top_n or len(documents)]
    results = []
    for index, relevance in ranked:
        result = {"index": index, "relevance_score": relevance}
        if payload.return_documents:
            result["document"] = {"text": documents[index]}
        results.append(result)
    return {"id": "rerank_" + secrets.token_urlsafe(12), "object": "list", "results": results, "meta": {"billed_units": {"search_units": 1}}}
