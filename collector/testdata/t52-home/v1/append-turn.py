#!/usr/bin/env python3
"""Append one deterministic Claude user/assistant turn to the D-B live session."""
import argparse
import datetime as dt
import json
import hashlib
import os
from pathlib import Path
import uuid

NS = uuid.UUID("ff65a453-7b5d-45cf-b1c8-e488069a10c2")

def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("home", type=Path, help="generated D-B output directory")
    p.add_argument("--session", default="claude-live")
    p.add_argument("--now")
    args = p.parse_args()
    root = args.home
    manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8"))
    record = next((x for x in manifest["sessions"] if x["id"] == args.session and x["agent"] == "claude"), None)
    if record is None:
        raise SystemExit("session is not in this D-B manifest")
    at = dt.datetime.now(dt.timezone.utc) if not args.now else dt.datetime.fromisoformat(args.now.replace("Z", "+00:00")).astimezone(dt.timezone.utc)
    path = root / "home" / record["path"]
    session_id = str(uuid.uuid5(NS, f"claude:{args.session}"))
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    for offset, kind, role, body in ((1, "user", "user", "Continue the synthetic live task."), (2, "assistant", "assistant", "The synthetic task is waiting for the next append-turn.")):
        message = {"role": role, "content": body} if role == "user" else {"role": role, "model": "claude-sonnet-4-5-20250929", "id": "msg_t52_append", "type": "message", "content": [{"type": "text", "text": body}], "stop_reason": None}
        rows.append({"type": kind, "timestamp": (at + dt.timedelta(seconds=offset)).isoformat(timespec="milliseconds").replace("+00:00", "Z"), "sessionId": session_id, "uuid": str(uuid.uuid5(NS, f"append:{args.session}:{kind}:{at.isoformat()}")), "parentUuid": rows[-1].get("uuid"), "cwd": record["cwd"], "gitBranch": "main", "isSidechain": False, "userType": "external", "version": "2.1.3", "message": message})
    path.write_text("".join(json.dumps(row, separators=(",", ":")) + "\n" for row in rows), encoding="utf-8")
    timestamp = at.timestamp()
    path.touch()
    os.utime(path, (timestamp, timestamp))
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    for item in manifest["files"]:
        if item["path"] == record["path"]:
            item["bytes"] = path.stat().st_size
            item["sha256"] = digest
            break
    else:
        manifest["files"].append({"path": record["path"], "bytes": path.stat().st_size, "sha256": digest})
    record["activityAt"] = at.isoformat(timespec="milliseconds").replace("+00:00", "Z")
    (root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    os.chmod(root / "manifest.json", 0o600)
    print(f"appended turn to synthetic Claude session {args.session}")

if __name__ == "__main__":
    main()
