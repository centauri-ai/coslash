import json
from importlib.resources import files

import pytest
from jsonschema import Draft202012Validator, FormatChecker

from tests.kinds.board.support import dispatch, payload, setup, written


@pytest.mark.parametrize("kind", ["board_tick", "day_report"])
@pytest.mark.parametrize("mode", ["normal", "error"])
def test_produced_report_validates(monkeypatch, kind, mode):
    fake, _, app = setup(monkeypatch, kind=kind, mode=mode)
    assert dispatch(app, payload(kind)).status_code == 200
    schema = json.loads(
        files("coslash_agents.contracts").joinpath("ctb/board-report-1.schema.json").read_text()
    )
    Draft202012Validator(schema, format_checker=FormatChecker()).validate(written(fake)["report"])


def test_prompt_files_match_runtime_prompts():
    from pathlib import Path

    from coslash_agents.kinds.board import PROMPT as board_prompt
    from coslash_agents.kinds.day_report import PROMPT as day_prompt

    kinds = Path(__file__).resolve().parents[2] / "coslash_agents/kinds"
    assert (kinds / "board/prompt.md").read_text().strip() == board_prompt
    assert (kinds / "day_report/prompt.md").read_text().strip() == day_prompt
