from coslash_agents.kinds.ask import READS, tools_for_model


def test_model_only_receives_reads():
    names = {t["name"] for t in tools_for_model()}
    assert names == READS
    assert "coslash_reply_in_thread" not in names
    assert "coslash_record_usage" not in names
    assert "coslash_write_board_report" not in names
    assert "coslash_upsert_issues" not in names


async def test_cannot_widen_channel_or_space(monkeypatch, cases, ask_task):
    from .conftest import called, calls, execute, final, setup

    fake, _ = setup(
        monkeypatch,
        cases,
        [
            calls(
                ("coslash_conversation_search", {"query": "secret", "channel_id": "C_PRIVATE"}),
                ("coslash_get_session", {"id": "s1", "space": "org:another"}),
            ),
            final(found=False),
        ],
    )
    await execute(fake, ask_task)
    assert not called(fake, "coslash_conversation_search")
    assert called(fake, "coslash_get_session")[0]["space"] == "org:team-1"


async def test_rejected_tools_consume_limit(monkeypatch, cases, ask_task):
    from .conftest import called, calls, execute, final, setup

    requests = [("coslash_upsert_issues", {}) for _ in range(6)]
    fake, model = setup(monkeypatch, cases, [calls(*requests), final(found=False)])
    await execute(fake, ask_task)
    assert not called(fake, "coslash_upsert_issues")
    assert model.calls[1]["tools"] == []
