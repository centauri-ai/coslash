import pytest
from starlette.testclient import TestClient

from coslash_agents.tests.test_app import app_for

from .conftest import called, final, setup


@pytest.mark.integration
def test_ask_redelivery_and_forbidden(monkeypatch, cases, ask_task):
    fake, _ = setup(monkeypatch, cases, [final(found=False), final(found=False)])
    fake.outputs["coslash_reply_in_thread"] = {"slackTs": "ack"}
    with TestClient(app_for(monkeypatch, fake)) as client:
        headers = {"X-Coslash-Dev-Secret": "secret"}
        assert client.post("/v1/tasks", json=ask_task, headers=headers).status_code == 200
        fake.outputs["coslash_reply_in_thread"] = {"slackTs": "ack", "alreadyReplied": True}
        assert client.post("/v1/tasks", json=ask_task, headers=headers).status_code == 200
        fake.disabled = True
        assert client.post("/v1/tasks", json=ask_task, headers=headers).status_code == 409
    assert len(called(fake, "coslash_reply_in_thread")) == 2


@pytest.mark.integration
def test_forbidden_at_reply_returns_409(monkeypatch, cases, ask_task):
    from coslash_agents.tests.fakes.mcp import FakeMCP

    class ReplyForbidden(FakeMCP):
        async def handle(self, request):
            body = await request.json()
            if (
                body["method"] == "tools/call"
                and body["params"]["name"] == "coslash_reply_in_thread"
            ):
                self.disabled = True
            return await super().handle(request)

    fake, _ = setup(monkeypatch, cases, [final(found=False)])
    rejecting = ReplyForbidden(kind="ask", channel_id="channel-1")
    rejecting.outputs = fake.outputs
    with TestClient(app_for(monkeypatch, rejecting)) as client:
        response = client.post(
            "/v1/tasks", json=ask_task, headers={"X-Coslash-Dev-Secret": "secret"}
        )
    assert response.status_code == 409
    assert len(called(rejecting, "coslash_reply_in_thread")) == 1
