from threading import Event

import pytest
from fastapi.testclient import TestClient

import app as runtime


@pytest.fixture(autouse=True)
def ready_runtime(monkeypatch):
    monkeypatch.setattr(runtime, "rerank_ready", True)
    monkeypatch.setattr(runtime, "rerank_status", "ready")
    monkeypatch.setattr(runtime, "rerank_error", "")
    monkeypatch.setattr(runtime, "select_device", lambda: "cpu")
    monkeypatch.setattr(runtime, "allocated_memory_bytes", lambda: 0)


def test_rerank_orders_scores_and_optionally_returns_documents(monkeypatch):
    monkeypatch.setattr(runtime, "score", lambda _query, _documents, _cancelled: [0.2, 0.9, 0.5])
    response = TestClient(runtime.app).post("/v1/rerank", json={"model": runtime.DEFAULT_MODEL, "query": "best", "documents": ["low", {"text": "high"}, "middle"], "top_n": 2, "return_documents": True})
    assert response.status_code == 200
    assert [item["index"] for item in response.json()["results"]] == [1, 2]
    assert response.json()["results"][0]["document"] == {"text": "high"}


def test_rerank_rejects_unknown_fields_and_bounds():
    client = TestClient(runtime.app)
    assert client.post("/v1/rerank", json={"model": runtime.DEFAULT_MODEL, "query": "q", "documents": ["d"], "workflow": {}}).status_code == 422
    assert client.post("/v1/rerank", json={"model": runtime.DEFAULT_MODEL, "query": "q", "documents": ["d"], "top_n": 2}).status_code == 422
    assert client.post("/v1/rerank", json={"model": runtime.DEFAULT_MODEL, "query": "q", "documents": ["x" * (runtime.MAX_DOCUMENT_CHARACTERS + 1)]}).status_code == 422
    assert client.post("/v1/rerank", json={"model": "unknown", "query": "q", "documents": ["d"]}).status_code == 404


def test_health_is_not_ready_until_warmup(monkeypatch):
    monkeypatch.setattr(runtime, "rerank_ready", False)
    monkeypatch.setattr(runtime, "rerank_status", "warming")
    monkeypatch.setattr(runtime, "rerank_error", "loading")
    response = TestClient(runtime.app).get("/health")
    assert response.status_code == 503
    assert response.json()["capabilities"][0]["id"] == "text.rerank"


def test_score_checks_cancellation_between_batches(monkeypatch):
    cancelled = Event()
    cancelled.set()
    with pytest.raises(RuntimeError, match="cancelled"):
        runtime.score("q", ["d"], cancelled)
