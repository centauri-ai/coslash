from dataclasses import dataclass
import json
from importlib.resources import files

from . import ask, board, day_report


@dataclass(frozen=True)
class Registration:
    handler: object
    tools: frozenset[str]


_contract = json.loads(files("coslash_agents.contracts").joinpath("ctb/mcp-tools.json").read_text())
REGISTRY = {
    kind: Registration(
        handler=handler,
        tools=frozenset(t["name"] for t in _contract["tools"] if kind in t["taskKinds"]),
    )
    for kind, handler in (
        ("board_tick", board.run),
        ("day_report", day_report.run),
        ("ask", ask.run),
    )
}
