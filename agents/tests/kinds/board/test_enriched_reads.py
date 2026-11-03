import copy
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest
from jsonschema import Draft202012Validator, ValidationError

import coslash_agents.app as app_module
import coslash_agents.kinds.board as board
import coslash_agents.kinds.day_report as day
import coslash_agents.llm.client as llm
import coslash_agents.mcp.client as mcp_module
from coslash_agents.mcp.client import MCPClient
from coslash_agents.safety import SCHEMA as REPORT_SCHEMA
from coslash_agents.tests.fakes.mcp import FakeMCP
from tests.kinds.board import support

READ_EXAMPLE = json.loads((Path(__file__).parent / "fixtures/team-board-read.json").read_text())
SESSION = {"kind": "session", "sessionId": "session-1"}
SLACK = {"kind": "slack", "channelId": "channel-1", "ts": "1.2"}


class FrozenDateTime(datetime):
    @classmethod
    def now(cls, tz=None):
        return cls(2026, 11, 2, 12, tzinfo=UTC).astimezone(tz)


@pytest.fixture
def frozen(monkeypatch):
    for module in (support, app_module, board, day, llm, mcp_module):
        monkeypatch.setattr(module, "datetime", FrozenDateTime)


def enriched_read(kind):
    example = copy.deepcopy(READ_EXAMPLE)
    report = example["report"]
    session = report["decisions"][0]["source"]
    slack = report["issues"][0]["refs"][0]
    report["decisions"].append({"text": "Team agreed", "source": copy.deepcopy(slack)})
    report["issues"][0]["refs"].append(copy.deepcopy(session))
    if kind == "day_report":
        day_example = json.loads(
            (Path(__file__).parents[3] / "contracts/ctb/examples/day_2_of_3.json").read_text()
        )
        day_example["active"] = report["active"]
        day_example["done"] = report["done"]
        day_example["decisions"] = report["decisions"]
        day_example["issues"] = report["issues"]
        day_example["moved"][0]["refs"] = [copy.deepcopy(session), copy.deepcopy(slack)]
        example["report"] = day_example
    return example


@pytest.mark.parametrize("kind", ["board_tick", "day_report"])
@pytest.mark.integration
async def test_previous_report_strips_enrichment_from_every_position(kind, frozen):
    fake = FakeMCP(kind=kind)
    example = enriched_read(kind)
    fake.outputs["coslash_get_team_board"] = example
    with pytest.raises(ValidationError):
        Draft202012Validator(REPORT_SCHEMA).validate(example["report"])
    async with MCPClient(
        "http://fake/mcp",
        "synthetic-token",
        "task-1",
        kind,
        FrozenDateTime.now(UTC) + timedelta(minutes=10),
        transport=httpx.ASGITransport(app=fake.app),
    ) as client:
        result = await board.previous_report(
            client, "org:team-1", "board" if kind == "board_tick" else kind
        )
    assert result["decisions"][0]["source"] == SESSION
    assert result["decisions"][1]["source"] == SLACK
    assert result["issues"][0]["refs"] == [SLACK, SESSION]
    assert result["issues"][0]["title"] == "Review"
    assert all(
        "hubUrl" not in item and "title" not in item for item in result["active"] + result["done"]
    )
    if kind == "day_report":
        assert result["moved"][0]["refs"] == [SESSION, SLACK]
    Draft202012Validator(REPORT_SCHEMA).validate(result)
    assert support.calls(fake, "coslash_get_team_board")


@pytest.mark.parametrize(
    "budget,mode", [("soft_limit", "budget_fallback"), ("ok", "carry_forward")]
)
@pytest.mark.integration
def test_enriched_previous_board_survives_next_tick(monkeypatch, frozen, budget, mode):
    fake, model, app = support.setup(monkeypatch)
    example = enriched_read("board_tick")
    fake.outputs["coslash_get_team_board"] = example
    fake.outputs["coslash_team_activity"]["items"][0]["session_id"] = "session-1"
    message = fake.outputs["coslash_conversation_window"]["items"][0]
    message.update({"channel_id": "channel-1", "ts": "1.2"})
    issue = copy.deepcopy(example["report"]["issues"][0])
    issue.update(
        {
            "status": "open",
            "first_seen": "2026-11-02T12:00:00Z",
            "last_seen": "2026-11-02T12:00:00Z",
            "can_close": True,
        }
    )
    fake.outputs["coslash_list_team_issues"]["items"] = [issue]
    task = support.payload()
    task["budget"]["state"] = budget
    task["task"]["board_tick"]["lastReport"] = {
        "id": "previous",
        "inputDigest": "old",
        "insightsAsOf": "2026-11-02T12:00:00Z",
    }
    assert support.dispatch(app, task).status_code == 200
    assert len(support.calls(fake, "coslash_get_team_board")) == 1
    report = support.written(fake)["report"]
    assert report["insightMode"] == mode
    assert [item["source"] for item in report["decisions"]] == [SESSION, SLACK]
    assert report["issues"][0]["refs"] == [SLACK, SESSION]
    assert not model.calls
    Draft202012Validator(REPORT_SCHEMA).validate(report)


@pytest.mark.integration
def test_final_day_report_reads_enriched_prior_reports(monkeypatch, frozen):
    fake, _, app = support.setup(monkeypatch, kind="day_report")
    fake.outputs["coslash_get_team_board"] = enriched_read("day_report")
    task = support.payload("day_report")
    task["task"]["day_report"]["slot"] = "2026-11-02#3/3"
    assert support.dispatch(app, task).status_code == 200
    assert len(support.calls(fake, "coslash_get_team_board")) == 2
    report = support.written(fake)["report"]
    assert report["totals"]["decisions"] == 5
    Draft202012Validator(REPORT_SCHEMA).validate(report)
