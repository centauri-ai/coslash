import coslash_agents.app as app_module
from coslash_agents.mcp.client import TaskForbidden
from tests.kinds.board.support import calls, dispatch, payload, setup, written


def test_nullable_empty_board_and_missing_previous_content(monkeypatch):
    fake, model, app = setup(monkeypatch)
    parent = app_module.MCPClient

    class EmptyBoard(parent):
        async def call(self, name, arguments=None):
            if name == "coslash_get_team_board":
                return {
                    "slot": None,
                    "insight_mode": None,
                    "created_at": None,
                    "report": None,
                    "untrusted": True,
                }
            return await super().call(name, arguments)

    monkeypatch.setattr(app_module, "MCPClient", EmptyBoard)
    task = payload()
    assert dispatch(app, task).status_code == 200
    assert written(fake)["report"]["insightMode"] == "llm"
    fake.calls.clear()
    model.calls.clear()
    task["task"]["board_tick"]["lastReport"] = {
        "id": "previous",
        "inputDigest": "old",
        "insightsAsOf": "2026-11-02T11:00:00Z",
    }
    assert dispatch(app, task).status_code == 503
    assert not calls(fake, "coslash_write_board_report") and not model.calls


def test_forbidden_during_usage_stops_task(monkeypatch):
    fake, _, app = setup(monkeypatch)
    parent = app_module.MCPClient

    class ForbiddenUsage(parent):
        async def call(self, name, arguments=None):
            if name == "coslash_record_usage":
                raise TaskForbidden()
            return await super().call(name, arguments)

    monkeypatch.setattr(app_module, "MCPClient", ForbiddenUsage)
    assert dispatch(app, payload()).status_code == 409
    assert not calls(fake, "coslash_write_board_report")
    assert not calls(fake, "coslash_upsert_issues")


def test_unknown_bot_status_is_not_treated_as_evidence(monkeypatch):
    fake, model, app = setup(monkeypatch)
    del fake.outputs["coslash_conversation_window"]["items"][0]["is_bot"]
    assert dispatch(app, payload()).status_code == 503
    assert not model.calls and not calls(fake, "coslash_write_board_report")


def test_all_list_reads_follow_cursors(monkeypatch):
    fake, _, app = setup(monkeypatch)
    parent = app_module.MCPClient
    received = []

    class Paged(parent):
        async def call(self, name, arguments=None):
            if name in (
                "coslash_team_activity",
                "coslash_conversation_window",
                "coslash_list_team_issues",
            ):
                args = arguments or {}
                received.append((name, args.get("cursor")))
                source = fake.outputs[name]
                items = source["items"]
                if args.get("cursor"):
                    return {**source, "items": items[1:], "next_cursor": None}
                return {**source, "items": items[:1], "next_cursor": "next"}
            return await super().call(name, arguments)

    monkeypatch.setattr(app_module, "MCPClient", Paged)
    assert dispatch(app, payload()).status_code == 200
    report = written(fake)["report"]
    assert report["windows"]["24h"]["sessions"] == 3
    assert [row["sessionId"] for row in report["done"]] == ["s2"]
    for name in (
        "coslash_team_activity",
        "coslash_conversation_window",
        "coslash_list_team_issues",
    ):
        assert (name, "next") in received
