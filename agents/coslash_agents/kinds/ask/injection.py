import html
import re
import unicodedata


INSTRUCTIONS = re.compile(
    r"\b(?:ignore|disregard|forget|override|bypass|discard)\b.{0,100}"
    r"\b(?:instructions?|directions?|rules?|guidelines?|prompts?)\b"
    r"|\b(?:assistant|agent|model|chatgpt|system)\b.{0,100}"
    r"\b(?:ignore|follow|use|call|invoke|run|execute|post|send|share|reveal|repeat|print|output)\b"
    r"|\b(?:use|call|invoke|run|execute)\b.{0,60}\b(?:tools?|functions?|commands?|api)\b"
    r"|\b(?:post|send|share|forward|publish|notify|ping|message|tell|broadcast|announce)\b"
    r".{0,120}(?:[#@][\w-]+|\b(?:channels?|everyone|everybody)\b)"
    r"|\b(?:post|send|share|dump|export|upload|forward|publish)\b.{0,100}"
    r"\b(?:all|every)\b.{0,50}\b(?:sessions?|messages?|data)\b"
    r"|\b(?:reply|reveal|show|expose|print|include|send|post|share|provide)\b"
    r".{0,100}\b(?:private|secret|confidential)\b"
)


def normalize(text):
    for _ in range(3):
        text = html.unescape(text)
    text = unicodedata.normalize("NFKC", text).casefold()
    text = "".join(c for c in text if unicodedata.category(c) != "Cf")
    return " ".join(re.findall(r"[#@][\w-]+|\w+", text))


def remember_instructions(value, passages):
    if isinstance(value, str):
        for sentence in re.split(r"(?<=[.!?])\s+|[\r\n]+", value):
            normalized = normalize(sentence)
            if INSTRUCTIONS.search(normalized):
                words = normalized.split()
                passages.update(tuple(words[i : i + 5]) for i in range(len(words) - 4))
                passages.update(
                    (word.lstrip("@#"),)
                    for word in words
                    if re.fullmatch(r"[@#][uc][a-z0-9]{6,}", word)
                )
    elif isinstance(value, list):
        for item in value:
            remember_instructions(item, passages)
    elif isinstance(value, dict):
        for item in value.values():
            remember_instructions(item, passages)


def unsafe_answer(text, passages):
    normalized = normalize(text)
    if INSTRUCTIONS.search(normalized):
        return True
    words = normalized.split()
    return any((word.lstrip("@#"),) in passages for word in words) or any(
        tuple(words[i : i + 5]) in passages for i in range(len(words) - 4)
    )
