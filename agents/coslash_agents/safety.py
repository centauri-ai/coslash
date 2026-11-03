import copy
import html
import json
import re
from importlib.resources import files


SCHEMA = json.loads(
    files("coslash_agents.contracts").joinpath("ctb/board-report-1.schema.json").read_text()
)
CONTROL = re.compile(r"<(![^>]*|@[A-Z0-9]+|#[A-Z0-9]+(?:\|[^>]*)?|https?://[^>]+)>", re.I)
MARKUP = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f]|<[^>]*>|\[[^\]]+\]\([^)]+\)|[*_~`]")


def quote_untrusted(text):
    return "<untrusted>" + html.escape(text, quote=False) + "</untrusted>"


def strip_slack_controls(text):
    def replace(match):
        value = match.group(1)
        if value.startswith(("!", "@", "#")):
            return ""
        return value.split("|", 1)[-1] if "|" in value else ""

    return CONTROL.sub(replace, text)


def is_plain_text(text):
    return not bool(MARKUP.search(text))


def _ref_key(ref):
    if ref.get("kind") == "session" and isinstance(ref.get("sessionId"), str):
        return ("session", ref["sessionId"])
    if (
        ref.get("kind") == "slack"
        and isinstance(ref.get("channelId"), str)
        and isinstance(ref.get("ts"), str)
    ):
        return ("slack", ref["channelId"], ref["ts"])
    return None


def validate_refs(output_refs, seen_refs):
    seen = {key for ref in seen_refs if (key := _ref_key(ref)) is not None}
    result = []
    for ref in output_refs:
        key = _ref_key(ref)
        if key not in seen:
            continue
        if key[0] == "session":
            result.append({"kind": "session", "sessionId": key[1]})
        else:
            result.append({"kind": "slack", "channelId": key[1], "ts": key[2]})
    return result


def enforce_caps(report):
    result = copy.deepcopy(report)
    truncated = []

    def walk(value, schema, path):
        if "$ref" in schema and schema["$ref"].startswith("#/$defs/"):
            schema = SCHEMA["$defs"][schema["$ref"].split("/")[-1]]
        if isinstance(value, str) and "maxLength" in schema and len(value) > schema["maxLength"]:
            truncated.append({"field": path, "count": len(value) - schema["maxLength"]})
            return value[: schema["maxLength"]]
        if isinstance(value, list):
            maximum = schema.get("maxItems", len(value))
            if len(value) > maximum:
                truncated.append({"field": path, "count": len(value) - maximum})
            return [walk(item, schema.get("items", {}), path) for item in value[:maximum]]
        if isinstance(value, dict):
            return {
                k: walk(v, schema.get("properties", {}).get(k, {}), f"{path}.{k}" if path else k)
                for k, v in value.items()
            }
        return value

    result = walk(result, SCHEMA, "")
    if truncated:
        result.setdefault("truncated", []).extend(truncated)
    return result
