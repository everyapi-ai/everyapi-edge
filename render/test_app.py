import io
import json
import time
from email.message import Message
from threading import Event

import pytest
from fastapi.testclient import TestClient

import app as runtime


class FakeResponse(io.BytesIO):
    def __init__(self, body: bytes, content_type="application/json"):
        super().__init__(body)
        headers = Message()
        headers["Content-Type"] = content_type
        self.headers = headers

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        self.close()


@pytest.fixture(autouse=True)
def isolated_runtime(tmp_path, monkeypatch):
    workflow_root = tmp_path / "workflows"
    workflow_root.mkdir()
    template = {
        "id": "safe-render",
        "version": 1,
        "workflow": {"6": {"class_type": "CLIPTextEncode", "inputs": {"text": "default"}}, "9": {"class_type": "SaveImage", "inputs": {}}},
        "parameters": {"prompt": {"type": "string", "node": "6", "input": "text", "required": True}},
        "outputs": [{"node": "9", "key": "images"}],
    }
    (workflow_root / "safe.json").write_text(json.dumps(template), encoding="utf-8")
    monkeypatch.setattr(runtime, "WORKFLOW_ROOT", workflow_root)
    monkeypatch.setattr(runtime, "DATA_ROOT", tmp_path / "data")


def wait_for_status(client, job_id, expected):
    deadline = time.time() + 2
    while time.time() < deadline:
        payload = client.get(f"/v1/render/jobs/{job_id}").json()
        if payload["status"] in expected:
            return payload
        time.sleep(0.01)
    raise AssertionError(f"job did not reach {expected}")


def test_request_rejects_workflow_and_unknown_parameters(monkeypatch):
    monkeypatch.setattr(runtime, "request_json", lambda *_args, **_kwargs: {"prompt_id": "prompt-1"})
    client = TestClient(runtime.app)
    response = client.post("/v1/render/jobs", json={"model": "safe-render", "parameters": {"prompt": "safe"}, "workflow": {"1": {}}})
    assert response.status_code == 422
    response = client.post("/v1/render/jobs", json={"model": "safe-render", "parameters": {"prompt": "safe", "node": "1"}})
    assert response.status_code == 422


def test_job_transitions_and_outputs_are_durable(monkeypatch):
    def fake_request(method, path, payload=None, timeout=10):
        if path == "prompt":
            assert payload["prompt"]["6"]["inputs"]["text"] == "a cup"
            return {"prompt_id": "prompt-1"}
        if path.startswith("history/"):
            return {"prompt-1": {"status": {"completed": True}, "outputs": {"9": {"images": [{"filename": "cup.png", "subfolder": "", "type": "output"}]}}}}
        raise AssertionError(path)

    monkeypatch.setattr(runtime, "request_json", fake_request)
    monkeypatch.setattr(runtime, "download_output", lambda job_id, index, _descriptor: write_output(runtime.job_dir(job_id), index))
    client = TestClient(runtime.app)
    created = client.post("/v1/render/jobs", json={"model": "safe-render", "parameters": {"prompt": "a cup"}}).json()
    assert created["status"] == "queued"
    completed = wait_for_status(client, created["id"], {"completed"})
    assert completed["outputs"] == [{"index": 0, "content_type": "image/png", "bytes": 5}]
    assert "prompt_id" not in runtime.read_state(created["id"])
    assert client.get(f"/v1/render/jobs/{created['id']}/content/0").content == b"image"


def write_output(directory, index):
    directory.mkdir(parents=True, exist_ok=True)
    body = b"image"
    (directory / f"output-{index}.bin").write_bytes(body)
    return {"index": index, "content_type": "image/png", "bytes": len(body)}


@pytest.mark.parametrize(
    "descriptor",
    [
        {"filename": "../secret", "subfolder": "", "type": "output"},
        {"filename": "image.png", "subfolder": "/root", "type": "output"},
        {"filename": "image.png", "subfolder": "", "type": "http://attacker"},
    ],
)
def test_output_descriptor_rejects_paths_and_unknown_types(descriptor):
    with pytest.raises(RuntimeError, match="unsafe output descriptor"):
        runtime.download_output("render_abcdefghijklmnopqrst", 0, descriptor)


def test_comfyui_json_response_is_bounded(monkeypatch):
    monkeypatch.setattr(runtime, "MAX_JSON_BYTES", 8)
    monkeypatch.setattr(runtime, "urlopen", lambda *_args, **_kwargs: FakeResponse(b'{"value":123456}'))
    with pytest.raises(RuntimeError, match="invalid response"):
        runtime.request_json("GET", "system_stats")


def test_health_requires_comfyui_and_a_valid_template(monkeypatch):
    monkeypatch.setattr(runtime, "request_json", lambda *_args, **_kwargs: {})
    client = TestClient(runtime.app)
    assert client.get("/health").json()["status"] == "ready"
    monkeypatch.setattr(runtime, "WORKFLOW_ROOT", runtime.WORKFLOW_ROOT / "missing")
    response = client.get("/health")
    assert response.status_code == 503
    assert response.json()["status"] == "unsupported"


def test_cancellation_wins_over_a_late_comfyui_result(monkeypatch):
    history_started = Event()
    release_history = Event()

    def fake_request(_method, path, _payload=None, timeout=10):
        if path == "prompt":
            return {"prompt_id": "prompt-cancel"}
        if path.startswith("history/"):
            history_started.set()
            release_history.wait(1)
            return {"prompt-cancel": {"status": {"completed": True}, "outputs": {"9": {"images": [{"filename": "late.png", "type": "output"}]}}}}
        if path == "queue":
            return {}
        raise AssertionError(path)

    monkeypatch.setattr(runtime, "request_json", fake_request)
    monkeypatch.setattr(runtime, "download_output", lambda job_id, index, _descriptor: write_output(runtime.job_dir(job_id), index))
    client = TestClient(runtime.app)
    created = client.post("/v1/render/jobs", json={"model": "safe-render", "parameters": {"prompt": "cancel"}}).json()
    assert history_started.wait(2)
    cancelled = client.delete(f"/v1/render/jobs/{created['id']}").json()
    release_history.set()
    assert cancelled["status"] == "cancelled"
    time.sleep(0.05)
    assert client.get(f"/v1/render/jobs/{created['id']}").json()["status"] == "cancelled"
    assert not list(runtime.job_dir(created["id"]).glob("output-*.bin"))


def test_restart_recovers_incomplete_jobs(monkeypatch):
    state = {"id": "render_abcdefghijklmnopqrst", "object": "render.job", "template": "safe-render", "prompt_id": "prompt-recover", "status": "in_progress", "progress": 10, "created_at": 1}
    runtime.write_state(state)
    started = []
    monkeypatch.setattr(runtime, "start_job", started.append)
    runtime.recover_jobs()
    assert started == [state["id"]]
