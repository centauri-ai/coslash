import asyncio
import json
from datetime import UTC, datetime
from pathlib import Path
from types import SimpleNamespace

import httpx
import pytest
from jsonschema import Draft202012Validator
from starlette.testclient import TestClient

import coslash_agents.app as app_module
import coslash_agents.kinds.board as board
import coslash_agents.kinds.day_report as day
import coslash_agents.llm.client as llm
import coslash_agents.mcp.client as mcp
import coslash_agents.tests.fakes.model as model_module
from coslash_agents.kinds import ask
from coslash_agents.kinds.board import common
from coslash_agents.safety import SCHEMA as REPORT_SCHEMA
from coslash_agents.safety import quote_untrusted, validate_refs
from coslash_agents.tests.fakes.model import FakeModel
from tests.kinds.ask import conftest as ask_support
from tests.kinds.board import support
from tests.kinds.day_report.test_slots import SLOTS


def request(schema, data, prompt="", tools=()):
    return {
        "input": [
            {"role": "system", "content": prompt},
            {"role": "user", "content": quote_untrusted(json.dumps(data))},
        ],
        "text": {"format": {"type": "json_schema", "name": "result", "schema": schema}},
        "tools": list(tools),
        "store": False,
    }


def content(response):
    return json.loads(response.json()["output"][0]["content"][0]["text"])


@pytest.mark.parametrize("schema,prompt", [(board.SCHEMA, board.PROMPT), (day.SCHEMA, day.PROMPT)])
def test_report_response_uses_seen_sources(schema, prompt):
    data = support.DATA
    body = request(schema, data, prompt)
    client = TestClient(FakeModel().app)
    first = content(client.post("/v1/responses", json=body))
    assert content(client.post("/responses", json=body)) == first
    Draft202012Validator(schema).validate(first)
    seen = common.seen_refs(data["sessions"], data["messages"])
    assert first["decisions"] and validate_refs([first["decisions"][0]["source"]], seen)
    if schema is board.SCHEMA:
        assert {row["sessionId"] for row in first["lines"]} == {"s1", "s2", "s3"}
    else:
        assert validate_refs(first["moved"][0]["refs"], seen) == first["moved"][0]["refs"]


@pytest.mark.parametrize("schema", [board.SCHEMA, day.SCHEMA])
def test_report_sources_exclude_bots_and_handle_empty_context(schema):
    client = TestClient(FakeModel().app)
    messages = [
        {"channel_id": "C", "ts": "1", "is_bot": True},
        {"channel_id": "C", "ts": "2", "is_bot": False},
    ]
    body = request(schema, {"sessions": [], "messages": messages})
    result = content(client.post("/v1/responses", json=body))
    Draft202012Validator(schema).validate(result)
    assert result["decisions"][0]["source"] == {"kind": "slack", "channelId": "C", "ts": "2"}
    body = request(schema, {"sessions": [], "messages": messages[:1]})
    result = content(client.post("/v1/responses", json=body))
    Draft202012Validator(schema).validate(result)
    assert result["decisions"] == []


def test_ask_cites_non_bot_thread_source():
    thread = [
        {"channel_id": "C", "ts": "1", "is_bot": True, "text": "bot"},
        {"channel_id": "C", "ts": "2", "is_bot": False, "text": "hello <world> & team"},
    ]
    body = request(ask.SCHEMA, {"question": "What happened?", "thread": thread}, ask.PROMPT)
    result = content(TestClient(FakeModel().app).post("/v1/responses", json=body))
    Draft202012Validator(ask.SCHEMA).validate(result)
    assert result["found"] is True
    assert result["citations"] == [{"kind": "slack", "channelId": "C", "ts": "2"}]
    assert validate_refs(result["citations"], ask.seen_in("coslash_conversation_window", thread))


def test_ask_fetches_sources_and_falls_back_when_none():
    body = request(
        ask.SCHEMA, {"question": "What happened?", "thread": []}, ask.PROMPT, ask.tools_for_model()
    )
    client = TestClient(FakeModel().app)
    call = client.post("/v1/responses", json=body).json()["output"][0]
    assert call["name"] == "coslash_list_sessions"
    body["input"].extend(
        [
            call,
            {
                "type": "function_call_output",
                "call_id": call["call_id"],
                "output": quote_untrusted(json.dumps({"items": [{"id": "s1"}]})),
            },
        ]
    )
    result = content(client.post("/v1/responses", json=body))
    Draft202012Validator(ask.SCHEMA).validate(result)
    assert result["citations"] == [{"kind": "session", "sessionId": "s1"}]
    assert validate_refs(
        result["citations"], ask.seen_in("coslash_list_sessions", {"items": [{"id": "s1"}]})
    )
    body["input"][-1]["output"] = quote_untrusted('{"items": []}')
    result = content(client.post("/v1/responses", json=body))
    Draft202012Validator(ask.SCHEMA).validate(result)
    assert result["found"] is False and result["citations"] == []


def test_control_switch_and_invalid_input_preserve_scripted_replies():
    scripted = {"output": [{"type": "message"}], "usage": {}}
    fake = FakeModel(replies=[scripted], mode="error")
    client = TestClient(fake.app)
    assert client.post("/responses", json={}).status_code == 400
    assert client.post("/_control", json={"mode": "invalid"}).status_code == 400
    assert client.post("/_control", content="broken").status_code == 400
    assert client.post("/responses", json={}).status_code == 400
    assert client.post("/_control", json={"mode": "ok"}).json() == {"mode": "ok"}
    assert client.post("/responses", json={}).json() == scripted
    assert content(client.post("/responses", json={})) == {"text": "ok"}
    assert client.post("/_control", json={"mode": "error"}).json() == {"mode": "error"}
    assert client.post("/responses", json={}).status_code == 400


async def test_slow_mode_can_be_changed_while_request_is_pending(monkeypatch):
    entered, release = asyncio.Event(), asyncio.Event()

    async def wait(_):
        entered.set()
        await release.wait()

    monkeypatch.setattr(model_module, "asyncio", SimpleNamespace(sleep=wait))
    fake = FakeModel()
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=fake.app), base_url="http://fake"
    ) as client:
        assert (await client.post("/_control", json={"mode": "slow"})).status_code == 200
        pending = asyncio.create_task(client.post("/responses", json={}))
        await entered.wait()
        assert (await client.post("/_control", json={"mode": "ok"})).status_code == 200
        assert (await client.post("/responses", json={})).status_code == 200
        release.set()
        assert (await pending).status_code == 200


class FrozenDateTime(datetime):
    @classmethod
    def now(cls, tz=None):
        return cls(2026, 11, 2, 12, tzinfo=UTC).astimezone(tz)


@pytest.mark.parametrize("slot", [None, *SLOTS])
@pytest.mark.integration
def test_default_model_produces_written_reports(monkeypatch, slot):
    for module in (support, app_module, board, day, llm, mcp):
        monkeypatch.setattr(module, "datetime", FrozenDateTime)
    kind = "board_tick" if slot is None else "day_report"
    fake, model, app = support.setup(monkeypatch, kind=kind)
    model.replies.clear()
    task = support.payload(kind)
    if slot:
        task["task"]["day_report"] = {key: slot[key] for key in ("slot", "window", "timezone")}
    assert support.dispatch(app, task).status_code == 200
    report = support.written(fake)["report"]
    Draft202012Validator(REPORT_SCHEMA).validate(report)
    assert report["insightMode"] == "llm" and report["decisions"]
    if slot is None:
        assert report["active"] and report["done"]
    else:
        assert report["moved"]
        assert ("totals" in report) == slot["slot"].endswith("#3/3")


@pytest.mark.integration
async def test_default_ask_tool_loop_and_live_error_recovery(monkeypatch):
    for module in (ask_support, ask, mcp, llm):
        monkeypatch.setattr(module, "datetime", FrozenDateTime)
    cases = json.loads((Path(__file__).parents[1] / "kinds/ask/fixtures/cases.json").read_text())
    task = json.loads(
        (Path(__file__).parents[1] / "fakes/fixtures/ctb-agents-1/ask.json").read_text()
    )
    fake, model = ask_support.setup(monkeypatch, cases)
    fake.outputs["coslash_conversation_window"]["items"] = []
    await ask_support.execute(fake, task)
    reply = ask_support.called(fake, "coslash_reply_in_thread")[-1]["answer"]
    assert reply["citations"] == [{"kind": "session", "sessionId": "s1"}]
    assert ask_support.called(fake, "coslash_list_sessions")
    async with httpx.AsyncClient(
        transport=httpx.ASGITransport(app=model.app), base_url="http://fake"
    ) as client:
        assert (await client.post("/_control", json={"mode": "error"})).status_code == 200
        await ask_support.execute(fake, task)
        assert (
            ask_support.called(fake, "coslash_reply_in_thread")[-1]["answer"]["text"] == ask.FAILED
        )
        assert (await client.post("/_control", json={"mode": "ok"})).status_code == 200
        await ask_support.execute(fake, task)
        assert (
            ask_support.called(fake, "coslash_reply_in_thread")[-1]["answer"]["citations"]
            == reply["citations"]
        )
