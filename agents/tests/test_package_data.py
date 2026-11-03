import shutil
import subprocess
import sys
from pathlib import Path
from zipfile import ZipFile


def test_wheel_contains_prompts(tmp_path):
    source = Path(__file__).resolve().parents[1]
    project = tmp_path / "agents"
    shutil.copytree(
        source,
        project,
        ignore=shutil.ignore_patterns(
            ".pytest_cache", ".ruff_cache", "__pycache__", "*.egg-info", "build", "dist"
        ),
    )
    subprocess.run(
        [
            sys.executable,
            "-m",
            "pip",
            "wheel",
            "--no-deps",
            "--no-build-isolation",
            "--wheel-dir",
            str(tmp_path),
            str(project),
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    wheel = next(tmp_path.glob("coslash_agents-*.whl"))
    with ZipFile(wheel) as archive:
        for kind in ("ask", "board", "day_report"):
            path = f"coslash_agents/kinds/{kind}/prompt.md"
            assert archive.read(path) == (source / path).read_bytes()
