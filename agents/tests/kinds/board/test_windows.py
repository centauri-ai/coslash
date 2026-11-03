import pytest

from coslash_agents.kinds.board.common import digest
from tests.kinds.board.support import calls, dispatch, payload, setup_overlapping_activity, written


@pytest.mark.integration
def test_late_board_uses_task_window_and_overlapping_activity(monkeypatch):
    fake, model, app = setup_overlapping_activity(monkeypatch, "board_tick")
    task = payload()
    task["budget"]["state"] = "soft_limit"
    assert dispatch(app, task).status_code == 200
    activity = calls(fake, "coslash_team_activity")
    assert [read["window"] for read in activity] == [
        {"from": "2026-11-02T11:00:00Z", "to": "2026-11-02T12:00:00Z"},
        {"from": "2026-11-01T12:00:00Z", "to": "2026-11-02T12:00:00Z"},
    ]
    report = written(fake)["report"]
    # D-82 replaces the earlier assumption that overlap proves last-hour activity.
    assert report["active"] == []
    assert [row["sessionId"] for row in report["done"]] == ["s2"]
    assert report["windows"]["1h"] == {"active": 0, "done": 1, "people": 1}
    assert report["windows"]["24h"]["sessions"] == 3
    assert report["windows"]["24h"]["done"] == 1
    assert not model.calls


def test_digest_does_not_count_sessions_ending_after_the_window():
    end = "2026-11-02T12:00:00Z"
    session = {"session_id": "s1", "ended_at": "2026-11-02T12:30:00Z"}
    assert digest([session], [], end) == digest([{"session_id": "s1"}], [], end)
    session["ended_at"] = end
    assert digest([session], [], end) != digest([{"session_id": "s1"}], [], end)


@pytest.mark.parametrize("kind", ["board_tick", "day_report"])
@pytest.mark.parametrize(
    ("last_activity", "last_hour"),
    [
        ("2026-11-02T10:59:59Z", False),
        ("2026-11-02T11:00:00Z", True),
        ("2026-11-02T12:00:00Z", True),
        ("2026-11-02T12:00:01Z", False),
        ("2026-11-02T15:00:00Z", False),
    ],
)
def test_last_hour_requires_observed_activity_within_window(
    monkeypatch, kind, last_activity, last_hour
):
    fake, _, app = setup_overlapping_activity(monkeypatch, kind)
    fake.outputs["coslash_team_activity"]["items"][0]["last_activity_at"] = last_activity
    task = payload(kind)
    if kind == "board_tick":
        task["budget"]["state"] = "soft_limit"
    else:
        task["task"]["day_report"]["window"] = {
            "start": "2026-11-02T08:00:00Z",
            "end": "2026-11-02T12:00:00Z",
        }
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["windows"]["1h"]["active"] == int(last_hour)
    assert all(row["lastActivityAt"] <= "2026-11-02T12:00:00Z" for row in report["active"])
    assert report["windows"]["24h"]["sessions"] == 3


@pytest.mark.parametrize("kind", ["board_tick", "day_report"])
def test_future_card_content_is_not_used_or_counted_as_last_hour(monkeypatch, kind):
    import json

    fake, model, app = setup_overlapping_activity(monkeypatch, kind)
    future = fake.outputs["coslash_team_activity"]["items"][0]
    future.update(
        {
            "goal": "future secret goal",
            "outcome": "future secret outcome",
            "key_decisions": ["future secret decision"],
            "next_step": "future secret next",
        }
    )
    task = payload(kind)
    if kind == "day_report":
        task["task"]["day_report"]["window"] = {
            "start": "2026-11-02T08:00:00Z",
            "end": "2026-11-02T12:00:00Z",
        }
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert report["windows"]["1h"]["active"] == 0
    assert report["windows"]["24h"]["sessions"] == 3
    assert "future secret" not in json.dumps(report)
    assert "future secret" not in json.dumps(model.calls)
    assert "s1" not in [row["sessionId"] for row in report["active"]]
    assert all(item["source"].get("sessionId") != "s1" for item in report["decisions"])
    if kind == "board_tick":
        assert all("s1" not in theme["sessionIds"] for theme in report["themes"])
    else:
        assert not report["moved"]


def test_card_updated_after_window_is_not_sent_to_model(monkeypatch):
    import json

    fake, model, app = setup_overlapping_activity(monkeypatch, "board_tick")
    item = fake.outputs["coslash_team_activity"]["items"][0]
    item.update(
        {
            "last_activity_at": "2026-11-02T11:45:00Z",
            "card_at": "2026-11-02T12:01:00Z",
            "goal": "future card content",
        }
    )
    assert dispatch(app, payload()).status_code == 200
    report = written(fake)["report"]
    assert any(row["sessionId"] == "s1" for row in report["active"])
    assert "future card content" not in json.dumps(report)
    assert "future card content" not in json.dumps(model.calls)


def test_overlap_alone_keeps_day_nonquiet_without_inventing_activity(monkeypatch):
    fake, model, app = setup_overlapping_activity(monkeypatch, "day_report")
    fake.outputs["coslash_team_activity"]["items"] = fake.outputs["coslash_team_activity"]["items"][
        :1
    ]
    fake.outputs["coslash_conversation_window"]["items"] = []
    task = payload("day_report")
    task["task"]["day_report"]["window"] = {
        "start": "2026-11-02T08:00:00Z",
        "end": "2026-11-02T12:00:00Z",
    }
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert not report["quiet"] and report["windows"]["24h"]["sessions"] == 1
    assert report["windows"]["1h"]["active"] == 0 and report["active"] == []
    assert report["moved"] == report["decisions"] == []
    assert len(model.calls) == 1
