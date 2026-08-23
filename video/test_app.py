import time

import pytest
from fastapi.testclient import TestClient

import app as runtime


@pytest.fixture(autouse=True)
def isolated_runtime(tmp_path, monkeypatch):
    monkeypatch.setattr(runtime, "DATA_ROOT", tmp_path)
    monkeypatch.setattr(runtime, "video_ready", True)
    monkeypatch.setattr(runtime, "video_status", "ready")
    monkeypatch.setattr(runtime, "video_error", "")
    monkeypatch.setattr(runtime, "select_device", lambda: ("cuda", "cuda"))
    monkeypatch.setattr(runtime, "allocated_memory_bytes", lambda: 0)


def wait_for_status(client, job_id, expected):
    deadline = time.time() + 2
    while time.time() < deadline:
        payload = client.get(f"/v1/videos/{job_id}").json()
        if payload["status"] in expected:
            return payload
        time.sleep(0.01)
    raise AssertionError(f"job did not reach {expected}")


def test_job_transitions_and_content_are_durable(monkeypatch):
    def fake_generate(_state, output, _cancelled):
        output.write_bytes(b"video-bytes")

    monkeypatch.setattr(runtime, "generate_video", fake_generate)
    client = TestClient(runtime.app)
    created = client.post("/v1/videos", json={"prompt": "a sunrise"}).json()
    assert created["status"] == "queued"
    assert "prompt" not in created
    completed = wait_for_status(client, created["id"], {"completed"})
    assert completed["progress"] == 100
    content = client.get(f"/v1/videos/{created['id']}/content")
    assert content.status_code == 200
    assert content.content == b"video-bytes"
    assert "prompt" not in runtime.read_state(created["id"])


def test_failure_is_persisted_without_local_paths(monkeypatch):
    monkeypatch.setattr(runtime, "generate_video", lambda *_args: (_ for _ in ()).throw(RuntimeError("pipeline failed at /private/model")))
    client = TestClient(runtime.app)
    created = client.post("/v1/videos", json={"prompt": "failure"}).json()
    failed = wait_for_status(client, created["id"], {"failed"})
    assert failed["error"]["code"] == "generation_failed"
    assert "output" not in failed
    assert client.get(f"/v1/videos/{created['id']}/content").status_code == 409


def test_cancelled_job_does_not_publish_content(monkeypatch):
    def blocking_generate(_state, output, cancelled):
        assert cancelled.wait(1)
        output.write_bytes(b"must-not-publish")

    monkeypatch.setattr(runtime, "generate_video", blocking_generate)
    client = TestClient(runtime.app)
    created = client.post("/v1/videos", json={"prompt": "cancel"}).json()
    wait_for_status(client, created["id"], {"in_progress"})
    cancelled = client.delete(f"/v1/videos/{created['id']}")
    assert cancelled.json()["status"] == "cancelled"
    time.sleep(0.05)
    assert client.get(f"/v1/videos/{created['id']}/content").status_code == 409


def test_restart_recovers_incomplete_jobs(monkeypatch):
    state = {"id": "vid_recover", "object": "video", "model": runtime.DEFAULT_MODEL, "prompt": "recover", "seconds": "2", "size": runtime.DEFAULT_SIZE, "status": "in_progress", "progress": 50, "created_at": 1}
    runtime.write_state(state)
    started = []
    monkeypatch.setattr(runtime, "start_job", started.append)
    runtime.recover_jobs()
    assert started == ["vid_recover"]
    assert runtime.read_state("vid_recover")["status"] == "queued"


def test_validation_and_output_limit(monkeypatch):
    client = TestClient(runtime.app)
    assert client.post("/v1/videos", json={"prompt": "x", "size": "4096x4096"}).status_code == 422
    assert client.post("/v1/videos", json={"prompt": "x", "seconds": runtime.MAX_SECONDS + 1}).status_code == 422

    monkeypatch.setattr(runtime, "MAX_OUTPUT_BYTES", 4)
    monkeypatch.setattr(runtime, "generate_video", lambda _state, output, _cancelled: output.write_bytes(b"12345"))
    created = client.post("/v1/videos", json={"prompt": "large"}).json()
    assert wait_for_status(client, created["id"], {"failed"})["status"] == "failed"
