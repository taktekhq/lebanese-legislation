"""Exact canonical Unicode chunks, compatible with the deployed Go implementation."""
from dataclasses import dataclass
from hashlib import sha256

MODEL = 'gemini-embedding-001'
DIMENSIONS = 768
CHUNK_SIZE = 900
OVERLAP = 120


@dataclass(frozen=True)
class Chunk:
    start: int
    end: int
    text: str
    hash: str


def digest(text: str) -> str:
    return sha256(text.encode('utf-8')).hexdigest()


def chunks(text: str):
    if not text.strip():
        return
    hashed = digest(text)
    for start in range(0, len(text), CHUNK_SIZE - OVERLAP):
        end = min(start + CHUNK_SIZE, len(text))
        yield Chunk(start, end, text[start:end], hashed)
        if end == len(text):
            break


def reusable(text: str, hashed: str, start: int, end: int, excerpt: str) -> bool:
    """Only accept canonical chunk boundaries, not arbitrary matching substrings."""
    return (hashed == digest(text) and start >= 0 and start % 780 == 0
            and end == min(start + CHUNK_SIZE, len(text)) and end > start
            and (start == 0 or start + OVERLAP < len(text)) and text[start:end] == excerpt)
