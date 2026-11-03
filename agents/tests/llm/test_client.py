from datetime import UTC, datetime, timedelta

import httpx
import pytest
import structlog

from coslash_agents.llm.client import ModelClient, ModelError
from coslash_agents.tests.fakes.model import FakeModel


async def run(fake, kind="ask"):
    async with ModelClient(
        kind,
        "task-1",
        "team-1",
        datetime.now(UTC) + timedelta(minutes=5),
        api_key="private-key",
        base_url="http://fake/v1",
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        return await client.run(
            [{"role": "user", "content": "hello"}],
            {
                "type": "object",
                "properties": {"text": {"type": "string"}},
                "required": ["text"],
                "additionalProperties": False,
            },
        )


async def test_store_false_and_tier_fallback():
    fake = FakeModel(mode="tier_error")
    with structlog.testing.capture_logs() as logs:
        result = await run(fake)
    assert len(fake.calls) == 2
    assert all(request["store"] is False for request in fake.calls)
    assert fake.calls[0]["service_tier"] == "fast"
    assert "service_tier" not in fake.calls[1]
    assert result.service_tier == "default"
    assert result.usage.cached_input_tokens == 2
    assert [line["event"] for line in logs] == ["tier_fallback", "llm_finished"]


async def test_other_400_does_not_retry():
    fake = FakeModel(mode="error")
    with pytest.raises(ModelError) as exc:
        await run(fake)
    assert len(fake.calls) == 1
    assert fake.calls[0]["store"] is False
    assert exc.value.usage.estimated
    assert exc.value.usage.output_tokens == 6000


async def test_function_loop_sends_context_and_store_false():
    reply = {
        "output": [
            {
                "type": "function_call",
                "call_id": "call-1",
                "name": "lookup",
                "arguments": '{"id":"1"}',
            }
        ],
        "usage": {"input_tokens": 1, "output_tokens": 2},
    }
    fake = FakeModel(replies=[reply])

    async def lookup(name, args):
        return {"found": args["id"]}

    async with ModelClient(
        "ask",
        "task-1",
        "team-1",
        datetime.now(UTC) + timedelta(minutes=3),
        api_key="private-key",
        base_url="http://fake/v1",
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        result = await client.run(
            [],
            {"type": "object"},
            tools=[{"type": "function", "name": "lookup", "parameters": {"type": "object"}}],
            call_tool=lookup,
        )
    assert len(fake.calls) == 2 and all(call["store"] is False for call in fake.calls)
    assert fake.calls[1]["input"][-1]["call_id"] == "call-1"
    assert result.usage.input_tokens == 11


@pytest.mark.parametrize("usage", [None, {"input_tokens": 1}])
async def test_missing_usage_is_estimated(usage):
    reply = {
        "output": [
            {"type": "message", "content": [{"type": "output_text", "text": '{"text":"ok"}'}]}
        ]
    }
    if usage is not None:
        reply["usage"] = usage
    result = await run(FakeModel(replies=[reply]))
    assert result.usage.estimated
    assert result.usage.input_tokens > 0
    assert result.usage.output_tokens == 6000
