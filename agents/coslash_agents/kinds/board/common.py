import hashlib
import json
import re
from datetime import UTC, datetime

import structlog

from coslash_agents.echo import remember_instructions, unsafe_answer
from coslash_agents.llm.client import ModelClient, ModelError
from coslash_agents.mcp.client import Retryable
from coslash_agents.safety import enforce_caps, quote_untrusted, strip_slack_controls, validate_refs
from coslash_agents.usage import record_usage

log = structlog.get_logger()


def timestamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def iso(value):
    return value.astimezone(UTC).isoformat().replace("+00:00", "Z")


def safety_context(*inputs):
    passages = set()
    for value in inputs:
        remember_instructions(value, passages)
    return passages


def clean(value, limit, passages=()):
    value = str(value or "")
    if unsafe_answer(value, passages):
        return ""
    value = strip_slack_controls(value)
    value = re.sub(r"https?://\S+|<[^>]*>|\[[^\]]+\]\([^)]*\)", "", value, flags=re.I)
    value = re.sub(r"[*_~`\x00-\x1f]", " ", value)
    value = " ".join(value.split())[:limit]
    return "" if unsafe_answer(value, passages) else value


def window_sessions(items, end):
    result = []
    for item in items:
        if any(
            item.get(field) and timestamp(item[field]) > end
            for field in ("last_activity_at", "card_at", "content_as_of")
        ):
            item = {
                **item,
                "title": "",
                "goal": "",
                "outcome": "",
                "key_decisions": [],
                "next_step": "",
                "status": "",
                "_after_window": True,
            }
        result.append(item)
    return result


def owner(item):
    return item.get("owner") or {}


def people(items):
    return {owner(i).get("id") for i in items if owner(i).get("id")}


async def pages(mcp, name, **arguments):
    result = []
    async for page in mcp.pages(name, arguments):
        result.extend(page["items"])
    return result


async def activity(mcp, space, window):
    return await pages(mcp, "coslash_team_activity", space=space, window=window, limit=100)


async def messages(mcp, window):
    result = await pages(
        mcp, "coslash_conversation_window", since=window["start"], until=window["end"], limit=500
    )
    if any(type(item.get("is_bot")) is not bool for item in result):
        raise Retryable()
    return result


async def issues(mcp, space):
    return await pages(mcp, "coslash_list_team_issues", space=space, status="open")


def seen_refs(sessions, messages):
    return [
        {"kind": "session", "sessionId": s["session_id"]} for s in sessions if s.get("session_id")
    ] + [
        {"kind": "slack", "channelId": m["channel_id"], "ts": m["ts"]}
        for m in messages
        if m.get("is_bot") is False and m.get("channel_id") and m.get("ts")
    ]


def minimal_refs(refs):
    return validate_refs(refs, refs)


def report_issues(items, passages=None):
    if passages is None:
        passages = set()
    remember_instructions(items, passages)
    result = []
    for issue in items:
        entry = {k: issue[k] for k in ("id", "type", "confidence")}
        entry["title"] = clean(issue.get("title"), 1000000, passages)
        if not entry["title"]:
            continue
        if issue.get("detail"):
            detail = clean(issue["detail"], 1000000, passages)
            if detail:
                entry["detail"] = detail
        entry["refs"] = minimal_refs(issue.get("refs", []))
        result.append(entry)
    return result


def entries(items, done=False, passages=()):
    out = []
    for s in items:
        at = s.get("ended_at" if done else "last_activity_at")
        if not at or not s.get("session_id"):
            continue
        row = {
            "sessionId": s["session_id"],
            "line": clean(s.get("goal") or s.get("title"), 1000000, passages),
            "owner": clean(owner(s).get("name"), 1000000, passages),
            "agent": clean(s.get("agent"), 120, passages),
            ("endedAt" if done else "lastActivityAt"): at,
        }
        optional = (s.get("key_decisions") or [None])[0] if done else s.get("next_step")
        if optional:
            row["decided" if done else "next"] = clean(optional, 1000000, passages)
        out.append(row)
    return out


def digest(sessions, messages, end):
    end = timestamp(end)
    obj = {
        "sessions": sorted(s.get("session_id", "") for s in sessions),
        "ended": sorted(
            s.get("session_id", "")
            for s in sessions
            if s.get("ended_at") and timestamp(s["ended_at"]) <= end
        ),
        "decisions": sorted(
            (s.get("session_id", ""), sorted(s.get("key_decisions") or [])) for s in sessions
        ),
        "messages": sorted(
            (m.get("channel_id", ""), m.get("ts", "")) for m in messages if m.get("is_bot") is False
        ),
    }
    return hashlib.sha256(
        json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode()
    ).hexdigest()


def count_decisions(report):
    return len(report.get("decisions", [])) + sum(
        x["count"] for x in report.get("truncated", []) if x["field"] == "decisions"
    )


def cap(report, field, limit):
    value = report[field]
    if len(value) > limit:
        report.setdefault("truncated", []).append({"field": field, "count": len(value) - limit})
        report[field] = value[:limit]


def finalize(report, passages=()):
    report = enforce_caps(report)
    if unsafe_answer(report["team"]["name"], passages):
        report["team"]["name"] = ""
    if "headline" in report and unsafe_answer(report["headline"], passages):
        report["headline"] = ""
    report["themes"] = [
        theme
        for theme in report["themes"]
        if not any(unsafe_answer(theme[field], passages) for field in ("name", "summary"))
    ]
    for row in report["active"] + report["done"]:
        for field in ("line", "owner", "agent", "next", "decided"):
            if field in row and unsafe_answer(row[field], passages):
                row[field] = ""
    for person in report.get("people", []):
        if unsafe_answer(person["name"], passages):
            person["name"] = ""
    report["decisions"] = [
        item for item in report["decisions"] if not unsafe_answer(item["text"], passages)
    ]
    report["issues"] = [
        issue for issue in report["issues"] if not unsafe_answer(issue["title"], passages)
    ]
    for issue in report["issues"]:
        if "detail" in issue and unsafe_answer(issue["detail"], passages):
            del issue["detail"]
    if "moved" in report:
        report["moved"] = [
            item for item in report["moved"] if not unsafe_answer(item["text"], passages)
        ]
    if "notes" in report:
        report["notes"] = [note for note in report["notes"] if not unsafe_answer(note, passages)]
    return report


def base(task, kind, window, as_of, passages=()):
    return {
        "schema": "board-report/1",
        "team": {"id": task["orgId"], "name": clean(task["teamName"], 120, passages)},
        "kind": kind,
        "title": "",
        "asOf": as_of,
        "window": window,
        "insightMode": "carry_forward",
        "insightsAsOf": as_of,
        "windows": {
            "1h": {"active": 0, "done": 0, "people": 0},
            "24h": {"sessions": 0, "done": 0, "people": 0, "decisions": 0},
        },
        "themes": [],
        "active": [],
        "done": [],
        "decisions": [],
        "issues": [],
    }


def model_inputs(sessions, messages, prompt, task):
    sessions = sorted(sessions, key=lambda x: x.get("last_activity_at", ""), reverse=True)
    messages = sorted(messages, key=lambda x: x.get("ts", ""), reverse=True)
    log.info(
        "input_counts",
        task_id=task["id"],
        kind=task["kind"],
        org_id=task["orgId"],
        sessions_overflow=max(0, len(sessions) - 60),
        messages_overflow=max(0, len(messages) - 200),
    )
    cards = []
    for s in sessions[:60]:
        card = {
            k: str(s.get(k) or "")[:500]
            for k in ("session_id", "title", "goal", "outcome", "next_step", "status")
        }
        remaining = 500
        card["key_decisions"] = []
        for decision in s.get("key_decisions") or []:
            if remaining <= 0:
                break
            text = str(decision)[:remaining]
            card["key_decisions"].append(text)
            remaining -= len(text) + 1
        cards.append(card)
    msgs = [
        {
            "channel_id": m.get("channel_id"),
            "ts": m.get("ts"),
            "is_bot": m.get("is_bot") is not False,
            "text": str(m.get("text") or "")[:500],
        }
        for m in messages[:200]
    ]
    data = quote_untrusted(json.dumps({"sessions": cards, "messages": msgs}, ensure_ascii=False))
    return [{"role": "system", "content": prompt}, {"role": "user", "content": data}]


REF = {
    "anyOf": [
        {
            "type": "object",
            "properties": {"kind": {"const": "session"}, "sessionId": {"type": "string"}},
            "required": ["kind", "sessionId"],
            "additionalProperties": False,
        },
        {
            "type": "object",
            "properties": {
                "kind": {"const": "slack"},
                "channelId": {"type": "string"},
                "ts": {"type": "string"},
            },
            "required": ["kind", "channelId", "ts"],
            "additionalProperties": False,
        },
    ]
}


def obj(properties):
    return {
        "type": "object",
        "properties": properties,
        "required": list(properties),
        "additionalProperties": False,
    }


def arr(item, maximum=None):
    result = {"type": "array", "items": item}
    if maximum is not None:
        result["maxItems"] = maximum
    return result


def string(maximum):
    return {"type": "string", "maxLength": maximum}


DECISION = obj({"text": string(280), "source": REF})


def decisions(values, seen, passages=()):
    result = []
    for item in values:
        refs = validate_refs([item["source"]], seen)
        text = clean(item["text"], 280, passages)
        if refs and text:
            result.append({"text": text, "source": refs[0]})
    return result


async def invoke_model(task, mcp, deadline, sessions, msgs, prompt, schema):
    inputs = model_inputs(sessions, msgs, prompt, task)
    async with ModelClient(task["kind"], task["id"], task["orgId"], deadline) as model:
        try:
            result = await model.run(inputs, schema)
        except ModelError as error:
            log.info("llm_failed", task_id=task["id"], kind=task["kind"], org_id=task["orgId"])
            if error.usage.estimated or any(
                (
                    error.usage.input_tokens,
                    error.usage.cached_input_tokens,
                    error.usage.output_tokens,
                    error.usage.reasoning_tokens,
                )
            ):
                await record_usage(
                    mcp, model.config.model, model.config.effort, error.service_tier, error.usage
                )
            return None
        await record_usage(
            mcp, model.config.model, model.config.effort, result.service_tier, result.usage
        )
        return result.output
