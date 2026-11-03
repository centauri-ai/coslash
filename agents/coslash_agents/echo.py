import html
import re
import unicodedata


ACTIONS = (
    r"ignore|disregard|forget|override|bypass|discard|follow|use|call|invoke|run|execute|"
    r"post|send|share|reveal|repeat|print|output|publish"
)
ADDRESSED = re.compile(
    rf"\b(?:assistant|agent|model|chatgpt|system)\s+(?:please\s+)?(?:{ACTIONS})\b"
)
IMPERATIVE = re.compile(
    r"^(?:please\s+)?(?:"
    r"(?:ignore|disregard|forget|override|bypass|discard)\b.{0,100}"
    r"\b(?:instructions?|directions?|rules?|guidelines?|prompts?)\b"
    r"|(?:use|call|invoke|run|execute)\s+(?:the|a|this|your)\b.{0,60}"
    r"\b(?:tools?|functions?|commands?|api)\b"
    r"|(?:post|send|share|forward|publish|notify|ping|message|tell|broadcast|announce)\b"
    r".{0,120}(?:[#@][\w-]+|\b(?:channels?|everyone|everybody)\b)"
    r"|(?:post|send|share|dump|export|upload|forward|publish)\b.{0,100}"
    r"\b(?:all|every)\b.{0,50}\b(?:sessions?|messages?|data)\b"
    r"|(?:reply|reveal|show|expose|print|include|send|post|share|provide)\b"
    r".{0,100}\b(?:private|secret|confidential)\b)"
)
COMMON = frozenset(
    "a an and are as at be by for from in is it its of on or that the this to was will with "
    "all every previous prior instructions directions rules report reports session sessions "
    "message messages data update updates".split()
)


def normalize(text):
    for _ in range(3):
        text = html.unescape(text)
    text = unicodedata.normalize("NFKC", text).casefold()
    text = "".join(c for c in text if unicodedata.category(c) != "Cf")
    return " ".join(re.findall(r"[#@][\w-]+|\w+", text))


def sentences(text):
    for part in re.split(r"(?<=[.!?;])\s+|[\r\n]+", text):
        yield normalize(part)
        for fragment in re.split(r":\s+", part)[1:]:
            yield normalize(fragment)


def directive(text):
    return ADDRESSED.search(text) or IMPERATIVE.match(text)


def remember_instructions(value, passages):
    if isinstance(value, str):
        for sentence in sentences(value):
            match = directive(sentence)
            if not match:
                continue
            words = sentence.split()
            remainder = sentence[match.end() :] if match.re is ADDRESSED else " ".join(words[1:])
            payload = re.split(
                rf"\s+(?:(?:and )?then\s+|and\s+(?=(?:{ACTIONS})\b))",
                remainder,
                maxsplit=1,
            )[0].split()
            if 2 <= len(payload) < 5:
                passages.add(tuple(payload))
            passages.update(tuple(payload[i : i + 5]) for i in range(len(payload) - 4))
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
    if any(directive(sentence) for sentence in sentences(text)):
        return True
    words = normalize(text).split()
    if any((word.lstrip("@#"),) in passages for word in words):
        return True
    return any(
        phrase in passages
        and (length == 5 or len(words) == length or len(set(phrase) - COMMON) >= 2)
        for length in range(2, 6)
        for i in range(len(words) - length + 1)
        if (phrase := tuple(words[i : i + length]))
    )
