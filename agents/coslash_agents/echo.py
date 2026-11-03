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
VERB = r"(?:repeat|say|print|output|write|echo)"
ADDRESSED = re.compile(
    r"\b(?:assistant|agent|model|chatgpt)\s+"
    rf"(?:(?:please|can\s+you|could\s+you|would\s+you)\s+)?{VERB}\b"
)
QUESTION = re.compile(
    rf"^(?:(?:assistant|agent|model|chatgpt)\s+)?"
    rf"(?:(?:please|can\s+you|could\s+you|would\s+you)\s+)?{VERB}\b"
)
REPEAT = re.compile(rf"\b{VERB}\b")
NEXT_COMMAND = re.compile(
    r"\s+(?:(?:and\s+)?then\s+|and\s+(?=(?:use|call|post|send|share|publish|"
    r"repeat|say|print|output|write|echo)\b))"
)
COMMON = frozenset(
    "a an and are as at be by for from in is it its of on or that the this to was will with "
    "all any next previous prior above".split()
)


def normalize(text):
    for _ in range(3):
        text = html.unescape(text)
    text = strip_slack_controls(text)
    text = unicodedata.normalize("NFKC", text).casefold()
    text = "".join(c for c in text if unicodedata.category(c) != "Cf")
    return " ".join(re.findall(r"[#@][\w-]+|\w+", text))


def remember_instructions(value, passages, *, question=None):
    if isinstance(value, str):
        is_question = question is not None and normalize(value) == normalize(question)
        for part in re.split(r"(?<=[.!?;])\s+|[\r\n]+", value):
            sentence = normalize(part)
            matches = list(ADDRESSED.finditer(sentence))
            override = OVERRIDE.search(sentence)
            if override:
                matches.extend(
                    match
                    for match in REPEAT.finditer(sentence, override.end())
                    if re.fullmatch(
                        r"(?:(?:and|then|please|now)\s*)*",
                        sentence[override.end() : match.start()].strip(),
                    )
                )
            question_request = QUESTION.match(sentence) if is_question else None
            if question_request:
                matches.append(question_request)
            for match in matches:
                words = NEXT_COMMAND.split(sentence[match.end() :], maxsplit=1)[0].split()
                if len(words) < 2:
                    continue
                if is_question and question_request and match.start() == 0:
                    passages.add(tuple(words[:2]))
                passages.add(tuple(words))
                passages.update(
                    tuple(words[i : i + 3])
                    for i in range(len(words) - 2)
                    if not set(words[i : i + 3]).issubset(COMMON)
                )
    elif isinstance(value, list):
        for item in value:
            remember_instructions(item, passages, question=question)
    elif isinstance(value, dict):
        for item in value.values():
            remember_instructions(item, passages, question=question)


def unsafe_answer(text, passages):
    normalized = normalize(text)
    if OVERRIDE.search(normalized):
        return True
    words = normalized.split()
    return any(
        tuple(words[i : i + length]) in passages
        for length in {len(payload) for payload in passages}
        for i in range(len(words) - length + 1)
    )
