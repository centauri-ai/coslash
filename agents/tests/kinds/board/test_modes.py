from datetime import timedelta

import pytest

from coslash_agents.kinds.board.common import iso, timestamp
from tests.kinds.board.support import (
    BOARD,
    calls,
    dispatch,
    last,
    payload,
    response,
    setup,
    written,
)


@pytest.mark.integration
def test_board_modes_and_deferred_change(monkeypatch):
    fake, model, app = setup(monkeypatch)
    task = payload()
    assert dispatch(app, task).status_code == 200
    first = written(fake)
    report = first["report"]
    assert first["insight_mode"] == report["insightMode"] == "llm"
    assert [s["sessionId"] for s in report["active"]] == ["s1"]
    assert [s["sessionId"] for s in report["done"]] == ["s2"]
    assert report["windows"]["1h"] == {"active": 1, "done": 1, "people": 2}
    tick = timestamp(task["task"]["board_tick"]["tick"])
    prior = last(fake, report, first["input_digest"])
    prior["insightsAsOf"] = iso(tick)
    task["task"]["board_tick"]["lastReport"] = prior
    task["task"]["board_tick"]["tick"] = iso(tick + timedelta(minutes=10))
    assert dispatch(app, task).status_code == 200
    assert written(fake)["report"]["insightMode"] == "carry_forward"
    assert written(fake)["report"]["insightsAsOf"] == iso(tick)
    fake.outputs["coslash_team_activity"]["items"][0]["key_decisions"].append("Change")
    task["task"]["board_tick"]["tick"] = iso(tick + timedelta(minutes=20))
    assert dispatch(app, task).status_code == 200
    assert written(fake)["input_digest"] == first["input_digest"]
    assert written(fake)["report"]["insightMode"] == "carry_forward"
    model.replies.append(response(BOARD))
    task["task"]["board_tick"]["tick"] = iso(tick + timedelta(minutes=30))
    assert dispatch(app, task).status_code == 200
    assert written(fake)["report"]["insightMode"] == "llm"
    assert written(fake)["input_digest"] != first["input_digest"]
    assert len(model.calls) == 2
    assert len(calls(fake, "coslash_record_usage")) == 2
    assert all("channel_id" not in x for x in calls(fake, "coslash_conversation_window"))
