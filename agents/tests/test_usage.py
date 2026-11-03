from types import SimpleNamespace

from coslash_agents.llm.client import Usage
from coslash_agents.mcp.client import TOOLS
from coslash_agents.usage import record_usage


async def test_usage_estimate_uses_contract_and_never_fails_task(monkeypatch):
    class MCP:
        task_id = "task-1"
        kind = "ask"
        calls = []

        async def coslash_record_usage(self, **kwargs):
            self.calls.append(kwargs)
            return SimpleNamespace(soft_limit_pct=42)

    client = MCP()
    usage = Usage(input_tokens=20, output_tokens=6000, estimated=True)
    assert await record_usage(client, "model", "high", "default", usage) == 42
    assert "estimated" not in client.calls[-1]
    props = TOOLS["coslash_record_usage"]["inputSchema"]["properties"]
    monkeypatch.setitem(props, "estimated", {"type": "boolean"})
    assert await record_usage(client, "model", "high", "default", usage) == 42
    assert client.calls[-1]["estimated"] is True

    async def broken(**kwargs):
        raise RuntimeError("private content")

    client.coslash_record_usage = broken
    assert await record_usage(client, "model", "high", "default", usage) is None
