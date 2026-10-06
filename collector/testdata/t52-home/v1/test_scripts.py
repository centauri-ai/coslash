"""Focused safety and contract checks for the T52 synthetic-home scripts."""
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest


HERE = Path(__file__).resolve().parent
GENERATOR = HERE / "generate.py"
APPENDER = HERE / "append-turn.py"
NOW = "2026-10-06T12:00:00Z"


def generate(out: Path, lane: str, *, cwd: Path | None = None) -> None:
    subprocess.run(
        [sys.executable, str(GENERATOR), "--lane", lane, "--out", str(out), "--now", NOW],
        cwd=cwd,
        check=True,
        capture_output=True,
        text=True,
    )


class SyntheticHomeScriptsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_relative_output_keeps_embedded_workspace_paths_absolute(self) -> None:
        generate(Path("./relative-fixture"), "a", cwd=self.root)
        out = self.root / "relative-fixture"
        manifest = json.loads((out / "manifest.json").read_text(encoding="utf-8"))
        self.assertTrue(all(Path(row["cwd"]).is_absolute() for row in manifest["sessions"] if row.get("cwd")))

        db = sqlite3.connect(out / "home/.local/share/opencode/opencode.db")
        try:
            self.assertTrue(all(Path(row[0]).is_absolute() for row in db.execute("SELECT directory FROM session")))
            self.assertTrue(all(Path(row[0]).is_absolute() for row in db.execute("SELECT worktree FROM project")))
        finally:
            db.close()

    def test_claude_rows_keep_parent_lineage_through_appended_turn(self) -> None:
        out = self.root / "lane-b"
        generate(out, "b")
        manifest = json.loads((out / "manifest.json").read_text(encoding="utf-8"))
        record = next(row for row in manifest["sessions"] if row["id"] == "claude-live")
        session_path = out / "home" / record["path"]
        rows = [json.loads(line) for line in session_path.read_text(encoding="utf-8").splitlines()]
        self.assertIsNone(rows[0]["parentUuid"])
        self.assertEqual(rows[1]["parentUuid"], rows[0]["uuid"])

        subprocess.run([sys.executable, str(APPENDER), str(out), "--now", NOW], check=True, capture_output=True, text=True)
        appended = [json.loads(line) for line in session_path.read_text(encoding="utf-8").splitlines()]
        self.assertEqual(appended[-2]["parentUuid"], appended[-3]["uuid"])
        self.assertEqual(appended[-1]["parentUuid"], appended[-2]["uuid"])

    def test_lane_c_manifest_keeps_clean_user(self) -> None:
        out = self.root / "lane-c"
        generate(out, "c")
        manifest = json.loads((out / "manifest.json").read_text(encoding="utf-8"))
        self.assertEqual(manifest["cleanUser"], "t52clean")

    def test_append_rejects_absolute_and_traversal_manifest_paths(self) -> None:
        out = self.root / "lane-b"
        generate(out, "b")
        outside = self.root / "outside.jsonl"
        original = '{"synthetic":"must remain untouched"}\n'
        outside.write_text(original, encoding="utf-8")
        unsafe_paths = [str(outside), "../../outside.jsonl"]
        symlink = out / "home" / "outside-link.jsonl"
        try:
            symlink.symlink_to(outside)
        except (NotImplementedError, OSError):
            pass
        else:
            unsafe_paths.append("outside-link.jsonl")
        manifest_path = out / "manifest.json"
        original_manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

        for unsafe_path in unsafe_paths:
            with self.subTest(path=unsafe_path):
                manifest = json.loads(json.dumps(original_manifest))
                next(row for row in manifest["sessions"] if row["id"] == "claude-live")["path"] = unsafe_path
                manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
                result = subprocess.run([sys.executable, str(APPENDER), str(out), "--now", NOW], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(outside.read_text(encoding="utf-8"), original)

    def test_append_rejects_timezone_naive_now_without_mutating_fixture(self) -> None:
        out = self.root / "lane-b"
        generate(out, "b")
        manifest_path = out / "manifest.json"
        manifest_before = manifest_path.read_bytes()
        record = next(row for row in json.loads(manifest_before)["sessions"] if row["id"] == "claude-live")
        session_path = out / "home" / record["path"]
        session_before = session_path.read_bytes()

        result = subprocess.run([sys.executable, str(APPENDER), str(out), "--now", "2026-10-06T12:00:00"], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("explicit timezone offset", result.stderr)
        self.assertEqual(manifest_path.read_bytes(), manifest_before)
        self.assertEqual(session_path.read_bytes(), session_before)


if __name__ == "__main__":
    unittest.main()
