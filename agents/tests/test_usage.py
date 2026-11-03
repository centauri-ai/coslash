from types import SimpleNamespace

import pytest

from coslash_agents.llm.client import Usage
from coslash_agents.mcp.client import TaskExpired, TaskForbidden
from coslash_agents.usage import record_usage


@pytest.mark.parametrize(
    "kind,feature", [("board_tick", "board"), ("day_report", "day_report"), ("ask", "ask")]
)
@pytest.mark.parametrize("estimated", [True, False])
async def test_usage_kind_and_estimate(kind, feature, estimated):
    class MCP:
        task_id = "task-1"
        calls = []

        async def coslash_record_usage(self, **kwargs):
            self.calls.append(kwargs)
            return SimpleNamespace(soft_limit_pct=42)

    client = MCP()
    client.kind = kind
    usage = Usage(input_tokens=20, output_tokens=6000, estimated=estimated)
    assert await record_usage(client, "model", "high", "default", usage) == 42
    assert client.calls[-1]["feature"] == feature
    assert client.calls[-1]["estimated"] is estimated


@pytest.mark.parametrize("error", [TaskForbidden, TaskExpired, RuntimeError])
async def test_usage_failure_preserves_task_invalidation(error):
    class MCP:
        task_id = "task-1"
        kind = "ask"

        async def coslash_record_usage(self, **kwargs):
            raise error("private content")

    if error is RuntimeError:
        assert await record_usage(MCP(), "model", "high", "default", Usage()) is None
    else:
        with pytest.raises(error):
            await record_usage(MCP(), "model", "high", "default", Usage())
