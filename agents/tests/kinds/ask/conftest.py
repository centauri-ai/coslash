import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest

import coslash_agents.kinds.ask as ask
from coslash_agents.llm.client import ModelClient
from coslash_agents.mcp.client import MCPClient
from coslash_agents.tests.fakes.mcp import FakeMCP
from coslash_agents.tests.fakes.model import FakeModel


@pytest.fixture
def cases():
    return json.loads((Path(__file__).parent / "fixtures/cases.json").read_text())


@pytest.fixture
def ask_task():
    path = Path(__file__).parents[2] / "fakes/fixtures/ctb-agents-1/ask.json"
    task = json.loads(path.read_text())
    expiry = (datetime.now(UTC) + timedelta(minutes=3)).isoformat()
    task["task"]["expiresAt"] = task["mcp"]["expiresAt"] = expiry
    task["mcp"]["url"] = "http://fake/mcp"
    return task


def final(text="The API is ready.", citations=None, found=True):
    return {
        "output": [
            {
                "type": "message",
                "content": [
                    {
                        "type": "output_text",
                        "text": json.dumps(
                            {"text": text, "citations": citations or [], "found": found}
                        ),
                    }
                ],
            }
        ],
        "usage": {"input_tokens": 10, "output_tokens": 5},
    }


def calls(*requests):
    return {
        "output": [
            {
                "type": "function_call",
                "call_id": str(i),
                "name": name,
                "arguments": json.dumps(args),
            }
            for i, (name, args) in enumerate(requests)
        ],
        "usage": {"input_tokens": 10, "output_tokens": 5},
    }


def setup(monkeypatch, cases, replies=None, mode="normal"):
    model = FakeModel(replies=replies, mode=mode)
    fake = FakeMCP(kind="ask", channel_id="channel-1")
    fake.outputs["coslash_conversation_window"] = {
        "items": [cases["public"], cases["bot"]],
        "next_cursor": None,
        "untrusted": True,
    }
    fake.outputs["coslash_conversation_search"] = fake.outputs["coslash_conversation_window"]
    fake.outputs["coslash_get_session"] = {
        "session_id": "s1",
        "goal": "API work",
        "pull_requests": [],
        "untrusted": True,
    }

    class FakeClient(ModelClient):
        def __init__(self, *args):
            super().__init__(
                *args,
                api_key="synthetic",
                base_url="http://fake/v1",
                transport=httpx.ASGITransport(app=model.app),
            )

    monkeypatch.setattr(ask, "ModelClient", FakeClient)
    return fake, model


async def execute(fake, task, seconds=100, *, deadline=None):
    deadline = deadline or datetime.now(UTC) + timedelta(seconds=seconds)
    async with MCPClient(
        "http://fake/mcp",
        "synthetic-token",
        "task-1",
        "ask",
        deadline,
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        await client.initialize()
        await ask.run(task, client, deadline)


def called(fake, name):
    return [
        c["params"]["arguments"]
        for c in fake.calls
        if c["method"] == "tools/call" and c["params"]["name"] == name
    ]
