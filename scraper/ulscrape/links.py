"""Links from law pages to their chapters and articles.

Law.aspx is metadata plus a table of contents: the text of each article lives
on LawArticles.aspx, whose url needs both the article id and the law id (an
article id alone is a 500). Most law pages list every article. About 2% --
the big codes -- list only chapters, and a chapter page expands the tree to
reveal the articles beneath it.
"""
from __future__ import annotations

import html
import re

_ART = re.compile(
    r"LawArticles\.aspx\?LawArticleID=(\d+)&(?:amp;)?LawId=(\d+)[^'\"]*['\"][^>]*>([^<]*)",
    re.I)
_SEC = re.compile(
    r"LawArticles\.aspx\?LawTreeSectionID=(\d+)&(?:amp;)?LawID=(\d+)[^'\"]*['\"][^>]*>([^<]*)",
    re.I)
# chapter labels end with the article range they hold: "الباب الاول: … (2 - 8)"
_RANGE = re.compile(r"\((\d+)\s*-\s*(\d+)\)")


def _label(s: str) -> str:
    return " ".join(html.unescape(s).split())


def extract(raw: bytes):
    """Return (articles, chapters) as [(id, law_id, label)] in page order."""
    text = raw.decode("utf-8", "replace")
    arts = [(int(a), int(law), _label(t)) for a, law, t in _ART.findall(text)]
    secs = [(int(s), int(law), _label(t)) for s, law, t in _SEC.findall(text)]
    return arts, secs


def article_number(label: str) -> str:
    return label.replace("مادة", "").strip()


def covered(chapter_label: str, known: set[str]) -> bool:
    """True when every article in the chapter's "(a - b)" range is already known."""
    m = _RANGE.search(chapter_label)
    if not m:
        return False
    lo, hi = int(m.group(1)), int(m.group(2))
    return hi >= lo and all(str(n) in known for n in range(lo, hi + 1))
