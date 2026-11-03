import asyncio
import hashlib
import time
from collections import OrderedDict
from contextlib import asynccontextmanager
from datetime import UTC, datetime

import structlog
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Route

from coslash_agents.kinds import REGISTRY
from coslash_agents.logging import configure_logging
from coslash_agents.mcp.client import MCPClient, TaskExpired, TaskForbidden
from coslash_agents.runtime.auth import authenticate
from coslash_agents.runtime.config import Config
from coslash_agents.runtime.task import deadline


async def health(request):
    return JSONResponse({"status": "ok"})


async def dispatch(request: Request):
    started = time.monotonic()
    received = datetime.now(UTC)
    config = request.app.state.config
    auth = await asyncio.to_thread(authenticate, request.headers, config)
    if auth != 200:
        return JSONResponse({"error": "unauthorized"}, status_code=auth)
    try:
        payload = await request.json()
        work_deadline = deadline(payload, config.mcp_origin, received)
    except (ValueError, TypeError, KeyError):
        return JSONResponse({"error": "invalid_task"}, status_code=409)
    task = payload["task"]
    registration = REGISTRY[task["kind"]]
    cache = request.app.state.invalid_tasks
    key = hashlib.sha256(
        (task["orgId"] + "\0" + task["id"] + "\0" + payload["mcp"]["token"]).encode()
    ).digest()
    if key in cache:
        if cache[key] > received:
            return JSONResponse({"error": "invalid_task"}, status_code=409)
        del cache[key]
    try:
        async with asyncio.timeout((work_deadline - datetime.now(UTC)).total_seconds()):
            async with MCPClient(
                payload["mcp"]["url"],
                payload["mcp"]["token"],
                task["id"],
                task["kind"],
                work_deadline,
            ) as mcp:
                await mcp.initialize()
                await registration.handler(payload, mcp, work_deadline)
        structlog.get_logger().info(
            "task_finished",
            task_id=task["id"],
            kind=task["kind"],
            org_id=task["orgId"],
            latency_ms=round((time.monotonic() - started) * 1000),
        )
        return JSONResponse({"status": "done"})
    except (TaskForbidden, TaskExpired):
        cache[key] = datetime.fromisoformat(task["expiresAt"].replace("Z", "+00:00"))
        cache.move_to_end(key)
        if len(cache) > 4096:
            cache.popitem(last=False)
        return JSONResponse({"error": "invalid_task"}, status_code=409)
    except NotImplementedError:
        return JSONResponse({"error": "kind_not_implemented"}, status_code=503)
    except Exception:
        structlog.get_logger().info(
            "task_failed", task_id=task["id"], kind=task["kind"], org_id=task["orgId"]
        )
        return JSONResponse({"error": "unavailable"}, status_code=503)


def create_app(config=None):
    @asynccontextmanager
    async def lifespan(app):
        configure_logging()
        app.state.config = config or Config.from_env()
        app.state.invalid_tasks = OrderedDict()
        yield

    return Starlette(
        routes=[Route("/healthz", health), Route("/v1/tasks", dispatch, methods=["POST"])],
        lifespan=lifespan,
    )


app = create_app()
