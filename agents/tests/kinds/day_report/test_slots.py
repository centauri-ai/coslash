import json
from pathlib import Path

import pytest

from tests.kinds.board.support import calls, dispatch, payload, setup, written

SLOTS = json.loads((Path(__file__).parent / "fixtures/slots.json").read_text())


@pytest.mark.parametrize("slot", SLOTS)
@pytest.mark.integration
def test_slots_use_supplied_windows_and_dst(monkeypatch, slot):
    fake, model, app = setup(monkeypatch, kind="day_report")
    task = payload("day_report")
    if slot["slot"].endswith("#3/3"):
        fake.outputs["coslash_team_activity"]["items"][1]["ended_at"] = "2026-11-02T01:45:00Z"
    task["task"]["day_report"] = {k: slot[k] for k in ("slot", "window", "timezone")}
    task["budget"]["state"] = "soft_limit"
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["title"] == slot["title"]
    assert report["window"] == slot["window"]
    assert report["insightMode"] == "llm" and len(model.calls) == 1
    activity = calls(fake, "coslash_team_activity")
    assert activity[0]["window"] == {
        "from": slot["window"]["start"],
        "to": slot["window"]["end"],
    }
    assert activity[1]["window"]["to"] == slot["window"]["end"]
    assert ("totals" in report) == slot["slot"].endswith("#3/3")
    if "totals" in report:
        assert report["totals"] == {"sessions": 3, "people": 2, "done": 1, "decisions": 3}
        requests = calls(fake, "coslash_get_team_board")
        assert [r["slot"] for r in requests] == ["2026-11-01#1/3", "2026-11-01#2/3"]
    assert calls(fake, "coslash_record_usage")[0]["feature"] == "day_report"
    assert not calls(fake, "coslash_upsert_issues")


def test_quiet_bot_messages_do_not_trigger_model(monkeypatch):
    fake, model, app = setup(monkeypatch, kind="day_report")
    fake.outputs["coslash_team_activity"]["items"] = []
    fake.outputs["coslash_conversation_window"]["items"] = [
        {"channel_id": "C1", "ts": "1", "is_bot": True, "text": "bot"}
    ]
    task = payload("day_report")
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["quiet"] is True and report["insightMode"] == "carry_forward"
    assert all(
        report[field] == []
        for field in ("themes", "active", "done", "decisions", "moved", "issues", "people")
    )
    assert not model.calls and not calls(fake, "coslash_record_usage")


def test_final_totals_include_prior_truncated_decisions(monkeypatch):
    fake, _, app = setup(monkeypatch, kind="day_report")
    from coslash_agents.tests.fakes.mcp import BOARD_EXAMPLE
    import copy

    prior = copy.deepcopy(BOARD_EXAMPLE)
    prior["truncated"] = [{"field": "decisions", "count": 2}]
    fake.outputs["coslash_get_team_board"] = {
        "slot": "previous",
        "insight_mode": "llm",
        "created_at": "2026-11-02T12:00:00Z",
        "report": prior,
        "untrusted": True,
    }
    task = payload("day_report")
    task["task"]["day_report"]["slot"] = "2026-11-02#3/3"
    assert dispatch(app, task).status_code == 200
    assert written(fake)["report"]["totals"]["decisions"] == 7


def test_active_is_evaluated_at_the_window_end(monkeypatch):
    fake, _, app = setup(monkeypatch, kind="day_report")
    task = payload("day_report")
    task["task"]["day_report"]["window"] = {
        "start": "2026-11-02T08:00:00Z",
        "end": "2026-11-02T12:00:00Z",
    }
    fake.outputs["coslash_team_activity"]["items"][2]["last_activity_at"] = "2026-11-02T09:59:59Z"
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert [row["sessionId"] for row in report["active"]] == ["s1"]
    assert [row["sessionId"] for row in report["done"]] == ["s2"]
