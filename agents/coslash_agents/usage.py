import structlog

from coslash_agents.mcp.client import TOOLS


async def record_usage(mcp, model, effort, service_tier, usage):
    try:
        extra = {}
        if "estimated" in TOOLS["coslash_record_usage"]["inputSchema"]["properties"]:
            extra["estimated"] = usage.estimated
        result = await mcp.coslash_record_usage(
            feature="board",
            model=model,
            effort=effort,
            service_tier=service_tier,
            input_tokens=usage.input_tokens,
            cached_input_tokens=usage.cached_input_tokens,
            output_tokens=usage.output_tokens,
            reasoning_tokens=usage.reasoning_tokens,
            **extra,
        )
        return result.soft_limit_pct
    except Exception:
        structlog.get_logger().info("usage_failed", task_id=mcp.task_id, kind=mcp.kind)
        return None
