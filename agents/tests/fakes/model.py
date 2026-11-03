import argparse
import asyncio
import html
import json

import uvicorn
from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Route

from coslash_agents.kinds.ask import seen_in


def data_block(value):
    if not isinstance(value, str) or not value.startswith("<untrusted>"):
        return {}
    if not value.endswith("</untrusted>"):
        return {}
    try:
        return json.loads(html.unescape(value[len("<untrusted>") : -len("</untrusted>")]))
    except ValueError:
        return {}


def context(body):
    messages = body.get("input", [])
    if not isinstance(messages, list):
        return {}
    for item in messages:
        if isinstance(item, dict) and item.get("role") == "user":
            data = data_block(item.get("content"))
            if isinstance(data, dict):
                return data
    return {}


def ask_refs(body, data):
    refs = seen_in("coslash_conversation_window", data.get("thread", []))
    calls = {}
    for item in body.get("input", []):
        if not isinstance(item, dict):
            continue
        if item.get("type") == "function_call":
            calls[item.get("call_id")] = item.get("name", "")
        elif item.get("type") == "function_call_output":
            output = data_block(item.get("output"))
            if isinstance(output, dict):
                refs.extend(seen_in(calls.get(item.get("call_id"), ""), output))
    return refs


def message(content):
    return [
        {
            "type": "message",
            "role": "assistant",
            "content": [{"type": "output_text", "text": json.dumps(content)}],
        }
    ]


def default_output(body):
    properties = body.get("text", {}).get("format", {}).get("schema", {}).get("properties", {})
    data = context(body)
    if {"found", "citations"} <= properties.keys():
        refs = ask_refs(body, data)
        if not refs:
            tools = body.get("tools", [])
            names = {tool.get("name") for tool in tools if isinstance(tool, dict)}
            if "coslash_list_sessions" in names and not any(
                item.get("type") == "function_call_output"
                for item in body.get("input", [])
                if isinstance(item, dict)
            ):
                return [
                    {
                        "type": "function_call",
                        "call_id": "fake-list-sessions",
                        "name": "coslash_list_sessions",
                        "arguments": "{}",
                    }
                ]
        content = {
            "text": "Synthetic answer based on the supplied source."
            if refs
            else "No source found.",
            "citations": refs[:1],
            "found": bool(refs),
        }
    elif "themes" in properties or "moved" in properties:
        sessions = [
            s
            for s in data.get("sessions", [])
            if isinstance(s, dict)
            and isinstance(s.get("session_id"), str)
            and 0 < len(s["session_id"]) <= 256
        ]
        refs = [{"kind": "session", "sessionId": s["session_id"]} for s in sessions]
        if not refs:
            refs = seen_in("coslash_conversation_window", data.get("messages", []))
        decisions = [{"text": "Synthetic decision", "source": refs[0]}] if refs else []
        if "themes" in properties:
            content = {
                "themes": [
                    {
                        "name": "Synthetic work",
                        "summary": "Synthetic progress",
                        "sessionIds": [s["session_id"] for s in sessions[:8]],
                    }
                ]
                if sessions
                else [],
                "lines": [
                    {"sessionId": s["session_id"], "line": "Synthetic progress"} for s in sessions
                ],
                "headline": "Synthetic board update",
                "decisions": decisions,
                "issues": [],
            }
        else:
            content = {
                "moved": [{"text": "Synthetic progress", "refs": refs[:1]}] if refs else [],
                "decisions": decisions,
            }
    else:
        content = {"text": "ok"}
    return message(content)


class FakeModel:
    def __init__(self, replies=None, mode="normal"):
        self.replies = list(replies or [])
        self.mode = mode
        self.calls = []
        self.app = Starlette(
            routes=[
                Route("/v1/responses", self.handle, methods=["POST"]),
                Route("/responses", self.handle, methods=["POST"]),
                Route("/_control", self.control, methods=["POST"]),
            ]
        )

    async def control(self, request):
        try:
            body = await request.json()
        except ValueError:
            return JSONResponse({"error": "invalid mode"}, status_code=400)
        if not isinstance(body, dict) or body.get("mode") not in ("ok", "error", "slow"):
            return JSONResponse({"error": "invalid mode"}, status_code=400)
        self.mode = body["mode"]
        return JSONResponse({"mode": self.mode})

    async def handle(self, request):
        body = await request.json()
        self.calls.append(body)
        if self.mode == "tier_error" and "service_tier" in body:
            return JSONResponse(
                {"error": {"param": "service_tier", "message": "unsupported service_tier"}},
                status_code=400,
            )
        if self.mode == "error":
            return JSONResponse({"error": {"message": "model error"}}, status_code=400)
        if self.mode in ("timeout", "slow"):
            await asyncio.sleep(3600)
        if self.replies:
            reply = self.replies.pop(0)
        else:
            if self.mode == "invented_refs":
                output = message({"refs": [{"kind": "session", "sessionId": "invented"}]})
            elif self.mode == "canary":
                output = message({"text": "canary-private-content"})
            else:
                output = default_output(body)
            reply = {
                "id": "resp_fake",
                "object": "response",
                "output": output,
                "usage": {
                    "input_tokens": 10,
                    "input_tokens_details": {"cached_tokens": 2},
                    "output_tokens": 3,
                    "output_tokens_details": {"reasoning_tokens": 1},
                },
            }
        return JSONResponse(reply)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--mode", default="normal")
    args = parser.parse_args()
    uvicorn.run(FakeModel(mode=args.mode).app, host="0.0.0.0", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()
