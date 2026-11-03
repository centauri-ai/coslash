import asyncio
import json
import math
import os
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

import httpx
import structlog
from jsonschema import Draft202012Validator


log = structlog.get_logger()


@dataclass(frozen=True)
class ModelConfig:
    model: str
    effort: str
    service_tier: str
    max_output_tokens: int

    @classmethod
    def for_kind(cls, kind):
        label, effort, tier, output = {
            "ask": ("ASK", "high", "fast", 6000),
            "board_tick": ("BOARD", "xhigh", "default", 16000),
            "day_report": ("DAY", "xhigh", "default", 20000),
        }[kind]
        prefix = "COSLASH_AGENTS_" + label + "_"
        return cls(
            os.getenv(prefix + "MODEL", "gpt-6-luna"),
            os.getenv(prefix + "EFFORT", effort),
            os.getenv(prefix + "SERVICE_TIER", tier),
            int(os.getenv(prefix + "MAX_OUTPUT_TOKENS", str(output))),
        )


@dataclass
class Usage:
    input_tokens: int = 0
    cached_input_tokens: int = 0
    output_tokens: int = 0
    reasoning_tokens: int = 0
    estimated: bool = False

    def add(self, value, input_data, max_output):
        if not isinstance(value, dict) or any(
            type(value.get(key)) is not int or value[key] < 0
            for key in ("input_tokens", "output_tokens")
        ):
            self.estimated = True
            self.input_tokens += math.ceil(len(json.dumps(input_data, ensure_ascii=False)) / 4)
            self.output_tokens += max_output
            return
        self.input_tokens += value.get("input_tokens", 0)
        self.cached_input_tokens += (value.get("input_tokens_details") or {}).get(
            "cached_tokens", 0
        )
        self.output_tokens += value.get("output_tokens", 0)
        self.reasoning_tokens += (value.get("output_tokens_details") or {}).get(
            "reasoning_tokens", 0
        )


@dataclass
class ModelResult:
    output: dict
    usage: Usage
    service_tier: str


class ModelError(Exception):
    def __init__(self, usage, service_tier):
        self.usage, self.service_tier = usage, service_tier
        super().__init__("model request failed")


class ModelClient:
    def __init__(
        self, kind, task_id, org_id, deadline, api_key=None, base_url=None, transport=None
    ):
        self.kind, self.task_id, self.org_id, self.deadline = kind, task_id, org_id, deadline
        self.config = ModelConfig.for_kind(kind)
        base = base_url or os.getenv("COSLASH_AGENTS_MODEL_BASE_URL") or "https://api.openai.com/v1"
        self.http = httpx.AsyncClient(
            base_url=base.rstrip("/") + "/",
            headers={"Authorization": "Bearer " + (api_key or os.getenv("OPENAI_API_KEY", ""))},
            transport=transport,
        )

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        await self.http.aclose()

    async def run(self, inputs, schema, tools=(), call_tool=None):
        config = self.config
        messages = list(inputs)
        tier = config.service_tier
        usage = Usage()
        margin = 0 if self.kind == "ask" else 60
        limit = self.deadline - timedelta(seconds=margin)
        while True:
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
                        "schema": schema,
                    }
                },
                "tools": list(tools),
            }
            if tier and tier != "default":
                body["service_tier"] = tier
            for attempt in range(2):
                remaining = (limit - datetime.now(UTC)).total_seconds()
                if remaining <= 0:
                    raise ModelError(usage, tier)
                try:
                    response = await self.http.post("responses", json=body, timeout=remaining)
                except httpx.HTTPError:
                    usage.add(None, body, config.max_output_tokens)
                    log.info("llm_failed", task_id=self.task_id, org_id=self.org_id, kind=self.kind)
                    raise ModelError(usage, tier) from None
                if response.status_code == 400 and tier == "fast" and attempt == 0:
                    try:
                        error = response.json().get("error", {})
                        is_tier = "service_tier" in str(
                            error.get("param", "")
                        ) or "service_tier" in str(error.get("message", ""))
                    except (ValueError, AttributeError, TypeError):
                        is_tier = False
                    if is_tier:
                        tier = "default"
                        body.pop("service_tier", None)
                        log.info(
                            "tier_fallback",
                            task_id=self.task_id,
                            org_id=self.org_id,
                            kind=self.kind,
                        )
                        continue
                if response.status_code >= 400:
                    usage.add(None, body, config.max_output_tokens)
                    log.info("llm_failed", task_id=self.task_id, org_id=self.org_id, kind=self.kind)
                    raise ModelError(usage, tier)
                break
            try:
                try:
                    payload = response.json()
                except ValueError:
                    usage.add(None, body, config.max_output_tokens)
                    raise
                if not isinstance(payload, dict):
                    usage.add(None, body, config.max_output_tokens)
                    raise ValueError("invalid model response")
                usage.add(payload.get("usage"), body, config.max_output_tokens)
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
                    result = json.loads(content["text"])
                    if not Draft202012Validator(schema).is_valid(result):
                        raise ValueError("invalid model output")
                    log.info(
                        "llm_finished",
                        task_id=self.task_id,
                        org_id=self.org_id,
                        kind=self.kind,
                        service_tier=tier,
                        input_tokens=usage.input_tokens,
                        cached_input_tokens=usage.cached_input_tokens,
                        output_tokens=usage.output_tokens,
                        reasoning_tokens=usage.reasoning_tokens,
                        estimated=usage.estimated,
                    )
                    return ModelResult(result, usage, tier)
                if call_tool is None:
                    raise ValueError("missing tool handler")
                messages.extend(output)
                for call in calls:
                    remaining = (limit - datetime.now(UTC)).total_seconds()
                    if remaining <= 0:
                        raise TimeoutError
                    result = await asyncio.wait_for(
                        call_tool(call["name"], json.loads(call["arguments"])), timeout=remaining
                    )
                    messages.append(
                        {
                            "type": "function_call_output",
                            "call_id": call["call_id"],
                            "output": json.dumps(result),
                        }
                    )
            except (KeyError, ValueError, TypeError, StopIteration, TimeoutError):
                log.info("llm_failed", task_id=self.task_id, org_id=self.org_id, kind=self.kind)
                raise ModelError(usage, tier) from None
