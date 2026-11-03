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
    assert [row["sessionId"] for row in report["active"]] == ["s1"]
    assert [row["sessionId"] for row in report["done"]] == ["s2"]
    assert report["windows"]["1h"] == {"active": 1, "done": 1, "people": 2}
    assert report["windows"]["24h"]["sessions"] == 3
    assert report["windows"]["24h"]["done"] == 1
    assert not model.calls


def test_digest_does_not_count_sessions_ending_after_the_window():
    end = "2026-11-02T12:00:00Z"
    session = {"session_id": "s1", "ended_at": "2026-11-02T12:30:00Z"}
    assert digest([session], [], end) == digest([{"session_id": "s1"}], [], end)
    session["ended_at"] = end
    assert digest([session], [], end) != digest([{"session_id": "s1"}], [], end)
