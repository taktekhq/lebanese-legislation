"""Registry of scrapable content types on legallaw.ul.edu.lb.

Every document type on the site is reachable by a single numeric id on a
detail page, which is the only enumeration method that can be *proven*
complete: all of the site's list/search views silently truncate (legislation
search caps at 20 rows, agreements at 10) and none of them expose a working
results pager.

Validity is decided by comparing a page against that type's "empty baseline"
-- the page the server returns for an id that does not exist. Baselines are
byte-deterministic once ViewState is stripped (see net.normalise), so an
exact hash comparison is a reliable record-exists test.
"""
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Target:
    name: str
    path: str                 # url template, '{id}' substituted
    label: str                # Arabic section name
    start: int = 1            # lowest id worth probing
    probe_ceiling: int = 0    # 0 => discover by galloping
    # Index pages that advertise per-bucket counts, used to verify completeness.
    index_urls: tuple = field(default_factory=tuple)
    notes: str = ""
    # set for pages only reachable through another type's links; the url
    # template then also takes '{parent}' (see links.py)
    parent: str = ""
    # False for pages fetched only for ids we already hold: no invalid-id
    # baseline is learnt, is_blank alone decides
    baseline: bool = True


BASE = "https://legallaw.ul.edu.lb/"
BASE_FALLBACK = ""   # none: the bare IP fails the TLS name check (UL is HTTPS-only since Oct 2026)

# Sentinel id used to capture each type's "record absent" page.
ABSENT_PROBE_ID = 999999999

TARGETS = {
    "law": Target(
        "law", "Law.aspx?lawId={id}&language=ar", "النصوص القانونية",
        start=150_000,
        index_urls=("LegisltaionSearch.aspx?language=ar",),
        notes="101,570 advertised across 1900-2025; ids observed 1.7e5-3.34e5",
    ),
    "ruling": Target(
        "ruling", "ViewRulePage.aspx?ID={id}&language=ar", "الأحكام / القرارات",
        start=1,
        index_urls=("AdvancedRulingSearch.aspx?language=ar",
                    "ReferenceRulings.aspx?language=ar"),
        notes="47,350 advertised on ReferenceRulings; ids observed up to 1.35e5",
    ),
    "research": Target(
        "research", "ViewResearchPage.aspx?id={id}&language=ar", "الأبحاث والدراسات",
        start=1, probe_ceiling=5_000,
        index_urls=("ResearchesList.aspx?language=ar",),
    ),
    "agreement": Target(
        "agreement", "ViewAgreementPage.aspx?ID={id}", "الاتفاقيات",
        start=1, probe_ceiling=40_000,
        index_urls=("AgreementPage.aspx?language=ar",),
        notes="1,733 advertised across 1921-2024",
    ),
    "opinion": Target(
        "opinion", "ViewOpinionPage.aspx?ID={id}", "الآراء الاستشارية",
        start=1,
        index_urls=("OpinionPage.aspx?language=ar",),
    ),
    "article": Target(
        "article", "RefPageDetails.aspx?id={id}", "القاعدة البيبليوغرافية / المقالات",
        start=1, probe_ceiling=40_000,
        index_urls=("ReferencesPage.aspx?language=ar",),
        notes="848 advertised across 2007-2024",
    ),
    "journal": Target(
        "journal", "OfficialJournal.aspx?Journal={id}&language=ar", "الجريدة الرسمية",
        start=1, probe_ceiling=30_000,
        index_urls=("SearchOfficialJournal.aspx?language=ar",),
        notes="issue pages list the laws they published",
    ),
    "news": Target(
        "news", "NewsDetailPage.aspx?id={id}&language=ar", "الأخبار",
        start=1, probe_ceiling=20_000,
    ),
    "lawtext": Target(
        "lawtext", "LawView.aspx?opt=view&LawID={id}", "النص الكامل للتشريع",
        baseline=False,
        notes="whole consolidated text of a law on one page (ar + fr, chapter "
              "headings, signatures); the Code of Obligations is 1.5 MB / ~40 s",
    ),
    "lawsection": Target(
        "lawsection", "LawArticles.aspx?LawTreeSectionID={id}&LawID={parent}&language=ar",
        "فهرس التشريع", parent="law", baseline=False,
        notes="chapter pages; fetched only where the law page leaves articles unlisted",
    ),
    "lawarticle": Target(
        "lawarticle", "LawArticles.aspx?LawArticleID={id}&LawId={parent}&language=ar",
        "مواد التشريع", parent="law", baseline=False,
        notes="the article text itself (Arabic, often French too); Law.aspx has none",
    ),
    "custompage": Target(
        "custompage", "CustomPage.aspx?id={id}&language=ar", "صفحات ثابتة",
        start=1, probe_ceiling=500,
    ),
}

# Static index/browse pages worth archiving verbatim each run: they carry the
# site's own per-year document counts, which is how we verify completeness.
STATIC_PAGES = (
    "Default.aspx",
    "LegisltaionSearch.aspx?language=ar",
    "AdvancedRulingSearch.aspx?language=ar",
    "ReferenceRulings.aspx?language=ar",
    "AgreementPage.aspx?language=ar",
    "OpinionPage.aspx?language=ar",
    "ReferencesPage.aspx?language=ar",
    "ResearchesList.aspx?language=ar",
    "PeriodsListPage.aspx?language=ar",
    "SearchOfficialJournal.aspx?language=ar",
    "Contact.aspx",
)
