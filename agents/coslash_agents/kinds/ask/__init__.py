import asyncio
import html
import json
import re
import time
from datetime import UTC, datetime
from importlib.resources import files

import httpx
import structlog
from jsonschema import Draft202012Validator

from coslash_agents.kinds.ask.copy import FAILED, NOT_FOUND, TIMEOUT
from coslash_agents.kinds.ask.injection import remember_instructions, unsafe_answer
from coslash_agents.llm.client import ModelClient, Usage
from coslash_agents.mcp.client import TOOLS, TaskExpired, TaskForbidden, ToolError
from coslash_agents.safety import quote_untrusted, strip_slack_controls, validate_refs
from coslash_agents.usage import record_usage

PROMPT = files(__package__).joinpath("prompt.md").read_text(encoding="utf-8").strip()
READS = frozenset(
    {
        "coslash_get_team",
        "coslash_list_sessions",
        "coslash_search_sessions",
        "coslash_get_session",
        "coslash_get_session_section",
        "coslash_team_activity",
        "coslash_get_team_board",
        "coslash_list_team_issues",
        "coslash_list_team_rules",
        "coslash_conversation_window",
        "coslash_conversation_search",
    }
)
REF = {
    "anyOf": [
        {
            "type": "object",
            "properties": {
                "kind": {"type": "string", "enum": ["session"]},
                "sessionId": {"type": "string"},
            },
            "required": ["kind", "sessionId"],
            "additionalProperties": False,
        },
        {
            "type": "object",
            "properties": {
                "kind": {"type": "string", "enum": ["slack"]},
                "channelId": {"type": "string"},
                "ts": {"type": "string"},
            },
            "required": ["kind", "channelId", "ts"],
            "additionalProperties": False,
        },
    ]
}
SCHEMA = {
    "type": "object",
    "properties": {
        "text": {"type": "string"},
        "citations": {"type": "array", "items": REF},
        "found": {"type": "boolean"},
    },
    "required": ["text", "citations", "found"],
    "additionalProperties": False,
}


def tools_for_model():
    result = []
    for name in sorted(READS):
        tool = TOOLS[name]
        schema = json.loads(json.dumps(tool["inputSchema"]))
        schema.get("properties", {}).pop("task_id", None)
        if "required" in schema:
            schema["required"] = [key for key in schema["required"] if key != "task_id"]
        result.append(
            {
                "type": "function",
                "name": name,
                "description": tool.get("description", ""),
                "parameters": schema,
            }
        )
    return result


def seen_in(name, data):
    refs = []

    def walk(value):
        if isinstance(value, list):
            for item in value:
                walk(item)
        elif isinstance(value, dict):
            if value.get("is_bot") is True:
                return
            if value.get("is_bot") is False and value.get("channel_id") and value.get("ts"):
                refs.append({"kind": "slack", "channelId": value["channel_id"], "ts": value["ts"]})
            session_id = value.get("session_id") or value.get("sessionId")
            if isinstance(session_id, str):
                refs.append({"kind": "session", "sessionId": session_id})
            for key, item in value.items():
                if key == "sessionIds" and isinstance(item, list):
                    refs.extend(
                        {"kind": "session", "sessionId": s} for s in item if isinstance(s, str)
                    )
                else:
                    walk(item)

    walk(data)
    if name in {"coslash_list_sessions", "coslash_search_sessions"}:
        for item in data.get("items", []):
            if isinstance(item, dict) and isinstance(item.get("id"), str):
                refs.append({"kind": "session", "sessionId": item["id"]})
    return refs


def clean_text(text):
    for _ in range(3):
        text = html.unescape(text)
    text = strip_slack_controls(text)
    text = re.sub(r"(?<![\w@])@[\w.-]+", "", text)
    text = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text)
    text = re.sub(r"https?://\S+|www\.\S+|<[^>]*>|[\x00-\x1f\x7f]|[*_~`]", "", text)
    text = " ".join(text.split())
    if len(text) > 900:
        text = text[:901].rsplit(" ", 1)[0] if " " in text[:901] else text[:900]
    return text.strip()


async def ask_model(task, mcp, deadline, started, seen, context, passages):
    details = task["task"]
    question = details["ask"]
    messages = [
        {"role": "system", "content": PROMPT},
        {
            "role": "user",
            "content": quote_untrusted(
                json.dumps(
                    {"question": question["question"], "thread": context}, ensure_ascii=False
                )
            ),
        },
    ]
    count = 0
    async with ModelClient("ask", details["id"], details["orgId"], deadline) as client:
        config = client.config
        tier = config.service_tier
        while True:
            enabled = count < 6 and time.monotonic() - started < 60
            body = {
                "model": config.model,
                "input": messages,
                "store": False,
                "include": ["reasoning.encrypted_content"],
                "reasoning": {"effort": config.effort},
                "max_output_tokens": config.max_output_tokens,
                "text": {
                    "format": {
                        "type": "json_schema",
                        "name": "result",
                        "strict": True,
                        "schema": SCHEMA,
                    }
                },
                "tools": tools_for_model() if enabled else [],
            }
            if tier and tier != "default":
                body["service_tier"] = tier
            timeout = max(0.001, (60 if enabled else 80) - (time.monotonic() - started))
            usage = Usage()
            try:
                response = await asyncio.wait_for(
                    client.http.post("responses", json=body, timeout=timeout), timeout
                )
            except (httpx.HTTPError, TimeoutError, asyncio.CancelledError) as exc:
                usage.add(None, body, config.max_output_tokens)
                await record_usage(mcp, config.model, config.effort, tier, usage)
                if isinstance(exc, asyncio.CancelledError):
                    raise
                if isinstance(exc, (TimeoutError, httpx.TimeoutException)):
                    if enabled:
                        continue
                    raise TimeoutError from None
                raise ValueError("model request failed") from None
            if response.status_code == 400 and tier == "fast":
                try:
                    error = response.json().get("error", {})
                    fallback = "service_tier" in str(
                        error.get("param", "")
                    ) or "service_tier" in str(error.get("message", ""))
                except (ValueError, TypeError, AttributeError):
                    fallback = False
                if fallback:
                    tier = "default"
                    structlog.get_logger().info("tier_fallback", task_id=details["id"], kind="ask")
                    continue
            try:
                payload = response.json()
                if response.status_code >= 400 or not isinstance(payload, dict):
                    raise ValueError
            except ValueError:
                usage.add(None, body, config.max_output_tokens)
                await record_usage(mcp, config.model, config.effort, tier, usage)
                raise ValueError("model request failed") from None
            usage.add(payload.get("usage"), body, config.max_output_tokens)
            await record_usage(mcp, config.model, config.effort, tier, usage)
            output = payload["output"]
            calls = [item for item in output if item.get("type") == "function_call"]
            if not calls:
                content = next(
                    c
                    for item in output
                    if item.get("type") == "message"
                    for c in item["content"]
                    if c.get("type") == "output_text"
                )
                answer = json.loads(content["text"])
                if not Draft202012Validator(SCHEMA).is_valid(answer):
                    raise ValueError("invalid model output")
                return answer, count
            if not enabled:
                raise ValueError("tool call after tools disabled")
            messages.extend(output)
            for call in calls:
                name = call["name"]
                args = json.loads(call["arguments"])
                result = {"error": "tool limit reached"}
                if count < 6 and time.monotonic() - started < 60:
                    count += 1
                    if name in READS and isinstance(args, dict):
                        args.pop("task_id", None)
                        if "space" in TOOLS[name]["inputSchema"].get("properties", {}):
                            args["space"] = "org:" + details["orgId"]
                        if name.startswith("coslash_conversation_") and args.get(
                            "channel_id"
                        ) not in (
                            None,
                            question["channelId"],
                        ):
                            result = {"error": "channel not available"}
                        else:
                            try:
                                remaining = max(0.001, 60 - (time.monotonic() - started))
                                result = await asyncio.wait_for(mcp.call(name, args), remaining)
                                seen.extend(seen_in(name, result))
                                if name.startswith("coslash_conversation_"):
                                    result["items"] = [
                                        item
                                        for item in result.get("items", [])
                                        if item.get("is_bot") is False
                                    ]
                            except (ToolError, TimeoutError):
                                result = {"error": "tool unavailable"}
                remember_instructions(result, passages)
                messages.append(
                    {
                        "type": "function_call_output",
                        "call_id": call["call_id"],
                        "output": quote_untrusted(json.dumps(result, ensure_ascii=False)),
                    }
                )


async def run(task, mcp, deadline):
    started = time.monotonic()
    details = task["task"]
    ask = details["ask"]
    seen = []
    context = []
    passages = set()
    remember_instructions(ask["question"], passages)
    received = 0
    outcome = "failed"
    count = 0
    try:
        async with asyncio.timeout(
            min(80, max(0.001, (deadline - datetime.now(UTC)).total_seconds()))
        ):
            args = {"channel_id": ask["channelId"], "thread_ts": ask["threadTs"], "limit": 200}
            cursors = set()
            while received < 200:
                page = await mcp.call("coslash_conversation_window", args)
                items = page.get("items", [])[: 200 - received]
                received += len(items)
                context.extend(item for item in items if item.get("is_bot") is False)
                seen.extend(seen_in("coslash_conversation_window", items))
                cursor = page.get("next_cursor")
                if not cursor or cursor in cursors:
                    break
                cursors.add(cursor)
                args["cursor"] = cursor
                args["limit"] = 200 - received
            remember_instructions(context, passages)
            result, count = await ask_model(task, mcp, deadline, started, seen, context, passages)
            citations = validate_refs(result["citations"], seen)
            text = clean_text(result["text"])
            if result["found"] and citations and text:
                if unsafe_answer(result["text"], passages):
                    answer = {"text": FAILED, "citations": []}
                else:
                    answer = {"text": text, "citations": citations}
                    outcome = "answered"
            else:
                answer = {"text": NOT_FOUND, "citations": []}
                outcome = "not_found"
    except TimeoutError:
        answer = {"text": TIMEOUT, "citations": []}
        outcome = "timeout"
    except (TaskForbidden, TaskExpired):
        raise
    except Exception:
        answer = {"text": FAILED, "citations": []}
    await mcp.coslash_reply_in_thread(answer=answer)
    structlog.get_logger().info(
        "ask_finished",
        task_id=details["id"],
        tool_calls=count,
        citations=len(answer["citations"]),
        latency_ms=round((time.monotonic() - started) * 1000),
        outcome=outcome,
    )
