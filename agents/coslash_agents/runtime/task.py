import json
from datetime import UTC, datetime, timedelta
from importlib.resources import files
from urllib.parse import urlsplit

from jsonschema import Draft202012Validator, FormatChecker


SCHEMA = json.loads(
    files("coslash_agents.contracts").joinpath("ctb/agent-task-1.schema.json").read_text()
)
VALIDATOR = Draft202012Validator(SCHEMA, format_checker=FormatChecker())
ATTEMPTS = {"board_tick": 540, "day_report": 900, "ask": 100}


def origin(url):
    value = urlsplit(url)
    if (
        value.scheme not in {"http", "https"}
        or not value.hostname
        or value.username
        or value.password
        or value.query
        or value.fragment
    ):
        raise ValueError("invalid origin")
    port = value.port or (443 if value.scheme == "https" else 80)
    return value.scheme, value.hostname.lower(), port


def deadline(payload, expected_origin, received=None):
    if not VALIDATOR.is_valid(payload):
        raise ValueError("invalid task")
    now = received or datetime.now(UTC)
    task = payload["task"]
    expires = datetime.fromisoformat(task["expiresAt"].replace("Z", "+00:00"))
    token_expires = datetime.fromisoformat(payload["mcp"]["expiresAt"].replace("Z", "+00:00"))
    if expires <= now or token_expires <= now:
        raise ValueError("expired")
    if origin(payload["mcp"]["url"]) != origin(expected_origin):
        raise ValueError("wrong origin")
    margin = 10 if task["kind"] == "ask" else 30
    work_deadline = min(
        expires, token_expires, now + timedelta(seconds=ATTEMPTS[task["kind"]])
    ) - timedelta(seconds=margin)
    if work_deadline <= now:
        raise ValueError("expired")
    return work_deadline
