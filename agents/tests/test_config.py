import pytest
from coslash_agents.runtime.config import Config


def test_store_refused_outside_dev(monkeypatch):
    monkeypatch.setenv("COSLASH_AGENTS_STORE", "true")
    monkeypatch.setenv("COSLASH_AGENTS_AUTH", "oidc")
    with pytest.raises(ValueError, match="STORE"):
        Config.from_env()
