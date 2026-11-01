"""Index-page capture and completeness accounting.

Each section's landing page renders a year browser whose labels carry the
site's own document totals, e.g. ``2012  (3581)``. Those numbers are the only
external yardstick for whether an id sweep found everything, so they are
harvested verbatim on every run and stored in the ``advertised`` table.

The same pages are also archived as blobs -- they are part of "their data".
"""
from __future__ import annotations

import html as _html
import re

# <a href="...lawYear=1913...">1913   (1)</a>
_ANCHOR = re.compile(r"""<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>""", re.S)
_LABEL = re.compile(r"^(\d{4})\s*\((\d+)\)$")
_TAGS = re.compile(r"<[^>]+>")
_WS = re.compile(r"\s+")

# id-bearing detail links, per url prefix -> (type, id param)
DETAIL_LINKS = {
    "Law.aspx":              ("law", "lawId"),
    "ViewRulePage.aspx":     ("ruling", "ID"),
    "ViewResearchPage.aspx": ("research", "id"),
    "ViewAgreementPage.aspx": ("agreement", "ID"),
    "ViewOpinionPage.aspx":  ("opinion", "ID"),
    "RefPageDetails.aspx":   ("article", "id"),
    "OfficialJournal.aspx":  ("journal", "Journal"),
    "NewsDetailPage.aspx":   ("news", "id"),
    "CustomPage.aspx":       ("custompage", "id"),
}


def _text(fragment: str) -> str:
    return _WS.sub(" ", _html.unescape(_TAGS.sub("", fragment))).strip()


def year_counts(raw: bytes) -> dict[str, int]:
    """Extract {year: advertised_count} from a section index page."""
    out: dict[str, int] = {}
    body = raw.decode("utf-8", "replace")
    for m in _ANCHOR.finditer(body):
        label = _text(m.group(2))
        hit = _LABEL.match(label)
        if hit:
            out[hit.group(1)] = int(hit.group(2))
    return out


def detail_ids(raw: bytes) -> dict[str, set[int]]:
    """Harvest every (type -> ids) detail link present on a page.

    Used to seed and cross-check the id sweeps: anything the site links to
    must exist in our manifest.
    """
    found: dict[str, set[int]] = {}
    body = raw.decode("utf-8", "replace")
    for href in re.findall(r"""href=["']([^"']+)["']""", body):
        href = _html.unescape(href)
        base, _, qs = href.partition("?")
        base = base.split("/")[-1]
        spec = DETAIL_LINKS.get(base)
        if not spec or not qs:
            continue
        typ, key = spec
        for part in qs.split("&"):
            k, _, v = part.partition("=")
            if k == key and v.isdigit():
                found.setdefault(typ, set()).add(int(v))
    return found


# Section index pages that must be POSTed or given a full query string before
# they reveal their year browser.
SEEDS = {
    "law": "LegisltaionSearch.aspx?language=ar",
    "ruling": "ReferenceRulings.aspx?language=ar",
    "ruling_adv": "AdvancedRulingSearch.aspx?language=ar",
    "article": "ReferencesPage.aspx?language=ar",
    "opinion": "OpinionPage.aspx?searchText=&number=0&year=0&type=-1&option=0&AndOr=AND",
    "agreement": "AgreementPage.aspx?year=0",
}


# Canonical type for each index page that advertises counts. AdvancedRulingSearch
# and ReferenceRulings are two views over the same ruling corpus, so the former
# is kept under a distinct key and only used as a cross-check.
PAGE_TYPE = {
    "LegisltaionSearch.aspx?language=ar": "law",
    "ReferenceRulings.aspx?language=ar": "ruling",
    "AdvancedRulingSearch.aspx?language=ar": "ruling_adv",
    "ReferencesPage.aspx?language=ar": "article",
    "AgreementPage.aspx?language=ar": "agreement",
    "AgreementPage.aspx?year=0": "agreement",
    "OpinionPage.aspx?language=ar": "opinion",
    "OpinionPage.aspx?searchText=&number=0&year=0&type=-1&option=0&AndOr=AND": "opinion",
    "ResearchesList.aspx?language=ar": "research",
    "SearchOfficialJournal.aspx?language=ar": "journal",
}


def year_browse_urls(typ: str, years: list[str]) -> list[str]:
    """Per-year browse URLs (each capped by the site, but still worth archiving
    because they map documents to years and surface ids for cross-checking)."""
    t = {
        "law": "LegisltaionSearch.aspx?searchText=&AndOr=AND&optionID=-1&status=-1"
               "&classification0&lawNumber=0&lawYear={y}&articleNumber=0",
        "ruling": "ReferenceRulings.aspx?searchText=&AndOr=AND&optionID=-1&status=-1"
                  "&Sequence&rulnumber=0&rulYear={y}&courtname=",
        "ruling_adv": "AdvancedRulingSearch.aspx?searchText=&AndOr=AND&courtID=0"
                      "&rulNumber=0&rulYear={y}&judjes=",
        "article": "ReferencesPage.aspx?searchText=&AndOr=AND&rulYear={y}&Sequence=",
        "opinion": "OpinionPage.aspx?searchText=&number=0&year={y}&type=-1&option=0&AndOr=AND",
        "agreement": "AgreementPage.aspx?year={y}",
    }.get(typ)
    return [t.format(y=y) for y in years] if t else []
