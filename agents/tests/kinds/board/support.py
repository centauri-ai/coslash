import copy
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
from starlette.testclient import TestClient

import coslash_agents.app as app_module
from coslash_agents.kinds.board import common
from coslash_agents.llm.client import ModelClient
from coslash_agents.mcp.client import MCPClient
from coslash_agents.runtime.config import Config
from coslash_agents.tests.fakes.mcp import FakeMCP
from coslash_agents.tests.fakes.model import FakeModel

DATA = json.loads((Path(__file__).parent / "fixtures/activity.json").read_text())
BOARD = {
    "themes": [{"name": "API", "summary": "Building", "sessionIds": ["s1"]}],
    "lines": [{"sessionId": "s1", "line": "Build the API"}],
    "headline": "API progress",
    "decisions": [{"text": "Use REST", "source": {"kind": "session", "sessionId": "s1"}}],
    "issues": [],
}
DAY = {
    "moved": [{"text": "API progress", "refs": [{"kind": "session", "sessionId": "s1"}]}],
    "decisions": [{"text": "Use REST", "source": {"kind": "session", "sessionId": "s1"}}],
}


def response(content):
    return {
        "id": "fake",
        "output": [
            {
                "type": "message",
                "role": "assistant",
                "content": [{"type": "output_text", "text": json.dumps(content)}],
            }
        ],
        "usage": {"input_tokens": 10, "output_tokens": 3},
    }


def setup(monkeypatch, kind="board_tick", content=None, mode="normal"):
    fake = FakeMCP(kind=kind)
    data = copy.deepcopy(DATA)
    fake.outputs["coslash_team_activity"] = {
        "as_of": "2026-11-02T12:00:00Z",
        "counts": {"active": 1, "idle": 1, "done": 1, "people": 2},
        "items": data["sessions"],
        "next_cursor": None,
        "untrusted": True,
    }
    fake.outputs["coslash_conversation_window"] = {
        "items": data["messages"],
        "next_cursor": None,
        "untrusted": True,
    }
    fake.outputs["coslash_list_team_issues"] = {"items": [], "next_cursor": None, "untrusted": True}
    model = FakeModel(
        replies=[response(content or (BOARD if kind == "board_tick" else DAY))], mode=mode
    )

    class TestMCP(MCPClient):
        def __init__(self, *args):
            super().__init__(*args, transport=httpx.ASGITransport(app=fake.app))

    class TestModel(ModelClient):
        def __init__(self, *args):
            transport = httpx.ASGITransport(app=model.app)
            if mode == "timeout":

                def timeout(request):
                    raise httpx.ReadTimeout("timed out", request=request)

                transport = httpx.MockTransport(timeout)
            super().__init__(*args, transport=transport)

    monkeypatch.setattr(app_module, "MCPClient", TestMCP)
    monkeypatch.setattr(common, "ModelClient", TestModel)
    app = app_module.create_app(Config("dev", "", "", "http://fake", "secret"))
    return fake, model, app


def payload(kind="board_tick"):
    file = Path(__file__).parents[2] / "fakes/fixtures/ctb-agents-1" / (kind + ".json")
    task = json.loads(file.read_text())
    expires = (datetime.now(UTC) + timedelta(minutes=20)).isoformat()
    task["task"]["expiresAt"] = task["mcp"]["expiresAt"] = expires
    task["mcp"]["url"] = "http://fake/mcp"
    return task


def dispatch(app, task):
    with TestClient(app) as client:
        return client.post("/v1/tasks", json=task, headers={"X-Coslash-Dev-Secret": "secret"})


def calls(fake, name):
    return [
        x["params"]["arguments"]
        for x in fake.calls
        if x.get("method") == "tools/call" and x["params"]["name"] == name
    ]


def written(fake):
    return calls(fake, "coslash_write_board_report")[-1]


def last(fake, report, input_digest):
    fake.outputs["coslash_get_team_board"] = {
        "slot": "2026-11-02T12:00:00Z",
        "insight_mode": report["insightMode"],
        "created_at": report["asOf"],
        "report": report,
        "untrusted": True,
    }
    return {"id": "report-1", "inputDigest": input_digest, "insightsAsOf": report["insightsAsOf"]}


def setup_overlapping_activity(monkeypatch, kind):
    fake, model, app = setup(monkeypatch, kind=kind)
    fake.outputs["coslash_team_activity"]["as_of"] = "2026-11-03T12:00:00Z"

    def session(session_id, status, started_at, last_activity_at, ended_at=None):
        item = {
            "session_id": session_id,
            "title": session_id,
            "status": status,
            "agent": "codex",
            "owner": {"id": session_id, "name": session_id},
            "started_at": started_at,
            "last_activity_at": last_activity_at,
        }
        if ended_at:
            item["ended_at"] = ended_at
        return item

    fake.outputs["coslash_team_activity"]["items"] = [
        session(
            "s1", "done", "2026-11-02T10:30:00Z", "2026-11-02T12:15:00Z", "2026-11-02T12:30:00Z"
        ),
        session(
            "s2", "done", "2026-11-02T11:00:00Z", "2026-11-02T11:50:00Z", "2026-11-02T11:50:00Z"
        ),
        session("s3", "idle", "2026-11-01T18:00:00Z", "2026-11-02T09:00:00Z"),
        session("before", "done", "2026-11-01T08:00:00Z", "2026-11-01T11:59:00Z"),
        session("after", "active", "2026-11-02T12:01:00Z", "2026-11-02T12:05:00Z"),
    ]
    parent = app_module.MCPClient

    class OverlapMCP(parent):
        async def call(self, name, arguments=None):
            result = await super().call(name, arguments)
            if name == "coslash_team_activity":
                window = arguments["window"]
                if isinstance(window, str):
                    end = common.timestamp("2026-11-03T12:00:00Z")
                    start = end - timedelta(hours={"1h": 1, "24h": 24}[window])
                else:
                    start, end = common.timestamp(window["from"]), common.timestamp(window["to"])
                result["items"] = [
                    item
                    for item in result["items"]
                    if common.timestamp(item["started_at"]) <= end
                    and common.timestamp(item["last_activity_at"]) >= start
                ]
            return result

    monkeypatch.setattr(app_module, "MCPClient", OverlapMCP)
    return fake, model, app
