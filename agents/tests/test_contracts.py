import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker


def test_vendored_task_examples():
    root = Path(__file__).resolve().parents[1] / "contracts/ctb"
    validator = Draft202012Validator(
        json.loads((root / "agent-task-1.schema.json").read_text()), format_checker=FormatChecker()
    )
    fixtures = Path(__file__).parent / "fakes/fixtures/ctb-agents-1"
    for name in ("ask", "board_tick", "day_report"):
        assert validator.is_valid(json.loads((fixtures / f"{name}.json").read_text()))
        assert not validator.is_valid(
            json.loads((root / "examples" / f"invalid_{name}.json").read_text())
        )
