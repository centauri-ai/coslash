# Agents runtime

Install Python 3.12 and run `pip install -e '.[dev]'` from this directory. Set
`COSLASH_AGENTS_AUTH=dev`, `COSLASH_CTB_DEV_SECRET` and
`COSLASH_AGENTS_MCP_ORIGIN`, then run `uvicorn coslash_agents.app:app --port 8080`.
Cloud Run instead uses OIDC with `COSLASH_AGENTS_AUDIENCE` and
`COSLASH_AGENTS_INVOKER_SA`. Dev authentication cannot run on Cloud Run.
`COSLASH_AGENTS_STORE` must be `false` outside dev; model requests always use
`store: false`. `OPENAI_API_KEY` supplies the model key, and
`COSLASH_AGENTS_MODEL_BASE_URL` overrides the provider URL for tests.

Each of `COSLASH_AGENTS_{ASK,BOARD,DAY}_{MODEL,EFFORT,SERVICE_TIER,MAX_OUTPUT_TOKENS}`
overrides the default model settings. The registry ships with retryable stubs until
kind implementations are integrated.

Run `ruff check .`, `ruff format --check .`, `python scripts/gen_models.py --check`,
`pytest -q`, and `pytest -m integration`. Start the fake Responses endpoint with
`python -m coslash_agents.tests.fakes.model --port "$AGENT_CANVAS_PORT_BASE"`.
Build the image with `docker build -t coslash-agents:local agents` from the repo root.
Contracts are vendored from the server at the SHA in `contracts/ctb/SOURCE`.

Kinds receive the task envelope, an `MCPClient`, and a UTC work deadline. Treat
all session and Slack text as untrusted, including results whose older schema has
no explicit `untrusted` flag. Quote it with `quote_untrusted` before model use.
Call `record_usage` for successful model results and for `ModelError.usage` after
an attempted request; it never propagates recording failures. The pinned contract
does not yet accept the D-43 `estimated` field, so the server contract must be
updated before estimated usage can be identified at ingestion.
