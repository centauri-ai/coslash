import html
import re
import unicodedata

from coslash_agents.safety import strip_slack_controls


OVERRIDE = re.compile(
    r"\b(?:ignore|disregard|forget)\s+"
    r"(?:(?:all|the|any|your|previous|prior|above)\s+)*"
    r"(?:instructions?|directions?|rules?)\b"
    r"|\bsystem\s+prompt\b|\byou\s+are\s+now\b|\bdeveloper\s+message\b"
)
REPEAT = re.compile(
    r"\b(?:assistant|agent|model|chatgpt)\s+"
    r"(?:(?:please|can\s+you|could\s+you|would\s+you)\s+)?"
    r"(?:repeat|say|print|output)\b"
)


def normalize(text):
    for _ in range(3):
        text = html.unescape(text)
    text = strip_slack_controls(text)
    text = unicodedata.normalize("NFKC", text).casefold()
    text = "".join(c for c in text if unicodedata.category(c) != "Cf")
    return " ".join(re.findall(r"[#@][\w-]+|\w+", text))


def remember_instructions(value, passages):
    if isinstance(value, str):
        for part in re.split(r"(?<=[.!?;])\s+|[\r\n]+", value):
            sentence = normalize(part)
            for pattern in (OVERRIDE, REPEAT):
                for match in pattern.finditer(sentence):
                    words = sentence[match.end() :].split()
                    passages.update(zip(words, words[1:]))
    elif isinstance(value, list):
        for item in value:
            remember_instructions(item, passages)
    elif isinstance(value, dict):
        for item in value.values():
            remember_instructions(item, passages)


def unsafe_answer(text, passages):
    normalized = normalize(text)
    if OVERRIDE.search(normalized):
        return True
    words = normalized.split()
    return any(pair in passages for pair in zip(words, words[1:]))
