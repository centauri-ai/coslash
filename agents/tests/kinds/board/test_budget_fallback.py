from tests.kinds.board.support import calls, dispatch, last, payload, setup, written


def test_soft_limit_freezes_insights_without_model_or_usage(monkeypatch):
    fake, model, app = setup(monkeypatch)
    task = payload()
    assert dispatch(app, task).status_code == 200
    initial = written(fake)
    report = initial["report"]
    task["task"]["board_tick"]["lastReport"] = last(fake, report, initial["input_digest"])
    task["budget"]["state"] = "soft_limit"
    fake.calls.clear()
    model.calls.clear()
    assert dispatch(app, task).status_code == 200
    result = written(fake)
    assert result["insight_mode"] == "budget_fallback"
    for key in ("themes", "decisions", "headline", "insightsAsOf"):
        assert result["report"][key] == report[key]
    assert result["input_digest"] == initial["input_digest"]
    assert not model.calls and not calls(fake, "coslash_record_usage")


def test_soft_limit_without_previous_report(monkeypatch):
    fake, model, app = setup(monkeypatch)
    task = payload()
    task["budget"]["state"] = "soft_limit"
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert (report["themes"], report["decisions"], report["headline"]) == ([], [], "")
    assert report["insightsAsOf"] == task["task"]["board_tick"]["tick"]
    assert not model.calls and not calls(fake, "coslash_record_usage")


def test_deterministic_caps_preserve_counts_and_truncation(monkeypatch):
    fake, _, app = setup(monkeypatch)
    task = payload()
    task["budget"]["state"] = "soft_limit"
    item = fake.outputs["coslash_team_activity"]["items"][0]
    item["goal"] = "x" * 130
    fake.outputs["coslash_team_activity"]["items"].extend(
        [{**item, "session_id": f"extra-{n}"} for n in range(17)]
    )
    assert dispatch(app, task).status_code == 200
    report = written(fake)["report"]
    assert len(report["active"]) == 15 and report["windows"]["1h"]["active"] == 18
    assert len(report["active"][0]["line"]) == 120
    assert {"field": "active", "count": 3} in report["truncated"]
    assert any(t["field"] == "active.line" and t["count"] == 10 for t in report["truncated"])


def test_truncated_decision_count_survives_carry_forward(monkeypatch):
    fake, _, app = setup(monkeypatch)
    task = payload()
    assert dispatch(app, task).status_code == 200
    initial = written(fake)
    report = initial["report"]
    report["truncated"] = [{"field": "decisions", "count": 4}]
    task["task"]["board_tick"]["lastReport"] = last(fake, report, initial["input_digest"])
    task["budget"]["state"] = "soft_limit"
    assert dispatch(app, task).status_code == 200
    result = written(fake)["report"]
    assert result["windows"]["24h"]["decisions"] == 5
    assert {"field": "decisions", "count": 4} in result["truncated"]
