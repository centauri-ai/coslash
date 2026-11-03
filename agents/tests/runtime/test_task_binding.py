from datetime import UTC, datetime, timedelta

import httpx
import pytest
from starlette.testclient import TestClient

from coslash_agents.kinds import REGISTRY, Registration
from coslash_agents.mcp.client import MCPClient, TaskForbidden
from coslash_agents.tests.fakes.mcp import FakeMCP
from tests.test_app import app_for


async def test_local_forbidden_no_call():
    fake = FakeMCP()
    async with MCPClient(
        "http://fake/mcp",
        "synthetic-token",
        "task-1",
        "board_tick",
        datetime.now(UTC) + timedelta(minutes=1),
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        with pytest.raises(TaskForbidden):
            await client.call("coslash_reply_in_thread", {"answer": {}})
        with pytest.raises(TaskForbidden):
            await client.call("coslash_record_usage", {"task_id": "other"})
    assert fake.calls == []


@pytest.mark.parametrize("mode", ["disabled", "expired"])
def test_server_denial_maps_409(monkeypatch, task, mode):
    fake = FakeMCP()
    setattr(fake, mode, True)

    async def handler(payload, mcp, deadline):
        await mcp.coslash_conversation_window()

    original = REGISTRY["board_tick"]
    monkeypatch.setitem(REGISTRY, "board_tick", Registration(handler, original.tools))
    with TestClient(app_for(monkeypatch, fake)) as client:
        response = client.post("/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"})
        assert response.status_code == 409
        calls = len(fake.calls)
        assert (
            client.post(
                "/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"}
            ).status_code
            == 409
        )
        assert len(fake.calls) == calls
    calls = len(fake.calls)
    assert calls == (2 if mode == "disabled" else 0)
