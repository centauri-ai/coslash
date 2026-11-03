from datetime import UTC, datetime, timedelta

import httpx
import pytest

from coslash_agents.mcp.client import MCPClient, TOOLS
from coslash_agents.tests.fakes.mcp import BOARD_EXAMPLE, FakeMCP, sample


@pytest.mark.integration
@pytest.mark.parametrize("name", [name for name, tool in TOOLS.items() if tool["taskKinds"]])
async def test_each_wrapper_against_schema_fake(name):
    tool = TOOLS[name]
    kind = tool["taskKinds"][0]
    fake = FakeMCP(kind=kind)
    properties = tool["inputSchema"].get("properties", {})
    args = {
        key: sample(properties[key])
        for key in tool["inputSchema"].get("required", [])
        if key != "task_id"
    }
    if name == "coslash_write_board_report":
        kind = "board_tick"
        fake.kind = kind
        args["report"] = BOARD_EXAMPLE
        args["insight_mode"] = BOARD_EXAMPLE["insightMode"]
    async with MCPClient(
        "http://fake/mcp",
        "synthetic-token",
        "task-1",
        kind,
        datetime.now(UTC) + timedelta(minutes=1),
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        await client.initialize()
        result = await getattr(client, name)(**args)
    assert result is not None
    assert fake.calls[-1]["method"] == "tools/call"


async def test_fake_audience_restricts_private_messages():
    for kind, channel, expected in [
        ("board_tick", None, False),
        ("ask", "C_PUBLIC", False),
        ("ask", "C_PRIVATE", True),
    ]:
        fake = FakeMCP(kind=kind, channel_id=channel)
        async with MCPClient(
            "http://fake/mcp",
            "synthetic-token",
            "task-1",
            kind,
            datetime.now(UTC) + timedelta(minutes=1),
            transport=httpx.ASGITransport(app=fake.app),
        ) as client:
            result = await client.call("coslash_conversation_window")
        assert any(item["channel_id"] == "C_PRIVATE" for item in result["items"]) is expected


@pytest.mark.parametrize("failure", ["unavailable", "server", "network", "other"])
async def test_tool_error_mapping(failure):
    import json

    from coslash_agents.mcp.client import Retryable, ToolError

    calls = []

    def handle(request):
        calls.append(request)
        if failure == "network":
            raise httpx.ConnectError("not connected", request=request)
        if failure == "server":
            return httpx.Response(503)
        error = {
            "error": {
                "code": "UNAVAILABLE" if failure == "unavailable" else "INVALID_ARGUMENT",
                "hint": "bad_input",
            }
        }
        return httpx.Response(
            200,
            json={
                "result": {
                    "isError": True,
                    "content": [{"type": "text", "text": json.dumps(error)}],
                }
            },
        )

    async with MCPClient(
        "http://fake/mcp",
        "synthetic-token",
        "task-1",
        "board_tick",
        datetime.now(UTC) + timedelta(minutes=1),
        transport=httpx.MockTransport(handle),
    ) as client:
        if failure == "other":
            with pytest.raises(ToolError) as error:
                await client.coslash_conversation_window()
            assert error.value.code == "INVALID_ARGUMENT" and error.value.hint == "bad_input"
        else:
            with pytest.raises(Retryable):
                await client.coslash_conversation_window()
    assert len(calls) == 1
