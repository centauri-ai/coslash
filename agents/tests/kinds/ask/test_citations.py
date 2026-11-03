from coslash_agents.kinds.ask.copy import NOT_FOUND

from .conftest import called, execute, final, setup


async def test_drops_invented_and_bot_refs(monkeypatch, cases, ask_task):
    refs = [
        {"kind": "session", "sessionId": "invented"},
        {"kind": "slack", "channelId": "channel-1", "ts": "invented"},
        {"kind": "slack", "channelId": "channel-1", "ts": "10.2"},
    ]
    fake, _ = setup(monkeypatch, cases, [final(citations=refs)])
    await execute(fake, ask_task)
    assert called(fake, "coslash_reply_in_thread")[0]["answer"] == {
        "text": NOT_FOUND,
        "citations": [],
    }


async def test_found_false_and_text_cleanup(monkeypatch, cases, ask_task):
    ref = {"kind": "slack", "channelId": "channel-1", "ts": "10.1"}
    fake, _ = setup(monkeypatch, cases, [final(found=False, citations=[ref])])
    await execute(fake, ask_task)
    assert called(fake, "coslash_reply_in_thread")[0]["answer"]["text"] == NOT_FOUND
    fake, _ = setup(
        monkeypatch,
        cases,
        [final(text="word " * 250 + "<!channel> https://evil.example", citations=[ref])],
    )
    await execute(fake, ask_task)
    text = called(fake, "coslash_reply_in_thread")[0]["answer"]["text"]
    assert len(text) <= 900 and "<!channel>" not in text and "https://" not in text
