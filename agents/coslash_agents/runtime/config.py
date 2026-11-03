from dataclasses import dataclass
import os


@dataclass(frozen=True)
class Config:
    auth: str
    audience: str
    invoker_sa: str
    mcp_origin: str
    dev_secret: str

    @classmethod
    def from_env(cls):
        auth = os.getenv("COSLASH_AGENTS_AUTH", "oidc")
        if auth not in {"oidc", "dev"}:
            raise ValueError("invalid authentication mode")
        if auth == "dev":
            if "K_SERVICE" in os.environ:
                raise ValueError("dev authentication is unavailable on Cloud Run")
            if os.getenv("COSLASH_ENV", "").strip().lower() in {"prod", "production"}:
                raise ValueError("dev authentication is unavailable in production")
        if auth != "dev" and os.getenv("COSLASH_AGENTS_STORE", "false") != "false":
            raise ValueError("COSLASH_AGENTS_STORE must be false")
        config = cls(
            auth=auth,
            audience=os.getenv("COSLASH_AGENTS_AUDIENCE", ""),
            invoker_sa=os.getenv("COSLASH_AGENTS_INVOKER_SA", ""),
            mcp_origin=os.getenv("COSLASH_AGENTS_MCP_ORIGIN", ""),
            dev_secret=os.getenv("COSLASH_CTB_DEV_SECRET", ""),
        )
        if not config.mcp_origin:
            raise ValueError("MCP origin is required")
        if auth == "dev" and not config.dev_secret:
            raise ValueError("dev secret is required")
        if auth == "oidc" and (not config.audience or not config.invoker_sa):
            raise ValueError("OIDC audience and invoker are required")
        return config
