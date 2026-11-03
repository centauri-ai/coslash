from datetime import UTC, datetime, timedelta

import httpx
from starlette.testclient import TestClient

import coslash_agents.app as app_module
from coslash_agents.mcp.client import MCPClient
from coslash_agents.runtime.config import Config
from coslash_agents.tests.fakes.mcp import FakeMCP


def app_for(monkeypatch, fake):
    class TestMCP(MCPClient):
        def __init__(self, *args):
            super().__init__(*args, transport=httpx.ASGITransport(app=fake.app))

    monkeypatch.setattr(app_module, "MCPClient", TestMCP)
    return app_module.create_app(Config("dev", "", "", "http://fake", "secret"))


def test_dev_task_dispatches_to_stub(monkeypatch, task):
    fake = FakeMCP()
    with TestClient(app_for(monkeypatch, fake)) as client:
        response = client.post("/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"})
    assert response.status_code == 503
    assert response.json()["error"] == "kind_not_implemented"
    assert [c["method"] for c in fake.calls] == ["initialize"]


def test_expired_no_calls(monkeypatch, task):
    fake = FakeMCP()
    task["task"]["expiresAt"] = (datetime.now(UTC) - timedelta(seconds=1)).isoformat()
    with TestClient(app_for(monkeypatch, fake)) as client:
        assert (
            client.post(
                "/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"}
            ).status_code
            == 409
        )
    assert fake.calls == []


def test_wrong_origin_and_secret(monkeypatch, task):
    fake = FakeMCP()
    task["mcp"]["url"] = "http://fake.attacker/mcp"
    with TestClient(app_for(monkeypatch, fake)) as client:
        assert (
            client.post(
                "/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "wrong"}
            ).status_code
            == 401
        )
        assert (
            client.post(
                "/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"}
            ).status_code
            == 409
        )
    assert fake.calls == []
