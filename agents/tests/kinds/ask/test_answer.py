from .conftest import called, calls, execute, final, setup


async def test_cited_answer(monkeypatch, cases, ask_task):
    refs = [
        {"kind": "slack", "channelId": "channel-1", "ts": "10.1"},
        {"kind": "session", "sessionId": "s1"},
    ]
    fake, model = setup(
        monkeypatch,
        cases,
        [
            calls(
                ("coslash_conversation_search", {"query": "API"}),
                ("coslash_get_session", {"id": "s1"}),
            ),
            final(citations=refs),
        ],
    )
    await execute(fake, ask_task)
    reply = called(fake, "coslash_reply_in_thread")
    assert reply[0]["answer"] == {"text": "The API is ready.", "citations": refs}
    assert len(called(fake, "coslash_conversation_window")) == 1
    assert len(called(fake, "coslash_record_usage")) == 2
    assert all(
        (u["feature"], u["model"], u["effort"], u["service_tier"])
        == ("ask", "gpt-6-luna", "high", "fast")
        for u in called(fake, "coslash_record_usage")
    )
    assert all(c["store"] is False for c in model.calls)
    assert model.calls[0]["reasoning"]["effort"] == "high"
    assert called(fake, "coslash_get_session")[0]["space"] == "org:team-1"
