import json
from datetime import UTC, datetime, timedelta

import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID
from google.auth import crypt, jwt

from coslash_agents.runtime.auth import authenticate
from coslash_agents.runtime.config import Config


@pytest.fixture(scope="module")
def credentials():
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "test")])
    cert = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(key.public_key())
        .serial_number(1)
        .not_valid_before(datetime.now(UTC) - timedelta(days=1))
        .not_valid_after(datetime.now(UTC) + timedelta(days=1))
        .sign(key, hashes.SHA256())
    )
    private = key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()
    )
    return private, cert.public_bytes(serialization.Encoding.PEM).decode()


def test_oidc_signed_tokens(monkeypatch, credentials):
    private, certificate = credentials

    class Response:
        status = 200
        data = json.dumps({"test": certificate}).encode()

    monkeypatch.setattr(
        "coslash_agents.runtime.auth.Request", lambda: lambda *args, **kwargs: Response()
    )
    config = Config("oidc", "https://agents", "worker@example.com", "https://mcp", "")
    now = 1800000000
    monkeypatch.setattr(
        "google.auth._helpers.utcnow", lambda: datetime.fromtimestamp(now, UTC).replace(tzinfo=None)
    )

    def token(**changes):
        claims = {
            "iss": "https://accounts.google.com",
            "aud": "https://agents",
            "email": "worker@example.com",
            "email_verified": True,
            "iat": now,
            "exp": now + 60,
        }
        claims.update(changes)
        return jwt.encode(crypt.RSASigner.from_string(private, key_id="test"), claims).decode()

    def auth(value):
        return authenticate({"authorization": "Bearer " + value}, config)

    assert auth(token()) == 200
    assert auth(token(exp=now - 10)) == 401
    assert auth(token(aud="wrong")) == 401
    assert auth(token(iss="https://wrong.example")) == 401
    assert auth(token(email="other@example.com")) == 403
    assert auth(token(email_verified=False)) == 403
    other = rsa.generate_private_key(public_exponent=65537, key_size=2048).private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()
    )
    assert (
        auth(
            jwt.encode(
                crypt.RSASigner.from_string(other, key_id="test"),
                {
                    "iss": "https://accounts.google.com",
                    "aud": "https://agents",
                    "exp": now + 60,
                    "iat": now,
                },
            ).decode()
        )
        == 401
    )
    assert authenticate({}, config) == 401


@pytest.fixture
def dev_env(monkeypatch):
    monkeypatch.setenv("COSLASH_AGENTS_AUTH", "dev")
    monkeypatch.setenv("COSLASH_CTB_DEV_SECRET", "local-secret")
    monkeypatch.setenv("COSLASH_AGENTS_MCP_ORIGIN", "https://mcp")
    monkeypatch.delenv("K_SERVICE", raising=False)
    monkeypatch.delenv("COSLASH_ENV", raising=False)


@pytest.mark.parametrize("service", ["agents", ""])
def test_dev_refused_on_cloud_run(monkeypatch, dev_env, service):
    monkeypatch.setenv("K_SERVICE", service)
    with pytest.raises(ValueError, match="Cloud Run"):
        Config.from_env()


@pytest.mark.parametrize("environment", ["prod", "production", "PROD", " Production "])
def test_dev_refused_in_production(monkeypatch, dev_env, environment):
    monkeypatch.setenv("COSLASH_ENV", environment)
    with pytest.raises(ValueError, match="production"):
        Config.from_env()


@pytest.mark.parametrize("environment", ["nonprod", "staging", ""])
def test_dev_allowed_outside_production(monkeypatch, dev_env, environment):
    monkeypatch.setenv("COSLASH_ENV", environment)
    config = Config.from_env()
    assert authenticate({"x-coslash-dev-secret": "local-secret"}, config) == 200
    assert authenticate({"x-coslash-dev-secret": "wrong"}, config) == 401


def test_oidc_allowed_in_production(monkeypatch, dev_env):
    monkeypatch.setenv("COSLASH_ENV", "prod")
    monkeypatch.setenv("COSLASH_AGENTS_AUTH", "oidc")
    monkeypatch.setenv("COSLASH_AGENTS_AUDIENCE", "https://agents")
    monkeypatch.setenv("COSLASH_AGENTS_INVOKER_SA", "worker@example.com")
    monkeypatch.setenv("COSLASH_AGENTS_STORE", "false")
    assert Config.from_env().auth == "oidc"
