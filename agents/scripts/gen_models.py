"""Generate contract models with a stable schema and detect drift."""

import argparse
import json
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts" / "ctb"
OUTPUT = ROOT / "coslash_agents" / "mcp" / "generated.py"


def rebase_refs(value, name):
    if isinstance(value, list):
        return [rebase_refs(v, name) for v in value]
    if isinstance(value, dict):
        result = {k: rebase_refs(v, name) for k, v in value.items()}
        ref = result.get("$ref")
        if isinstance(ref, str):
            if ref.startswith("#/$defs/"):
                result["$ref"] = f"#/$defs/{name}/$defs/{ref[8:]}"
            elif ref == "board-report-1.schema.json":
                result["$ref"] = "#/$defs/BoardReport"
        return result
    return value


def generate():
    definitions = {}
    docs = {"AgentTask": "agent-task-1.schema.json", "BoardReport": "board-report-1.schema.json"}
    for name, filename in docs.items():
        definitions[name] = rebase_refs(json.loads((CONTRACTS / filename).read_text()), name)
    for tool in json.loads((CONTRACTS / "mcp-tools.json").read_text())["tools"]:
        stem = "".join(part.capitalize() for part in tool["name"].split("_"))
        for field, suffix in (("inputSchema", "Input"), ("outputSchema", "Output")):
            name = stem + suffix
            definitions[name] = rebase_refs(tool[field], name)
    schema = {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "type": "object",
        "properties": {name: {"$ref": f"#/$defs/{name}"} for name in definitions},
        "$defs": definitions,
    }
    with tempfile.TemporaryDirectory() as tmp:
        source, output = Path(tmp) / "contracts.json", Path(tmp) / "generated.py"
        source.write_text(json.dumps(schema, indent=2))
        subprocess.run(
            [
                "datamodel-codegen",
                "--input",
                str(source),
                "--input-file-type",
                "jsonschema",
                "--output",
                str(output),
                "--output-model-type",
                "pydantic_v2.BaseModel",
                "--class-name",
                "ContractModels",
                "--disable-timestamp",
            ],
            check=True,
        )
        text = output.read_text()
        return re.sub(r"^#   filename:.*$", "#   filename: contracts.json", text, flags=re.M)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = generate()
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text() != generated:
            raise SystemExit("generated models are out of date")
    else:
        OUTPUT.write_text(generated)


if __name__ == "__main__":
    main()
