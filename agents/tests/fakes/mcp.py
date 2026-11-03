import json
from importlib.resources import files

from jsonschema import Draft202012Validator, FormatChecker, RefResolver
from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Route

from coslash_agents.mcp.client import TOOLS


ROOT = files("coslash_agents.contracts").joinpath("ctb")
BOARD = json.loads(ROOT.joinpath("board-report-1.schema.json").read_text())
FIXTURES = files("coslash_agents.tests.fakes").joinpath("fixtures/ctb-agents-1")
DATA = json.loads(FIXTURES.joinpath("data.json").read_text())
BOARD_EXAMPLE = json.loads(FIXTURES.joinpath("latest_board.json").read_text())


def validate(schema, value):
    resolver = RefResolver(
        base_uri="https://contracts.invalid/",
        referrer=schema,
        store={"https://contracts.invalid/board-report-1.schema.json": BOARD},
    )
    Draft202012Validator(schema, resolver=resolver, format_checker=FormatChecker()).validate(value)


def sample(schema):
    if "const" in schema:
        return schema["const"]
    if "enum" in schema:
        return schema["enum"][0]
    if "anyOf" in schema:
        return sample(schema["anyOf"][0])
    if "oneOf" in schema:
        return sample(schema["oneOf"][0])
    kind = schema.get("type")
    if isinstance(kind, list):
        kind = "null" if "null" in kind else kind[0]
    if kind == "object" or "properties" in schema:
        return {key: sample(schema["properties"][key]) for key in schema.get("required", [])}
    if kind == "array":
        return []
    if kind == "boolean":
        return True
    if kind in {"integer", "number"}:
        return max(0, schema.get("minimum", 0))
    if kind == "null":
        return None
    if schema.get("format") == "date-time":
        return "2026-11-02T12:00:00Z"
    return "example"


class FakeMCP:
    def __init__(
        self, task_id="task-1", kind="board_tick", token="synthetic-token", channel_id=None
    ):
        self.task_id, self.kind, self.token = task_id, kind, token
        self.channel_id = channel_id
        self.disabled = False
        self.expired = False
        self.calls = []
        self.outputs = {}
        self.app = Starlette(routes=[Route("/mcp", self.handle, methods=["POST"])])

    async def handle(self, request):
        if self.expired:
            return JSONResponse({"error": "expired"}, status_code=401)
        if request.headers.get("authorization") != f"Bearer {self.token}":
            return JSONResponse({"error": "unauthorized"}, status_code=401)
        body = await request.json()
        method = body["method"]
        self.calls.append(body)
        if method == "initialize":
            return JSONResponse(
                {
                    "jsonrpc": "2.0",
                    "id": body["id"],
                    "result": {
                        "protocolVersion": "2025-06-18",
                        "capabilities": {},
                        "serverInfo": {"name": "fake", "version": "1"},
                    },
                }
            )
        if method != "tools/call":
            return JSONResponse(
                {
                    "jsonrpc": "2.0",
                    "id": body["id"],
                    "error": {"code": -32601, "message": "unknown method"},
                }
            )
        name, args = body["params"]["name"], body["params"]["arguments"]
        tool = TOOLS.get(name)
        denied = (
            self.disabled
            or tool is None
            or self.kind not in tool["taskKinds"]
            or ("task_id" in args and args["task_id"] != self.task_id)
        )
        if not denied and name == "coslash_write_board_report":
            denied = args.get("report", {}).get("kind") != (
                "board" if self.kind == "board_tick" else "day_report"
            )
        if denied:
            error = {
                "error": {
                    "code": "FORBIDDEN",
                    "message": "forbidden",
                    "hint": "installation_disabled" if self.disabled else "task_binding",
                }
            }
            return JSONResponse(
                {
                    "jsonrpc": "2.0",
                    "id": body["id"],
                    "result": {
                        "isError": True,
                        "content": [{"type": "text", "text": json.dumps(error)}],
                    },
                }
            )
        validate(tool["inputSchema"], args)
        if name == "coslash_write_board_report":
            validate(BOARD, args["report"])
        result = self.outputs.get(name, sample(tool["outputSchema"]))
        if (
            name in {"coslash_conversation_window", "coslash_conversation_search"}
            and name not in self.outputs
        ):
            result = {
                "items": [
                    m
                    for m in DATA["messages"]
                    if (
                        m["channel_id"] != "C_PRIVATE"
                        or (self.kind == "ask" and self.channel_id == "C_PRIVATE")
                    )
                    and (not args.get("channel_id") or args["channel_id"] == m["channel_id"])
                ],
                "next_cursor": None,
                "untrusted": True,
            }
        if name == "coslash_team_activity" and name not in self.outputs:
            result = {
                "as_of": "2026-11-02T12:00:00Z",
                "counts": {"active": 2, "idle": 1, "done": 3, "people": 2},
                "items": DATA["sessions"],
                "next_cursor": None,
                "untrusted": True,
            }
        if name == "coslash_list_sessions" and name not in self.outputs:
            result = {
                "items": [
                    {
                        "id": s["session_id"],
                        "title": s["title"],
                        "status": s["status"],
                        "owner": s["owner"],
                        "agent": "codex",
                        "updated_at": "2026-11-02T12:00:00Z",
                        "hub_url": "https://example.invalid/" + s["session_id"],
                        "pull_requests": [],
                        "untrusted": True,
                    }
                    for s in DATA["sessions"]
                ],
                "next_cursor": "",
                "untrusted": True,
            }
        if name == "coslash_list_team_issues" and name not in self.outputs:
            result = {"items": DATA["issues"], "next_cursor": None, "untrusted": True}
        if name == "coslash_get_team_board" and name not in self.outputs:
            result = {
                "slot": "2026-11-02T12:00:00Z",
                "insight_mode": "llm",
                "created_at": "2026-11-02T12:00:00Z",
                "report": BOARD_EXAMPLE,
                "untrusted": True,
            }
        if "next_cursor" in result and name not in self.outputs:
            cursor_schema = tool["outputSchema"]["properties"]["next_cursor"]
            result["next_cursor"] = (
                None
                if isinstance(cursor_schema.get("type"), list) and "null" in cursor_schema["type"]
                else ""
            )
        validate(tool["outputSchema"], result)
        return JSONResponse(
            {"jsonrpc": "2.0", "id": body["id"], "result": {"structuredContent": result}}
        )


def main():
    import argparse
    import uvicorn

    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--kind", choices=["ask", "board_tick", "day_report"], default="board_tick")
    args = parser.parse_args()
    uvicorn.run(FakeMCP(kind=args.kind).app, host="0.0.0.0", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()
