from datetime import UTC, datetime, timedelta

import httpx
import pytest
import structlog

from coslash_agents.llm.client import ModelClient, ModelError
from coslash_agents.logging import assert_content_free
from coslash_agents.tests.fakes.model import FakeModel


async def test_no_content_or_secrets_in_logs():
    fake = FakeModel(mode="error")
    with structlog.testing.capture_logs() as logs:
        async with ModelClient(
            "ask",
            "task-1",
            "team-1",
            datetime.now(UTC) + timedelta(minutes=2),
            api_key="private-key",
            base_url="http://fake/v1",
            transport=httpx.ASGITransport(app=fake.app),
        ) as client:
            with pytest.raises(ModelError):
                await client.run(
                    [{"role": "user", "content": "canary-private-content synthetic-token"}],
                    {"type": "object"},
                )
    assert_content_free(str(logs), ["canary-private-content", "synthetic-token", "private-key"])
    assert logs[0]["event"] == "llm_failed"
