#!/usr/bin/env python3
"""Generate the synthetic native-agent home used by T52 PR-stage lanes."""
from __future__ import annotations
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import uuid

SPEC = Path(__file__).with_name("manifest.json")
NAMESPACE = uuid.UUID("ff65a453-7b5d-45cf-b1c8-e488069a10c2")


def stamp(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def identity(agent: str, key: str) -> str:
    return str(uuid.uuid5(NAMESPACE, f"{agent}:{key}"))


def write_jsonl(path: Path, rows: list[dict], modified: dt.datetime) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    path.write_text("".join(json.dumps(row, separators=(",", ":"), ensure_ascii=False) + "\n" for row in rows), encoding="utf-8")
    os.chmod(path, 0o600)
    timestamp = modified.timestamp()
    os.utime(path, (timestamp, timestamp))


def codex_rows(session_id: str, cwd: str, at: dt.datetime, topic: str) -> list[dict]:
    return [
        {"timestamp": stamp(at), "type": "session_meta", "payload": {"id": session_id, "timestamp": stamp(at), "cwd": cwd, "originator": "codex_cli_rs", "cli_version": "0.63.0", "source": "cli", "model_provider": "openai", "git": {"branch": "main"}}},
        {"timestamp": stamp(at + dt.timedelta(seconds=1)), "type": "turn_context", "payload": {"cwd": cwd, "model": "gpt-5-codex", "effort": "medium"}},
        {"timestamp": stamp(at + dt.timedelta(seconds=2)), "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": f"Review synthetic {topic} fixture."}]}},
        {"timestamp": stamp(at + dt.timedelta(seconds=3)), "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": f"Synthetic {topic} work is complete."}]}},
        {"timestamp": stamp(at + dt.timedelta(seconds=4)), "type": "event_msg", "payload": {"type": "task_complete", "last_agent_message": f"Synthetic {topic} work is complete."}},
    ]


def claude_rows(session_id: str, cwd: str, at: dt.datetime, topic: str, live: bool = False) -> list[dict]:
    def row(kind: str, offset: int, message: dict | None = None) -> dict:
        value = {"type": kind, "timestamp": stamp(at + dt.timedelta(seconds=offset)), "sessionId": session_id, "uuid": identity("claude-row", f"{session_id}:{kind}:{offset}"), "parentUuid": None, "cwd": cwd, "gitBranch": "main", "isSidechain": False, "userType": "external", "version": "2.1.3"}
        if message is not None:
            value["message"] = message
        return value
    prompt = {"role": "user", "content": f"Review synthetic {topic} fixture."}
    answer = {"role": "assistant", "model": "claude-sonnet-4-5-20250929", "id": "msg_" + session_id.replace("-", "")[:24], "type": "message", "content": [{"type": "text", "text": f"Synthetic {topic} work is complete."}], "stop_reason": "end_turn", "usage": {"input_tokens": 40, "output_tokens": 12}}
    if live:
        answer["content"][0]["text"] = "Synthetic live turn is waiting for its next token."
        answer["stop_reason"] = None
        return [row("user", 1, prompt), row("assistant", 2, answer)]
    return [row("user", 1, prompt), row("assistant", 2, answer), {"type": "ai-title", "aiTitle": f"Synthetic {topic}", "sessionId": session_id}]


def make_opencode(home: Path, entries: list[tuple[str, str, dt.datetime, str]]) -> None:
    path = home / ".local/share/opencode/opencode.db"
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    db = sqlite3.connect(path)
    db.executescript("""
      CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, name TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_initialized INTEGER, sandboxes TEXT NOT NULL DEFAULT '[]');
      CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL, directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL, share_url TEXT, summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT, revert TEXT, permission TEXT, agent TEXT, model TEXT, cost REAL NOT NULL DEFAULT 0, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_compacting INTEGER, time_archived INTEGER);
      CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
      CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
      CREATE TABLE todo (session_id TEXT NOT NULL, content TEXT NOT NULL, status TEXT NOT NULL, priority TEXT NOT NULL, position INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, PRIMARY KEY (session_id, position));
      CREATE TABLE session_share (session_id TEXT PRIMARY KEY, id TEXT NOT NULL, secret TEXT NOT NULL, url TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
    """)
    for index, (key, cwd, at, topic) in enumerate(entries):
        session_id = "ses_" + identity("opencode", key).replace("-", "")[:28]
        project_id = identity("opencode-project", str(Path(cwd).parent))
        created = int(at.timestamp() * 1000)
        db.execute("INSERT OR IGNORE INTO project (id,worktree,vcs,name,time_created,time_updated) VALUES(?,?,'git',?,?,?)", (project_id, str(Path(cwd).parent), Path(cwd).parent.name, created, created))
        model = json.dumps({"id": "claude-sonnet-4-5", "providerID": "anthropic"}, separators=(",", ":"))
        db.execute("INSERT INTO session (id,project_id,parent_id,slug,directory,title,version,agent,model,cost,time_created,time_updated) VALUES(?,?,NULL,?,?,?,?,?,?,?, ?,?)", (session_id, project_id, f"synthetic-{index:03d}", cwd, f"Synthetic {topic}", "1.0.20", "build", model, 0.01, created, created + 4000))
        for ordinal, (role, body, offset) in enumerate((("user", f"Review synthetic {topic} fixture.", 1000), ("assistant", f"Synthetic {topic} work is complete.", 3000))):
            message_id = f"msg_{identity('opencode-message', key + ':' + str(ordinal)).replace('-', '')[:32]}"
            data = {"id": message_id, "sessionID": session_id, "role": role, "time": {"created": created + offset}}
            if role == "assistant":
                data.update({"time": {"created": created + offset, "completed": created + offset + 500}, "modelID": "claude-sonnet-4-5", "providerID": "anthropic", "tokens": {"input": 40, "output": 12, "reasoning": 0, "cache": {"read": 0, "write": 0}}})
            db.execute("INSERT INTO message VALUES(?,?,?,?,?)", (message_id, session_id, created + offset, created + offset, json.dumps(data, separators=(",", ":"))))
            part_id = f"prt_{identity('opencode-part', key + ':' + str(ordinal)).replace('-', '')[:32]}"
            part = {"id": part_id, "sessionID": session_id, "messageID": message_id, "type": "text", "text": body}
            db.execute("INSERT INTO part VALUES(?,?,?,?,?,?)", (part_id, message_id, session_id, created + offset, created + offset, json.dumps(part, separators=(",", ":"))))
    db.commit()
    db.close()
    os.chmod(path, 0o600)


def generate(out: Path, lane: str, now: dt.datetime) -> dict:
    if lane not in {"a", "b", "c"}:
        raise ValueError("--lane must be a, b, or c")
    if out.exists() and any(out.iterdir()):
        raise ValueError(f"refusing to overwrite non-empty output directory: {out}")
    out.mkdir(parents=True, exist_ok=True, mode=0o700)
    home = out / "home"
    home.mkdir(mode=0o700)
    records: list[dict] = []
    if lane in {"a", "b"}:
        root = home / "workspaces"
        root.mkdir(mode=0o700)
        # Recent records are stable and intentionally newest-first across agents.
        for index in range(34):
            key = f"codex-recent-{index + 1:02d}"
            at = now - dt.timedelta(minutes=8 * index + 2)
            cwd = str(root / "maya-project")
            sid = identity("codex", key)
            path = home / ".codex/sessions" / at.strftime("%Y/%m/%d") / f"rollout-{at.strftime('%Y-%m-%dT%H-%M-%S')}-{sid}.jsonl"
            write_jsonl(path, codex_rows(sid, cwd, at, key), at + dt.timedelta(seconds=4))
            records.append({"id": key, "agent": "codex", "startedAt": stamp(at), "cwd": cwd, "path": path.relative_to(home).as_posix(), "leaveOut": False, "eligible": True})
        for index in range(6):
            key = f"codex-older-{index + 1:02d}"
            at = now - dt.timedelta(days=15, minutes=index * 20)
            cwd = str(root / "maya-project")
            sid = identity("codex", key)
            path = home / ".codex/sessions" / at.strftime("%Y/%m/%d") / f"rollout-{at.strftime('%Y-%m-%dT%H-%M-%S')}-{sid}.jsonl"
            write_jsonl(path, codex_rows(sid, cwd, at, key), at + dt.timedelta(seconds=4))
            records.append({"id": key, "agent": "codex", "startedAt": stamp(at), "cwd": cwd, "path": path.relative_to(home).as_posix(), "leaveOut": False, "eligible": False})
        for index in range(2):
            key = f"codex-personal-{index + 1:02d}"
            at = now - dt.timedelta(minutes=11 + index)
            cwd = str(home / "personal" / "private-project")
            sid = identity("codex", key)
            path = home / ".codex/sessions" / at.strftime("%Y/%m/%d") / f"rollout-{at.strftime('%Y-%m-%dT%H-%M-%S')}-{sid}.jsonl"
            write_jsonl(path, codex_rows(sid, cwd, at, key), at + dt.timedelta(seconds=4))
            records.append({"id": key, "agent": "codex", "startedAt": stamp(at), "cwd": cwd, "path": path.relative_to(home).as_posix(), "leaveOut": True, "eligible": False})
        claude_dir = home / ".claude/projects" / ("-" + str(root / "maya-project").strip("/").replace("/", "-"))
        for index in range(12):
            key = f"claude-recent-{index + 1:02d}"
            at = now - dt.timedelta(minutes=5 + index * 13)
            cwd = str(root / "maya-project")
            sid = identity("claude", key)
            path = claude_dir / f"{sid}.jsonl"
            write_jsonl(path, claude_rows(sid, cwd, at, key), at + dt.timedelta(seconds=2))
            records.append({"id": key, "agent": "claude", "startedAt": stamp(at), "cwd": cwd, "path": path.relative_to(home).as_posix(), "leaveOut": False, "eligible": True})
        if lane == "b":
            key = "claude-live"
            at = now - dt.timedelta(minutes=1)
            cwd = str(root / "maya-project")
            sid = identity("claude", key)
            path = claude_dir / f"{sid}.jsonl"
            write_jsonl(path, claude_rows(sid, cwd, at, key, live=True), at + dt.timedelta(seconds=2))
            records.append({"id": key, "agent": "claude", "startedAt": stamp(at), "cwd": cwd, "path": path.relative_to(home).as_posix(), "leaveOut": False, "eligible": True, "live": True})
        open_entries: list[tuple[str, str, dt.datetime, str]] = []
        for index in range(3):
            key = f"opencode-recent-{index + 1:02d}"
            at = now - dt.timedelta(minutes=7 + index * 9)
            open_entries.append((key, str(root / "maya-project"), at, key))
            records.append({"id": key, "agent": "opencode", "startedAt": stamp(at), "leaveOut": False, "eligible": True})
        for index in range(20):
            key = f"opencode-older-{index + 1:02d}"
            at = now - dt.timedelta(days=16, minutes=index * 10)
            open_entries.append((key, str(root / "maya-project"), at, key))
            records.append({"id": key, "agent": "opencode", "startedAt": stamp(at), "leaveOut": False, "eligible": False})
        make_opencode(home, open_entries)
    spec = json.loads(SPEC.read_text(encoding="utf-8"))
    tracked = sorted((p for p in home.rglob("*") if p.is_file()), key=lambda p: p.relative_to(home).as_posix())
    content = [{"path": p.relative_to(home).as_posix(), "bytes": p.stat().st_size, "sha256": hashlib.sha256(p.read_bytes()).hexdigest()} for p in tracked]
    selected = sorted((r for r in records if r.get("eligible") and not r.get("leaveOut")), key=lambda r: r["startedAt"], reverse=True)[: spec["catchupLimit"]]
    summary = {
        "schema": "t52-home/v1", "dataset": {"a": "D-A", "b": "D-B", "c": "D-C"}[lane], "lane": lane,
        "generatedAt": stamp(now), "home": "home", "expectedCatchup": len(selected), "expectedAgentCounts": {agent: sum(r["agent"] == agent for r in selected) for agent in ("codex", "claude", "cursor", "opencode")},
        "expectedLeaveOut": sum(r.get("leaveOut", False) for r in records), "sessions": records, "files": content,
        "selection": spec["selection"], "liveSessions": [r["id"] for r in records if r.get("live")],
        "devices": spec["lanes"].get(lane, {}).get("devices", {}),
    }
    (out / "manifest.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    os.chmod(out / "manifest.json", 0o600)
    return summary


def verify(out: Path) -> dict:
    data = json.loads((out / "manifest.json").read_text(encoding="utf-8"))
    home = out / data["home"]
    for entry in data["files"]:
        path = home / entry["path"]
        raw = path.read_bytes()
        if len(raw) != entry["bytes"] or hashlib.sha256(raw).hexdigest() != entry["sha256"]:
            raise ValueError(f"manifest mismatch: {entry['path']}")
    candidates = [r for r in data["sessions"] if r.get("eligible") and not r.get("leaveOut")]
    selected = sorted(candidates, key=lambda r: r["startedAt"], reverse=True)[:45]
    if len(selected) != data["expectedCatchup"]:
        raise ValueError("manifest catch-up count mismatch")
    if data["lane"] in {"a", "b"}:
        counts = {agent: sum(r["agent"] == agent for r in selected) for agent in ("codex", "claude", "cursor", "opencode")}
        if counts != data["expectedAgentCounts"]:
            raise ValueError("manifest per-agent catch-up counts mismatch")
    return data


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--lane", choices=("a", "b", "c"))
    parser.add_argument("--out", type=Path)
    parser.add_argument("--now", help="RFC3339 clock; defaults to current UTC")
    parser.add_argument("--verify", type=Path, help="verify an existing generated home")
    args = parser.parse_args()
    if args.verify:
        data = verify(args.verify)
        print(f"{data['dataset']} manifest OK; catch-up={data['expectedCatchup']} leave-out={data['expectedLeaveOut']} files={len(data['files'])}")
        return
    if not args.lane or not args.out:
        parser.error("--lane and --out are required unless --verify is used")
    now = dt.datetime.now(dt.timezone.utc)
    if args.now:
        now = dt.datetime.fromisoformat(args.now.replace("Z", "+00:00"))
        if now.tzinfo is None:
            raise ValueError("--now must include a timezone")
        now = now.astimezone(dt.timezone.utc)
    data = generate(args.out, args.lane, now)
    print(f"{data['dataset']} manifest written; catch-up={data['expectedCatchup']} leave-out={data['expectedLeaveOut']} files={len(data['files'])} output={args.out}")

if __name__ == "__main__":
    main()
