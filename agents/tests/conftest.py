import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest


@pytest.fixture
def task():
    path = Path(__file__).parent / "fakes/fixtures/ctb-agents-1/board_tick.json"
    value = json.loads(path.read_text())
    expires = (datetime.now(UTC) + timedelta(minutes=10)).isoformat()
    value["task"]["expiresAt"] = expires
    value["mcp"]["expiresAt"] = expires
    value["mcp"]["url"] = "http://fake/mcp"
    return value
