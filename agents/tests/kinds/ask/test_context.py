import copy
from datetime import UTC, datetime, timedelta

import coslash_agents.kinds.ask as ask


async def test_thread_paging_is_bounded_and_not_counted(monkeypatch, ask_task, cases):
    pages = []
    first = [dict(cases["public"], ts=str(i)) for i in range(150)]
    second = [dict(cases["bot"], ts=str(i)) for i in range(100)]
    captured = {}

    class MCP:
        async def call(self, name, args):
            pages.append(copy.deepcopy(args))
            return {
                "items": first if len(pages) == 1 else second,
                "next_cursor": "next" if len(pages) == 1 else "last",
            }

        async def coslash_reply_in_thread(self, answer):
            captured["answer"] = answer

    async def model(task, mcp, deadline, started, seen, context):
        captured["context"] = context
        captured["seen"] = seen
        return {"found": False, "text": "", "citations": []}, 0

    monkeypatch.setattr(ask, "ask_model", model)
    await ask.run(ask_task, MCP(), datetime.now(UTC) + timedelta(seconds=100))
    assert len(pages) == 2
    assert pages[0] == {"channel_id": "channel-1", "thread_ts": "1.0", "limit": 200}
    assert pages[1]["limit"] == 50
    assert "since" not in pages[0] and "since" not in pages[1]
    assert len(captured["context"]) == 150
    assert len(captured["seen"]) == 150
