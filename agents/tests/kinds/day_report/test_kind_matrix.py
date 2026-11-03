import pytest

from tests.kinds.board.support import calls, dispatch, payload, setup, written


@pytest.mark.parametrize("mode", ["normal", "error", "timeout"])
def test_day_never_upserts_issues(monkeypatch, mode):
    fake, _, app = setup(monkeypatch, kind="day_report", mode=mode)
    assert dispatch(app, payload("day_report")).status_code == 200
    assert not calls(fake, "coslash_upsert_issues")
    if mode != "normal":
        report = written(fake)["report"]
        assert report["insightMode"] == "carry_forward"
        assert report["moved"] == report["decisions"] == []
        assert len(calls(fake, "coslash_record_usage")) == 1
