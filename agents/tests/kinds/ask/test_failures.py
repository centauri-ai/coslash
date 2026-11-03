from types import SimpleNamespace

import coslash_agents.kinds.ask as ask
from coslash_agents.kinds.ask.copy import FAILED, TIMEOUT

from .conftest import called, calls, execute, final, setup


async def test_model_error_replies_and_records(monkeypatch, cases, ask_task):
    fake, _ = setup(monkeypatch, cases, mode="error")
    await execute(fake, ask_task)
    assert called(fake, "coslash_reply_in_thread")[0]["answer"] == {"text": FAILED, "citations": []}
    usage = called(fake, "coslash_record_usage")
    assert len(usage) == 1
    assert usage[0]["feature"] == "ask" and usage[0]["estimated"] is True


async def test_budget_timeout(monkeypatch, cases, ask_task):
    import asyncio

    fake, _ = setup(monkeypatch, cases)
    durations = []

    class Budget:
        def __init__(self, seconds):
            durations.append(seconds)

        async def __aenter__(self):
            return self

        async def __aexit__(self, kind, value, tb):
            if kind is asyncio.CancelledError:
                raise TimeoutError from None

    async def slow(*args):
        raise asyncio.CancelledError

    monkeypatch.setattr(ask, "asyncio", SimpleNamespace(timeout=Budget))
    monkeypatch.setattr(ask, "ask_model", slow)
    await execute(fake, ask_task)
    assert durations == [80]
    assert called(fake, "coslash_reply_in_thread")[0]["answer"] == {
        "text": TIMEOUT,
        "citations": [],
    }


async def test_seventh_call_is_never_executed(monkeypatch, cases, ask_task):
    requests = [("coslash_conversation_search", {"query": str(i)}) for i in range(7)]
    fake, model = setup(monkeypatch, cases, [calls(*requests), final(found=False)])
    await execute(fake, ask_task)
    assert len(called(fake, "coslash_conversation_search")) == 6
    assert model.calls[1]["tools"] == []


async def test_deadline_disables_tools(monkeypatch, cases, ask_task):
    fake, model = setup(monkeypatch, cases, [final(found=False)])
    real = ask.time.monotonic
    times = iter([real(), real() + 61, real() + 61, real() + 61, real() + 61])
    monkeypatch.setattr(ask, "time", SimpleNamespace(monotonic=lambda: next(times, real() + 61)))
    await execute(fake, ask_task)
    assert model.calls[0]["tools"] == []


async def test_tier_fallback_recorded(monkeypatch, cases, ask_task):
    fake, model = setup(monkeypatch, cases, [final(found=False)], mode="tier_error")
    await execute(fake, ask_task)
    assert model.calls[0]["service_tier"] == "fast"
    assert "service_tier" not in model.calls[1]
    assert called(fake, "coslash_record_usage")[0]["service_tier"] == "default"


async def test_invalid_model_output_replies_failed(monkeypatch, cases, ask_task):
    fake, _ = setup(
        monkeypatch,
        cases,
        [
            {
                "output": [{"type": "message", "content": [{"type": "output_text", "text": "{}"}]}],
                "usage": {"input_tokens": 1, "output_tokens": 1},
            }
        ],
    )
    await execute(fake, ask_task)
    assert called(fake, "coslash_reply_in_thread")[0]["answer"] == {"text": FAILED, "citations": []}


async def test_final_model_timeout_replies_timeout(monkeypatch, cases, ask_task):
    import httpx

    from coslash_agents.llm.client import ModelClient

    fake, _ = setup(monkeypatch, cases)

    def timed_out(request):
        raise httpx.ReadTimeout("synthetic", request=request)

    class SlowClient(ModelClient):
        def __init__(self, *args):
            super().__init__(
                *args,
                api_key="synthetic",
                base_url="http://fake/v1",
                transport=httpx.MockTransport(timed_out),
            )

    times = iter([0, 61, 61, 80])
    monkeypatch.setattr(ask, "time", SimpleNamespace(monotonic=lambda: next(times, 80)))
    monkeypatch.setattr(ask, "ModelClient", SlowClient)
    await execute(fake, ask_task)
    assert called(fake, "coslash_reply_in_thread")[0]["answer"] == {
        "text": TIMEOUT,
        "citations": [],
    }
    assert len(called(fake, "coslash_record_usage")) == 1
