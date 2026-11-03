import structlog

from .conftest import called, execute, final, setup


async def test_canaries_are_data_and_bot_messages_are_not_cited(monkeypatch, cases, ask_task):
    ask_task["task"]["ask"]["question"] = cases["canary"]
    cases["public"]["text"] = cases["canary"]
    ref = {"kind": "slack", "channelId": "channel-1", "ts": "10.1"}
    fake, model = setup(monkeypatch, cases, [final("The API is ready.", [ref])])
    with structlog.testing.capture_logs() as logs:
        await execute(fake, ask_task)
    answer = called(fake, "coslash_reply_in_thread")[0]["answer"]
    assert cases["canary"] not in str(answer)
    assert cases["canary"] not in str(logs)
    assert "untrusted" in model.calls[0]["input"][1]["content"]
    assert '"ts": "10.2"' not in model.calls[0]["input"][1]["content"]


async def test_canary_in_session_is_quoted_and_bot_search_result_excluded(
    monkeypatch, cases, ask_task
):
    from .conftest import calls

    ref = {"kind": "session", "sessionId": "s1"}
    fake, model = setup(
        monkeypatch,
        cases,
        [
            calls(
                ("coslash_get_session", {"id": "s1"}),
                ("coslash_conversation_search", {"query": "API"}),
            ),
            final("API work is ready.", [ref]),
        ],
    )
    fake.outputs["coslash_get_session"]["goal"] = cases["canary"]
    with structlog.testing.capture_logs() as logs:
        await execute(fake, ask_task)
    outputs = model.calls[1]["input"]
    tool_results = [
        entry["output"] for entry in outputs if entry.get("type") == "function_call_output"
    ]
    assert all(value.startswith("<untrusted>") for value in tool_results)
    assert '"ts": "10.2"' not in str(tool_results)
    assert cases["canary"] not in str(logs)
    assert cases["canary"] not in str(called(fake, "coslash_reply_in_thread"))
