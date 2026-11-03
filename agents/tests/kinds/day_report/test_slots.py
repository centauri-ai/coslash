import json
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

import pytest

from tests.kinds.board.support import (
    calls,
    dispatch,
    payload,
    setup,
    setup_overlapping_activity,
    written,
)

SLOTS = json.loads((Path(__file__).parent / "fixtures/slots.json").read_text())


def server_accepts_title(report, spec):
    # Matches the task-aware title check in P3's agentreports/validate.go.
    date, slot = spec["slot"].split("#")
    date = datetime.fromisoformat(date)
    month = ("Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec")[
        date.month - 1
    ]
    zone = ZoneInfo(spec["timezone"])
    start = datetime.fromisoformat(spec["window"]["start"].replace("Z", "+00:00")).astimezone(zone)
    end = datetime.fromisoformat(spec["window"]["end"].replace("Z", "+00:00")).astimezone(zone)
    return report["title"] == f"Day report {slot} · {month} {date.day} · {start:%H:%M}–{end:%H:%M}"


@pytest.mark.parametrize("slot", SLOTS)
@pytest.mark.integration
def test_slots_use_supplied_windows_and_dst(monkeypatch, slot):
    fake, model, app = setup(monkeypatch, kind="day_report")
    task = payload("day_report")
    if slot["slot"].endswith("#3/3"):
        fake.outputs["coslash_team_activity"]["items"][0]["last_activity_at"] = (
            "2026-11-02T01:30:00Z"
        )
        fake.outputs["coslash_team_activity"]["items"][1]["ended_at"] = "2026-11-02T01:45:00Z"
    task["task"]["day_report"] = {k: slot[k] for k in ("slot", "window", "timezone")}
    task["budget"]["state"] = "soft_limit"
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["title"] == slot["title"]
    assert server_accepts_title(report, task["task"]["day_report"])
    assert not server_accepts_title(
        {**report, "title": report["title"] + "!"}, task["task"]["day_report"]
    )
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
    assert server_accepts_title(report, task["task"]["day_report"])
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


@pytest.mark.parametrize(
    ("slot", "example"),
    [("1/3", "day_quiet.json"), ("2/3", "day_2_of_3.json"), ("3/3", "day_3_of_3.json")],
)
def test_produced_title_matches_vendored_examples(monkeypatch, slot, example):
    path = Path(__file__).resolve().parents[3] / "contracts/ctb/examples" / example
    expected = json.loads(path.read_text())
    fake, _, app = setup(monkeypatch, kind="day_report")
    task = payload("day_report")
    task["task"]["day_report"] = {
        "slot": f"2026-11-02#{slot}",
        "timezone": "UTC",
        "window": expected["window"],
    }
    if expected.get("quiet"):
        fake.outputs["coslash_team_activity"]["items"] = []
        fake.outputs["coslash_conversation_window"]["items"] = []
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["title"] == expected["title"]
    assert server_accepts_title(report, task["task"]["day_report"])


@pytest.mark.integration
def test_late_day_uses_task_window_and_overlapping_activity(monkeypatch):
    fake, _, app = setup_overlapping_activity(monkeypatch, "day_report")
    fake.outputs["coslash_conversation_window"]["items"] = []
    task = payload("day_report")
    task["task"]["day_report"]["window"] = {
        "start": "2026-11-02T08:00:00Z",
        "end": "2026-11-02T12:00:00Z",
    }
    assert dispatch(app, task).status_code == 200
    activity = calls(fake, "coslash_team_activity")
    assert [read["window"] for read in activity] == [
        {"from": "2026-11-02T08:00:00Z", "to": "2026-11-02T12:00:00Z"},
        {"from": "2026-11-01T12:00:00Z", "to": "2026-11-02T12:00:00Z"},
    ]
    report = written(fake)["report"]
    assert not report["quiet"]
    # D-82 replaces the earlier assumption that overlap proves last-hour activity.
    assert report["active"] == []
    assert [row["sessionId"] for row in report["done"]] == ["s2"]
    assert report["windows"]["1h"] == {"active": 0, "done": 1, "people": 1}
    assert report["windows"]["24h"]["sessions"] == 3
    assert len(report["people"]) == 3
