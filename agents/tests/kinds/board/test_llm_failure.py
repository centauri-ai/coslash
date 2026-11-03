import pytest

from tests.kinds.board.support import calls, dispatch, payload, setup, written


@pytest.mark.parametrize(
    "mode,content", [("error", None), ("timeout", None), ("normal", {"invalid": True})]
)
def test_model_failure_writes_carry_forward(monkeypatch, caplog, mode, content):
    caplog.set_level("INFO")
    fake, _, app = setup(monkeypatch, content=content, mode=mode)
    assert dispatch(app, payload()).status_code == 200
    assert written(fake)["report"]["insightMode"] == "carry_forward"
    assert len(calls(fake, "coslash_record_usage")) == 1
    assert "llm_failed" in caplog.text


def test_failure_keeps_previous_insights_and_digest(monkeypatch):
    from tests.kinds.board.support import last

    fake, model, app = setup(monkeypatch)
    task = payload()
    assert dispatch(app, task).status_code == 200
    first = written(fake)
    task["task"]["board_tick"]["lastReport"] = last(fake, first["report"], first["input_digest"])
    task["task"]["board_tick"]["lastReport"]["insightsAsOf"] = "2026-11-02T11:00:00Z"
    fake.outputs["coslash_team_activity"]["items"][0]["key_decisions"].append("new")
    model.mode = "error"
    assert dispatch(app, task).status_code == 200
    result = written(fake)
    assert (
        result["insight_mode"] == "carry_forward"
        and result["input_digest"] == first["input_digest"]
    )
    for key in ("headline", "themes", "decisions"):
        assert result["report"][key] == first["report"][key]
    assert result["report"]["insightsAsOf"] == "2026-11-02T11:00:00Z"
