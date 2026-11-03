from datetime import UTC, datetime, timedelta

import pytest

from coslash_agents.runtime.task import deadline


def test_deadline_uses_attempt_limit_and_expiry(task):
    now = datetime(2026, 1, 1, tzinfo=UTC)
    task["task"]["expiresAt"] = (now + timedelta(minutes=10)).isoformat()
    task["mcp"]["expiresAt"] = (now + timedelta(minutes=10)).isoformat()
    assert deadline(task, "http://fake", now) == now + timedelta(minutes=8, seconds=30)
    task["task"]["expiresAt"] = (now + timedelta(minutes=3)).isoformat()
    assert deadline(task, "http://fake", now) == now + timedelta(minutes=2, seconds=30)
    task["task"]["expiresAt"] = now.isoformat()
    with pytest.raises(ValueError):
        deadline(task, "http://fake", now)


def test_deadline_rejects_origin_confusion(task):
    now = datetime(2026, 1, 1, tzinfo=UTC)
    task["task"]["expiresAt"] = (now + timedelta(minutes=10)).isoformat()
    task["mcp"]["expiresAt"] = (now + timedelta(minutes=10)).isoformat()
    for url in (
        "http://fake.attacker/mcp",
        "http://fake:81/mcp",
        "http://user@fake/mcp",
        "http://fake/mcp?token=secret",
    ):
        task["mcp"]["url"] = url
        with pytest.raises(ValueError):
            deadline(task, "http://fake", now)
