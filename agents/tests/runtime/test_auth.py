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


def test_dev_refused_on_cloud_run(monkeypatch):
    monkeypatch.setenv("COSLASH_AGENTS_AUTH", "dev")
    monkeypatch.setenv("K_SERVICE", "agents")
    with pytest.raises(ValueError):
        Config.from_env()
