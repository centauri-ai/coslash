import json
from datetime import UTC, datetime
from importlib.resources import files

import httpx
from pydantic import ValidationError

from coslash_agents.mcp import generated


CONTRACT = json.loads(files("coslash_agents.contracts").joinpath("ctb/mcp-tools.json").read_text())
TOOLS = {tool["name"]: tool for tool in CONTRACT["tools"]}


class TaskForbidden(Exception):
    pass


class TaskExpired(Exception):
    pass


class Retryable(Exception):
    pass


class ToolError(Exception):
    def __init__(self, code, hint=None):
        self.code, self.hint = code, hint
        super().__init__("tool error")


class MCPClient:
    def __init__(self, url, token, task_id, kind, deadline, transport=None):
        self.url, self.task_id, self.kind, self.deadline = url, task_id, kind, deadline
        self._http = httpx.AsyncClient(
            headers={"Authorization": f"Bearer {token}"}, transport=transport
        )
        self._id = 0

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        await self._http.aclose()

    async def _rpc(self, method, params):
        timeout = (self.deadline - datetime.now(UTC)).total_seconds()
        if timeout <= 0:
            raise TaskExpired()
        self._id += 1
        try:
            response = await self._http.post(
                self.url,
                json={"jsonrpc": "2.0", "id": self._id, "method": method, "params": params},
                timeout=timeout,
            )
        except httpx.HTTPError:
            raise Retryable() from None
        if response.status_code == 401:
            raise TaskExpired()
        if response.status_code >= 500:
            raise Retryable()
        if response.status_code >= 400:
            raise ToolError("HTTP_ERROR")
        try:
            body = response.json()
            if body.get("error"):
                raise ToolError("RPC_ERROR")
            result = body["result"]
            if result.get("isError"):
                error = json.loads(result["content"][0]["text"])["error"]
                code = error.get("code", "UNKNOWN")
                if code == "FORBIDDEN":
                    raise TaskForbidden()
                if code == "UNAVAILABLE":
                    raise Retryable()
                raise ToolError(code, error.get("hint"))
            return result
        except (KeyError, ValueError, TypeError, IndexError):
            raise Retryable() from None

    async def initialize(self):
        return await self._rpc(
            "initialize",
            {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "coslash-agents", "version": "0.1.0"},
            },
        )

    async def call(self, name, arguments=None):
        tool = TOOLS.get(name)
        if tool is None or self.kind not in tool["taskKinds"]:
            raise TaskForbidden()
        args = dict(arguments or {})
        if "task_id" in args and args["task_id"] != self.task_id:
            raise TaskForbidden()
        if "task_id" in tool["inputSchema"].get("properties", {}):
            args["task_id"] = self.task_id
        if name == "coslash_write_board_report":
            expected = "board" if self.kind == "board_tick" else "day_report"
            if args.get("report", {}).get("kind") != expected:
                raise TaskForbidden()
        input_class = "".join(part.capitalize() for part in name.split("_")) + "Input"
        try:
            getattr(generated, input_class).model_validate(args)
        except ValidationError:
            raise ToolError("INVALID_ARGUMENT") from None
        result = await self._rpc("tools/call", {"name": name, "arguments": args})
        if "structuredContent" not in result:
            raise Retryable()
        return result["structuredContent"]

    async def pages(self, name, arguments=None):
        args = dict(arguments or {})
        seen = set()
        while True:
            page = await self.call(name, args)
            yield page
            cursor = page.get("next_cursor")
            if not cursor:
                return
            if cursor in seen:
                raise Retryable()
            seen.add(cursor)
            args["cursor"] = cursor


def _wrapper(name, input_type, output_type):
    async def invoke(self, arguments=None, **kwargs):
        if arguments is not None:
            if not isinstance(arguments, input_type) or kwargs:
                raise ToolError("INVALID_ARGUMENT")
            kwargs = arguments.model_dump(mode="json", exclude_unset=True)
        data = await self.call(name, kwargs)
        try:
            return output_type.model_validate(data)
        except ValidationError:
            raise Retryable() from None

    invoke.__name__ = name
    invoke.__annotations__ = {"arguments": input_type | None, "return": output_type}
    return invoke


for _name, _tool in TOOLS.items():
    if _tool["taskKinds"]:
        _class_name = "".join(part.capitalize() for part in _name.split("_"))
        setattr(
            MCPClient,
            _name,
            _wrapper(
                _name,
                getattr(generated, _class_name + "Input"),
                getattr(generated, _class_name + "Output"),
            ),
        )
