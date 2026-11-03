import hmac

from google.auth.transport.requests import Request
from google.oauth2 import id_token


def authenticate(headers, config):
    if config.auth == "dev":
        supplied = headers.get("x-coslash-dev-secret", "")
        return 200 if hmac.compare_digest(supplied, config.dev_secret) else 401
    scheme, _, token = headers.get("authorization", "").partition(" ")
    if scheme.lower() != "bearer" or not token:
        return 401
    try:
        claims = id_token.verify_oauth2_token(token, Request(), audience=config.audience)
    except Exception:
        return 401
    if claims.get("email") != config.invoker_sa or claims.get("email_verified") is not True:
        return 403
    return 200
