import copy

import pytest

from tests.kinds.board.support import BOARD, calls, dispatch, payload, setup, written

SESSION = {"kind": "session", "sessionId": "s1"}
SLACK = {"kind": "slack", "channelId": "C1", "ts": "100.1"}
BOT = {"kind": "slack", "channelId": "C1", "ts": "100.2"}
SESSION_EVIDENCE = {"ref": SESSION, "quote": "Use REST"}
SLACK_EVIDENCE = {"ref": SLACK, "quote": "Team update"}


@pytest.mark.integration
@pytest.mark.parametrize(
    ("issue_type", "evidence", "expected"),
    [
        ("mismatch", [SESSION_EVIDENCE, SLACK_EVIDENCE], "high"),
        ("mismatch", [{"ref": SESSION, "quote": "Outcome detail"}, SLACK_EVIDENCE], "high"),
        ("mismatch", [], "medium"),
        ("mismatch", None, "medium"),
        ("mismatch", [SESSION_EVIDENCE], "medium"),
        ("mismatch", [SESSION_EVIDENCE, {"ref": SLACK, "quote": "fabricated"}], "medium"),
        ("mismatch", [{"ref": SESSION, "quote": "Team update"}, SLACK_EVIDENCE], "medium"),
        ("mismatch", [{"ref": SESSION, "quote": "use rest"}, SLACK_EVIDENCE], "medium"),
        ("mismatch", [{"ref": SESSION, "quote": "Build API"}, SLACK_EVIDENCE], "medium"),
        ("mismatch", [{"ref": SESSION, "quote": " "}, SLACK_EVIDENCE], "medium"),
        (
            "mismatch",
            [SESSION_EVIDENCE, SLACK_EVIDENCE, {"ref": SLACK, "quote": "fabricated"}],
            "medium",
        ),
        ("mismatch", [SESSION_EVIDENCE, {"ref": BOT, "quote": "Bot update"}], "medium"),
        (
            "mismatch",
            [{"ref": {"kind": "session", "sessionId": "s3"}, "quote": "Use REST"}, SLACK_EVIDENCE],
            "medium",
        ),
        ("discussed_not_started", [SESSION_EVIDENCE, SLACK_EVIDENCE], "medium"),
    ],
    ids=[
        "decision",
        "outcome",
        "none",
        "omitted",
        "missing-side",
        "fabricated-slack",
        "wrong-source",
        "case-sensitive",
        "goal-not-evidence",
        "blank",
        "extra-fabricated",
        "bot",
        "uncited-session",
        "other-type",
    ],
)
def test_high_confidence_requires_verified_cited_quotes(
    monkeypatch, caplog, issue_type, evidence, expected
):
    output = copy.deepcopy(BOARD)
    issue = {
        "type": issue_type,
        "confidence": "high",
        "title": "Updates differ",
        "detail": "Check the updates",
        "refs": [SESSION, SLACK],
    }
    if evidence is not None:
        issue["evidence"] = evidence
    output["issues"] = [issue]
    fake, _, app = setup(monkeypatch, content=output)
    sessions = fake.outputs["coslash_team_activity"]["items"]
    sessions[0]["outcome"] = "Outcome detail"
    sessions[2]["outcome"] = "Use REST"
    fake.outputs["coslash_list_team_issues"]["items"] = [
        {
            "id": "issue-1",
            "type": "mismatch",
            "status": "open",
            "confidence": "medium",
            "title": "Stored issue",
            "refs": [SESSION, SLACK],
            "first_seen": "2026-11-02T12:00:00Z",
            "last_seen": "2026-11-02T12:00:00Z",
            "can_close": True,
        }
    ]
    caplog.set_level("INFO")

    assert dispatch(app, payload()).status_code == 200
    arguments = calls(fake, "coslash_upsert_issues")[0]
    assert arguments["issues"] == [
        {
            key: (expected if key == "confidence" else issue[key])
            for key in ("type", "confidence", "title", "detail", "refs")
        }
    ]
    assert "evidence" not in str(arguments)
    assert written(fake)["report"]["issues"][0]["id"] == "issue-1"
    assert "evidence" not in str(written(fake)["report"])
    assert "Outcome detail" not in caplog.text
