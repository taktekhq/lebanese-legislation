# legallaw.ul.edu.lb: page structure recon for the parser

Reconnaissance only. Produced by reading `data/manifest.sqlite` read-only and
decompressing sampled blobs under `data/blobs/`. No network requests, no
archive/manifest/scraper changes. Samples: ~30 docs per type spread across the
doc_id range and across raw_bytes (smallest / median / largest / percentiles),
plus larger scans (up to full-corpus for `research`, up to 400 docs for `law`)
wherever a signal looked rare in the 30-doc sample. All examples below are
truncated to a few words.

Measured `present=1` counts (ground truth from `doc`, for reference against
the task brief's approximate figures):

| type | present=1 | doc_id range |
|---|---:|---|
| law | 101,561 | 7,815 – 333,731 |
| ruling | 47,683 | 5,388 – 157,573 |
| journal | 6,809 | 4,351 – 16,891 |
| opinion | 3,639 | 523 – 23,508 |
| agreement | 2,015 | 2,872 – 5,893 |
| article | 2,574 | 1,555 – 8,321 |
| research | 50 | 14 – 66 |
| news | 19 | 44 – 76 |
| custompage | 15 | 1 – 149 |

`custompage` is 15, not the 1 in the task brief, the manifest is the source
of truth here. `page` table holds 482 archived index/search/static pages.

---

## 0. The single biggest thing to know before writing the parser

**`Law.aspx` (the `law` doc type) is a metadata + navigation shell, not the
statute text.** It carries the preamble ("whereas" clauses), all citation
metadata, and a `RadTreeView` table of contents that names every article , 
but the article bodies themselves live on `LawArticles.aspx?LawArticleID=…`,
and the six "related X" panels (amended articles, related rulings, related
opinions, related bibliography, related research, attachments) are each a
link to a *separate* list page (`AmendedArticles.aspx`,
`LawRelatedRulings.aspx`, `LawRelatedOpinions.aspx`,
`LawRelatedReference.aspx`, `LawRelatedRulingsReference.aspx`,
`LawRelatedResearches.aspx`, `LawRelatedAttachments.aspx`, all
`?lawId=N`). **None of these nine endpoint families are archived**, `page`
has zero rows for any of them, and there is no doc `type` for a law article.
So today's archive can tell you *that* a law has (say) 40 articles and can
name each one, but not what any article says, and it can tell you a "related
rulings" panel exists but not which rulings. This is the central scoping
question for the parser/re-scrape plan (see §7).

By contrast `ruling` (`MainContent_RulingText`), `agreement`
(`MainContent_AgreementText`), `opinion` (`MainContent_OpinionText`) and
`research`/`article` (both reuse `MainContent_RulingText`) **do** carry their
full body text inline on the one archived page. Those five types can be
parsed end-to-end from what's already on disk.

---

## 1. `law` (Law.aspx?lawId=N): 30-doc sample, cross-checked on 400

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb / status category | `MainContent_nav_lblNavLink` | `> التشريعات النافذة > …` | 400/400 |
| title | `MainContent_spanTitle` | `الترخيص لمهندس زراعي …` + optional `(ملغى)` span | 400/400 |
| kind \| number \| date \| status | `MainContent_subdetails` | `نوع النص: قرار \| رقم 180 تاريخ: 29/03/1997` (+ `/ ملغى` if repealed) | 400/400 |
| OJ issue number | `MainContent_divOJNumber` | `18` | 400/400 |
| OJ publish date | `MainContent_divOJPublishDate` | `17/04/1997` | 400/400 |
| OJ page(s) | `MainContent_divOJPage` | `1573-1574` | 400/400 |
| preamble / recitals ("whereas…") | `MainContent_divNotes` | `ان وزير الزراعة، بناء على المرسوم رقم …` ends `يقرر ما يلي:` | ~73% (22/30 small sample; large laws too) |
| second preamble block (explanatory memo, parliamentary bills) | `MainContent_divNotes2` | `وبما ان الحكومة احالت على مجلس النواب …` | ~10%, mostly `قانون`/bills |
| article TOC (RadTreeView) | `MainContent_divSections` → `ctl00_MainContent_treeTopics` | `الفهرس … مادة 1 مادة 2` | 100% present as element; ~empty tree for a few judicial-type `قرار` records (see below) |
| repeals-this-law-classification | `MainContent_cancellaw_lblClassification` | `مرسوم` | 21/120 (only laws that repeal another) |
| repeals → target title+link | `MainContent_cancellaw_cancellawLink` | href `Law/244565/<slug>` | 21/120 |
| repeals → target number/year/date | `cancellaw_lblNumber` / `_lblYear` / `_lblProductionDate` | `13896` / `2005` / `03/01/2005` | 21/120 |
| "amended articles" panel header (link only) | `MainContent_amdarticles_amendedarticles` | text is the static label `المواد المعدلة`; href `AmendedArticles.aspx?lawId=N` | 13/120 |
| "attachments/tables" panel header (link only) | `MainContent_rellawatt_attachmebntLink` | label `جداول / ملاحق`; href `LawRelatedAttachments.aspx?lawId=N` | 25/120 |
| "related judgments" panel header (link only) | `MainContent_relRulings_relrulings` | label `الأحكام المرتبطة`; href `LawRelatedRulings.aspx?lawId=N` | 35/120 |
| "related decisions (bibliography)" header (link only) | `MainContent_relrulref_relrulings` | href `LawRelatedRulingsReference.aspx?lawId=N` | 47/120 |
| "related articles (bibliography)" header (link only) | `MainContent_refref_relrulings` | href `LawRelatedReference.aspx?lawId=N` | 23/120 |
| "related research" header (link only) | `MainContent_RelAttach1_relrulings` | label `الدراسات المرتبطة`; href `LawRelatedResearches.aspx?lawId=N` | 7/120 |
| "related opinions" header (link only) | `MainContent_relop_relrulings` | label `الاراء الاستشارية المرتبطة`; href `LawRelatedOpinions.aspx?lawId=N` | 11/120 |
| view/PDF/print toolbar | `MainContent_aView` / `aPdf` / `aPrint` | → `LawView.aspx?opt=view&LawID=N`, `DownloadLawPage.aspx?...` (opt/print) | 100% |

**Important**: every "related …" row above is *just a clickable header* whose
own element text is the constant Arabic label, it is not a count and not a
list. The panel's presence (element exists at all in the HTML) is the only
signal that the relation is non-empty; the actual related items are on the
un-archived target page. `cancellaw` is the one exception, its five sub-ids
carry real inline data (the repealed/replaced law's classification, title,
number, year, production date), so "law X repeals law Y" is fully
reconstructable from the archive today; the reverse ("law Y was repealed by
law X") is not stated on Y's own page and must be built by inverting X's
`cancellaw` row.

### Status signal

The only machine-legible status marker found anywhere in 300 sampled law
pages is `<span style='color:#c41e32'>ملغى</span>`, appended after the title
in `spanTitle` and after the date in `subdetails` when a law is repealed.
No page ever renders "قيد التطبيق" or "نافذ" as a literal per-document
status string, **"in force" = absence of the ملغى span**, full stop. No
other status value (partial repeal, suspended, etc.) appeared in-page. The
site's own search form (`LegisltaionSearch.aspx`, see §4) proves the
underlying DB has exactly two status codes: `2445 = قيد التطبيق`,
`2443 = ملغى`, but only the repealed one ever prints on the detail page.

### Articles: how المواد are delimited

Confirmed on the smallest law (id 284387, a 2-article decree): the TOC is a
Telerik `RadTreeView` (`ctl00_MainContent_treeTopics`), rendered as flat
`<li class="rtLI">` rows (no real DOM nesting even when the label implies a
chapter), each row an `<a class="rtIn MenuLink" href="…">`:

```
<a href="LawArticles.aspx?LawTreeSectionID=297882&LawID=284387&language=ar">المواد :  (1 - 2)</a>
<a href="LawArticles.aspx?LawArticleID=1107610&LawID=284387&language=ar">مادة  1</a>
<a href="LawArticles.aspx?LawArticleID=1107611&LawID=284387&language=ar">مادة  2</a>
```

So the tree has (at least) two node kinds distinguished by query param:
- `LawTreeSectionID=N`: a **section/chapter node** (باب/فصل/قسم or a plain
  "المواد" range header); its own link text is the range label, e.g.
  `الباب الثالث : الاوضاع المعلقة … (176 - 2…)`. These headers do carry
  باب/فصل/قسم names when the law has real chapter structure (seen in a
  936-article law, id 244887); flat 1–2 article decrees just get a bare
  "المواد : (1-2)" pseudo-section.
- `LawArticleID=N`: a **leaf article node**, link text `مادة N`.

Neither node type's target page (`LawArticles.aspx`) is in the archive
(`page` table has 0 rows for it, and it isn't a `doc.type`). Article numbers
and the tree's nesting order are the only structure captured; the article
text, and multi-level باب > فصل > مادة nesting beyond this one flat tree, are
not. `LawArticleID` values do leak into other archived pages as citation
targets (see `ruling`/`research` below), e.g. a ruling citing "مادة رقم 98
نظام مجلس شورى الدولة" links to
`LawArticles.aspx?LawArticleID=971504&LawId=244226`. Harvesting those
scattered citations across rulings/opinions/research is a cheap partial
substitute for a real crawl of `LawArticles.aspx`.

Some `law` rows are not statutes at all but a single tribunal ruling filed
under the legislation type (e.g. doc 326659, a Constitutional Council
election-challenge `قرار`, kind=`قرار`, 941 KB). Its `divSections` tree is
empty (no articles) and its size comes from a single 150-row `<table>`
(vote-recount by list/district) with heavy per-cell inline styling, a
parser targeting "laws" needs to expect occasional judicial-decision-shaped
records with a big embedded table instead of an article tree.

### `law` href catalogue (all distinct patterns seen)

| pattern | points to | notes |
|---|---|---|
| `LawArticles.aspx?LawArticleID=N&LawID=M` | one article of law M | not archived |
| `LawArticles.aspx?LawTreeSectionID=N&LawID=M` | a chapter/section range | not archived |
| `AmendedArticles.aspx?lawId=N` | articles of N later amended | not archived |
| `LawRelatedRulings.aspx?lawId=N` | judgments applying N | not archived |
| `LawRelatedRulingsReference.aspx?lawId=N` | bibliographic "قرارات مرتبطة" | not archived |
| `LawRelatedReference.aspx?lawId=N` | bibliographic articles about N | not archived |
| `LawRelatedResearches.aspx?lawId=N` | studies about N | not archived |
| `LawRelatedOpinions.aspx?lawId=N` | advisory opinions about N | not archived |
| `LawRelatedAttachments.aspx?lawId=N` | tables/annexes of N | not archived |
| `Law/{id}/{url-encoded-title-slug}` | pretty permalink to another law | seen only inside `cancellaw_cancellawLink`; same target as `Law.aspx?lawId={id}` |
| `LawView.aspx?opt=view&LawID=N` | print-styled full view | toolbar link, not separately archived |
| `LawArticles.aspx` links embedded inside `research`/`ruling`/`opinion` bodies | article-level citations | same un-archived endpoint |
| generic nav (`LegisltaionSearch.aspx`, `AdvancedRulingSearch.aspx`, `AgreementPage.aspx`, `OpinionPage.aspx`, `ReferenceRulings.aspx`, `ReferencesPage.aspx`, `ResearchesList.aspx`, `SearchOfficialJournal.aspx`, `PeriodsListPage.aspx`, `CustomPage.aspx?id=N`, `Default.aspx`, `Contact.aspx`) | site chrome, same on every page | archived as static `page` rows for the bare (no-query) forms |
| `javascript:__doPostBack(...)` / `javascript:void(0)` / `javascript:history.go(-1)` / `#` | client-side only | no target |
| `/WebResource.axd?...`, `css/*.css`, `newDesign/css/*.css`, `images/favicon.ico` | ASP.NET/theme assets | irrelevant to content |

---

## 2. `ruling` (ViewRulePage.aspx?ID=N): merges "judgments" + "decisions"

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb | `MainContent_nav_lblNavLink` | `> الأحكام > تمييز جزائي حكم رقم : 244 /2019` | 30/30 |
| title | `MainContent_lblTitle` | `تمييز جزائي حكم رقم : 244 /2019` | 30/30 |
| court/chamber | `MainContent_lblcourtName` | `تمييز جزائي`, `شورى`, `تمييز مدني` | 30/30 |
| ruling number | `MainContent_lblNumber` | `244` | 30/30 |
| year | `MainContent_lblyear` | `2019` | 30/30 (0 in 291/47,683 blank leaks, see §6) |
| decision date | `MainContent_lblDate` | `07/06/2019` | 30/30 |
| **full ruling text** | `MainContent_RulingText` | opening holding paragraph | 29/30 (empty on blank-leak rows) |
| presiding judge line | `MainContent_Chief` | `الرئيس سهير الحركة` | 29/30 |
| presiding judge name only | `MainContent_lblJudge` | `سهير الحركة` | 29/30 |
| panel members line | `MainContent_Members` | `الأعضاء عيد - ابو خاطر` | 29/30 |
| panel members, name only | `MainContent_lblMembers` | `/هرموش//متري//زين//رياشي//المعلم//العوجى/`, separator is `-` for 2 members, `/…/…/` slash-wrapped for ≥3 | 29/30 |
| case-file link | `MainContent_RulingFile` / `MainContent_tdlinka` | label says "نص الحكم كاملا" but href is `RulingFile.aspx?RuliID=N&type=list` (a file list, not a plain document) | 27/30 |
| cited-law repeater | `MainContent_RelatedLaws_rptLaws_linkLaw_0..N` / `_lblLawName_0..N` | `مادة رقم 98 نظام مجلس شورى الدولة` → href `LawArticles.aspx?LawArticleID=971504&LawId=244226` | 5/30 have ≥1; up to 12 citations seen on one ruling |
| view/print | `MainContent_viewLink` / `PrintDoc` | `RulingView.aspx?opt&RulID=N[&TYPE=PRINT]` | 30/30 |

Court/chamber values line up with the 12-entry taxonomy in
`AdvancedRulingSearch.aspx`'s `drpcourt` dropdown (§4), that dropdown *is*
the "court/chamber classification tree" for this type, and it is small
(institutional courts, not a hierarchy, no nesting).

Body markup: mostly `<br>`-separated plain paragraphs, Latin digits
throughout, no Arabic-Indic digits in any sample. Occasional embedded French
legal terms/citations (`Regles de procedure`, Latin-alphabet party names)
inside otherwise all-Arabic text, never a whole French page.

---

## 3. `journal` (OfficialJournal.aspx?Journal=N): Official Gazette issues

| field | element id | example | fill rate |
|---|---|---|---|
| year filter (site chrome, not data) | `MainContent_drp_filteringYear` / `drp_years` | dropdown, `الكل 2025 2024 …` | 30/30 (chrome) |
| issue filter (site chrome) | `MainContent_drp_issue` | always empty in the static snapshot (populated by AJAX) | 0/30 |
| **table of contents of this issue** | `MainContent_results` | rows of `<a href="Law.aspx?lawId=N">title</a>` + page number | 29/30 |

**The issue's own number and publish date are not printed anywhere on its
own `OfficialJournal.aspx?Journal=N` page**, no `<title>`, breadcrumb, or
label carries them; both year dropdowns show "الكل" (all) selected in the
static markup. `Journal=N` is an internal sequential id (range 4,351–16,891)
unrelated in scale to the human-facing issue number (typically 1–~3,000
within a year, e.g. issue "57" of 1967). **Reconstruction path that works**:
every law linked from a journal's `MainContent_results` table already
carries `divOJNumber` / `divOJPublishDate` / `divOJPage` on its own `law`
row; those three fields are identical for every law in the same issue
(verified: journal id 12265 lists law 249830 at page "112", and law
249830's own `divOJPage` is exactly `112`, `divOJNumber=1`,
`divOJPublishDate=01/01/1900`). So: parse `journal` → get its list of
`lawId`s and per-law page numbers → look up any one linked law's OJ
number/date fields → that's the issue's real (year, number, date). No
network fetch needed, just a join within the existing law rows.

---

## 4. `opinion` (ViewOpinionPage.aspx?ID=N): advisory opinions

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb / title | `nav_lblNavLink` / `lblTitle` | `الرأي الأستشاري - رقم 1978/203` | 30/30 |
| opinion year | `MainContent_lbl_opinion_year` | `1978` | 30/30 |
| opinion date | `MainContent_lbl_opinion_date` | `2/23/1978` (M/D/YYYY, unlike every other date field's DD/MM/YYYY) | 30/30 |
| opinion number | `MainContent_lbl_opinion_number` | `203` | 27/30 |
| requesting body number/year | `lbl_foundation_numb` / `_year` | `9999` / `1978` | 30/30 |
| requester (ministry/body) | `MainContent_lbl_opinion_requester` | `وزارة المالية` | 24/30 |
| subject/title text | `MainContent_TitleText` | `طلب اعادة النظر في قرار ديوان المحاسبة …` | 27/30 |
| **full opinion text** | `MainContent_OpinionText` | reasoning paragraph | 26/30 |
| cited-law repeater | `RelatedLaws_rptLaws_linkLaw_N` | same shape as `ruling`, → `LawArticles.aspx?LawArticleID=…` | rare (1/30, but up to 12 citations on that one) |

All 3,639 sampled/titled opinions use the literal title prefix "الرأي
الأستشاري", even though the site's own `OpinionPage.aspx` search form
exposes a 9-value `drp_Source` classification (`رأي استشاري`, `قرار توحيد
اجتهاد`, `قرار رقابة حسابات`, `قرار رقابة مسبقة`, `قرار رقابة موظفين`, …),
none of that sub-typing is visible on the detail page in this sample, it
may be a search-only facet the corpus doesn't otherwise expose. Opinion date
format is M/D/YYYY (`.NET` `ToString()` default) vs. DD/MM/YYYY everywhere
else, a real per-field format inconsistency, not a typo, so the parser must
special-case it.

---

## 5. `agreement` (ViewAgreementPage.aspx?ID=N): treaties/international agreements

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb / title | `nav_lblNavLink` / `lblTitle` | `إتفاقية منحة بين الجمهورية اللبنانية والبنك الألماني…` | 30/30 |
| type | `MainContent_lblType` | `ثنائية` (bilateral) / `دولية` (international), binary | 30/30 |
| ratifying instrument | `MainContent_lblLegislationTool` | `مرسوم رقم 197 سنة 2025` | 30/30 |
| production/signing date | `MainContent_lblProductionDate` | `19/12/2024` | 30/30 |
| ratification-law block | `MainContent_Div1` / `MainContent_law_Link` | label "تشريع المصادقة" + link text = the ratifying law's title; href `Law.aspx?lawId=N` | 27/30 |
| **full agreement text** | `MainContent_AgreementText` | treaty text | 26/30 |
| attachments panel (link only) | `MainContent_attachemntRelated` | `ملحقات الاتفاقية مرفق اتفاقية` | 1/30 |

`law_Link`'s href is the one clean, directly-archived cross-type relation:
**agreement → ratified_by → law** (`Law.aspx?lawId=N`, target present in the
`law` table). Body text frequently embeds French (place names, treaty
boilerplate like "Rue de Stassart", "Bucarest") and occasional Word-pasted
markup (`<span style="font-size: 12pt">`, `<p align="center">`).

208/2,015 present agreements (~10%) have title
`… عنوان الاتفاقية غير متوفر` ("title unavailable") with empty
`AgreementText`/`lblNumber`/`lblDate`, see §6, these are blank-page leaks
that `present=1` does not filter out.

---

## 6. `article` (RefPageDetails.aspx?id=N): bibliography / doctrine articles

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb / title | `nav_lblNavLink` / `lblTitle` | `اين هي مراجعة النقض في القضاء الاداري …` | 30/30 |
| author(s) | `MainContent_lblNumber` (reused id, holds author not a number) | `جوزف الشدياق` or `ادوار /سيمون/انطوان حنا/ سكسك/ عبود` (slash-joined multi-author) | 28/30 |
| title again (reused id) | `MainContent_lblcourtName` | duplicate of `lblTitle` | 28/30 |
| **abstract/summary text** | `MainContent_RulingText` (reused id) | `يبحث المقال في مراجعة النقض امام القضاء الاداري …` | 28/30 |
| page range within series | `MainContent_Label1` | `37/50` | 7/30 |
| series title / number / year card | `MainContent_lblseqtitle` / `lblseqnum` / `lblseqyear` | rendered inside a "بطاقة المسلسل" (series card); all three are **empty `<span>`s** in every sample checked | 0/30 |

Note the site reuses generic control ids (`lblNumber`, `lblcourtName`,
`RulingText`) across unrelated doc types, the parser must key off `type`,
not id name alone, when deciding what a given id means. The series/journal
citation (which periodical this doctrine piece appeared in) is a real field
in the data model, `ReferencesPage.aspx`'s `drp_type` search facet lists 14
named series (`المجلة القضائية`, `مجلة الشرق الادنى`, `مجموعة اجتهادات
حاتم`, …), but it is essentially never populated on the detail page in this
corpus; treat `article.series` as sparse/best-effort.

---

## 7. `research` (ViewResearchPage.aspx?id=N): studies, only 50 records

| field | element id | example | fill rate |
|---|---|---|---|
| breadcrumb / title | `nav_lblNavLink` / `lblTitle` / `lbltitle1` | `دور القضاء في حماية حقوق الانسان` | 30/30 |
| publish date | `MainContent_lbldate` | `01/01/2016` | 30/30 |
| author | `MainContent_lblpublisher` | `عبد السلام شعيب` | 30/30 |
| **full text** | `MainContent_RulingText` (reused id) | full essay | 30/30 |

Smallest doc type by count (50) but the messiest markup: essays are
Word-pasted HTML, 36/40 sampled have inline `style="…"` on spans, 8/40
literally say `Times New Roman`, mixed `<p align="center" dir="RTL">` and
plain `<br>` paragraphing, occasional `<div style="direction: ltr;">` blocks
for embedded French/English quotations. Essays embed real cross-links:
`LawArticles.aspx?LawArticleID=…&lawId=…` citations (un-archived target,
same as elsewhere) and, in 4/50 (8%) of research docs, links to a **different,
legacy hostname** `https://legiliban.ul.edu.lb/Law.aspx?lawId=N`, same path
shape as the live site, worth normalising to `legallaw.ul.edu.lb` when
extracting cross-references.

---

## 8. `news` (19) and `custompage` (15)

`news`: `lblTitle`, `lblDate`, `lblNews` (body, plain `<br>`-separated),
`lblWriter` (16/19, values like `NNA`/`nna`, wire-service byline). Print
link `NewsView.aspx?opt&NewsID=N&TYPE=PRINT`. Editorial/announcement
content, no legal-citation structure.

`custompage`: `lblTitle` (14/15), `lblNews` (13/15, main body) or
`divContent` (3/15, used on a couple of pages instead of/alongside
`lblNews`). These are static informational pages (center history, staff
bios), no relation to the legal corpus, low priority for the parser.

---

## 9. Metadata taxonomies recovered from the `page` table (static search forms)

These are the site's *actual* controlled vocabularies, extracted from
`<select>`/`<option>` markup on archived search pages, this is how the
"classification tree" should be reconstructed, since no browse/category tree
page exists anywhere in the archive:

- **Document kind** (`نوع النص`), from `LegisltaionSearch.aspx`'s
  `drp_clasification`, **49 real values** with stable numeric ids (e.g.
  `49462=قانون`, `49465=قرار`, `49480=مرسوم`, `49481=مرسوم إشتراعى`,
  `49485=نظام`, `49441=اتفاقية`, `49462`… full list captured in the recon
  script's output; includes long-tail Ottoman-era kinds like `براءة سلطانية`,
  `خط همايوني`, `قانون عثماني`). This is the authoritative kind vocabulary , 
  richer than the ~15 values actually seen printed in `subdetails` across
  the sample.
- **Status** (`ddlStatus` on the same page): exactly 2 real codes,
  `2445=قيد التطبيق`, `2443=ملغى` (plus an "all" option), confirms status
  is binary at the data-model level even though only `ملغى` ever renders.
- **Court/chamber** (`المحكمة`), from `AdvancedRulingSearch.aspx`'s
  `drpcourt`, **12 real values** with ids: `إستئناف مدني`, `ابتدائي مدني`,
  `استئناف جزائي`, `المجلس الدستوري`, `المجلس العدلي`, `تمييز جزائي -
  مطبوعات`, `تمييز جزائي`, `تمييز مدني`, `شورى`, `مجالس العمل التحكيمية`,
  `محكمة الاستئناف الناظرة بجرائم المطبوعات`, `محكمة الجنايات`.
- **Bibliography series** (`ReferencesPage.aspx`'s `drp_type`): 14 named
  periodicals/collections (`المجلة القضائية`, `مجلة الشرق الادنى`, `مجموعة
  اجتهادات حاتم`, `قرارات محكمة حل الخلافات ومجلس شورى الدولة`, …), the
  intended values for `article.series`, mostly unpopulated in practice
  (§6).
- **Opinion source/sub-kind** (`OpinionPage.aspx`'s `drp_Source`): 9 values
  (`رأي استشاري`, `قرار توحيد اجتهاد`, `قرار رقابة حسابات`, `قرار رقابة
  مسبقة`, `قرار رقابة موظفين`, …), a facet the detail page doesn't surface
  (§4).
- `PeriodsListPage.aspx` is about parliamentary sessions
  (اللجنة الادارية, المجلس التمثيلي الاول/الثاني, …), belongs to the
  separate `parliament/` static subsite the README says isn't in scope, not
  to any of the 9 doc types here.

No genuine subject-matter taxonomy (e.g. "labour law" / "civil law" topic
tree) was found anywhere in the archive, the only trees are the kind/court/
series/source vocabularies above, plus the per-law article TOC. If a subject
tree exists on the live site it isn't exposed in any archived page.

---

## 10. Languages

Zero archived URLs use `language=fr` or `language=en` (`agreement`,
`article`, `opinion` don't even carry a `language` query param, checked
against the full `doc` table). The UI and all metadata are exclusively
Arabic. Embedded French appears only as short quoted terms/citations/proper
nouns inside otherwise-Arabic body text, seen in `ruling` (~1/6 of a 40-doc
sample had ≥1 Latin word, mostly citations like "Regles de procedure"),
`agreement` (~31/40, treaty boilerplate/place names), `research` (~all,
academic citations), rarely `opinion`/`article`. No full French- or
English-language page was found. Digits are **always Western Arabic
numerals (0-9)** in every field and body sampled, no Arabic-Indic digit
(٠-٩) occurred anywhere across ~250 body-text samples.

---

## 11. Encoding/markup oddities for the parser to defend against

1. **Blank/placeholder records slip past `present=1`.** The archive's own
   `is_blank()` looks for the literal bytes `المحتوى غير متوفر` or
   `01/01/0001`. At least three more blank-sentinel shapes were found that
   it does *not* catch, and which are therefore stored as ordinary present
   documents with empty content fields:
   - `opinion`: date rendered as `1/1/0001` (no leading zeros) instead of
     `01/01/0001`, 6 confirmed via title pattern `رقم0/`.
   - `ruling`: year `0` with date `01/01/1900` (not `0001`), **291 of
     47,683 rulings** (title ends `/0`), e.g. "شورى حكم رقم : 25 /0" with
     empty `RulingText`.
   - `agreement`: title literally `عنوان الاتفاقية غير متوفر` ("title
     unavailable") with no `المحتوى` phrase at all, **208 of 2,015
     agreements (~10%)**, empty `AgreementText`/number/date.
   Recommendation: the parser should apply its own per-type "is this really
   empty" check (empty body field, or year/number == 0, or title containing
   `غير متوفر`) rather than trusting `present=1` alone.
2. **Reused control ids across types**: `MainContent_RulingText` is the
   body element for `ruling`, `article`, *and* `research`; `lblNumber` and
   `lblcourtName` hold an author name on `article` pages. Always dispatch on
   `doc.type` first.
2b. **Two date formats coexist**: DD/MM/YYYY everywhere except `opinion`'s
   `lbl_opinion_date`, which is M/D/YYYY.
3. **Word-HTML paste junk**, concentrated in `research` (inline
   `style="font-family:...Times New Roman..."`, `<p align="center"
   dir="RTL">`, stray `<div style="direction: ltr;">` for quoted French)
   and present at lower rates in `law` preambles and `agreement` text.
   `<br>` is the dominant paragraph separator in `ruling`/`agreement`/`law`;
   `<p>` shows up mostly where Word content was pasted in verbatim.
4. **Tables**: rare but real: one sampled `law` (a Constitutional-Council
   election ruling filed under the `law` type) carries a 150-row `<table>`
   of vote tallies with heavy per-cell inline styling, ballooning the page
   to ~940 KB; a generic "strip tags, keep text" pass will mangle it unless
   tables are special-cased.
5. **`&nbsp;`-glued label text**: `subdetails`/`lblTitle`/etc. join labels
   and values with `&nbsp;` rather than real whitespace (e.g. `نوع
   النص:&nbsp;قرار&nbsp;|&nbsp;رقم 180…`); must `html.unescape` before
   splitting on `|`.
6. **Legacy hostname** `legiliban.ul.edu.lb` appears (8% of `research`
   docs) as an alternate/older domain for the exact same `Law.aspx?lawId=`
   path, normalise to `legallaw.ul.edu.lb` when resolving cross-links.
7. **Pretty-URL variant** `Law/{id}/{url-encoded-title}` (seen once, inside
   a `cancellaw` link) resolves to the same document as
   `Law.aspx?lawId={id}`, treat both as the same target when extracting
   the numeric id.
8. No scanned-image-of-text pages, no PDF-in-HTML, no images carrying body
   text were found in any sample, all body content is real selectable
   Arabic/French text.

---

## 12. Parser plan: proposed normalised schema

Given §0, the schema below separates "parseable today from the archive" from
"needs the nine un-archived endpoint families listed in §1", flagged `*`.

**`legislation`** (from `law`)
`law_id` (=doc_id) PK, `title`, `kind` (FK → `doc_kind`, parsed out of
`subdetails`), `number`, `issue_date`, `in_force` (bool, from presence of
`ملغى` span), `preamble_text` (`divNotes`), `preamble_memo_text`
(`divNotes2`), `oj_number`, `oj_publish_date`, `oj_page_range`,
`source_url`.

**`doc_kind`** lookup, seed from the 49-value `drp_clasification`
vocabulary (§9); `id`, `label_ar`.

**`law_article`*** (needs `LawArticles.aspx` crawl)
`law_article_id` (=`LawArticleID`), `law_id` FK, `article_number` (parsed
from `مادة N` label), `section_id` (FK to `law_section`, nullable),
`order_in_law`, `body_text`, **empty/absent until that endpoint is
crawled**; today only `(law_article_id, law_id, display_label)` triples can
be harvested, both from each law's own TOC and from citations scattered
across `ruling`/`opinion`/`research` bodies.

**`law_section`*** (باب/فصل/قسم chapter nodes)
`law_tree_section_id` (=`LawTreeSectionID`), `law_id` FK, `label`
(includes chapter name when present, else a bare article-range label),
`parent_section_id` (nullable, tree nesting beyond one flat level wasn't
observed but the id space allows it), `order_in_law`.

**`ruling`** (from `ruling`)
`ruling_id` PK, `court` (FK → `court`), `number`, `year`, `decision_date`,
`title`, `body_text`, `presiding_judge`, `panel_members` (array, split on
`/` or `-`), `case_file_url`.

**`court`** lookup, seed from the 12-value `drpcourt` vocabulary (§9).

**`gazette_issue`** (from `journal`, reconstructed per §3)
`journal_id` (=doc_id) PK, `issue_number` (joined from a member law's
`oj_number`), `issue_date` (joined `oj_publish_date`), `contents` → 1:N to
`legislation` via the `published_in` relation below.

**`opinion`** (from `opinion`)
`opinion_id` PK, `number`, `year`, `opinion_date` (careful: M/D/YYYY),
`requester`, `foundation_number`, `foundation_year`, `subject_title`,
`body_text`.

**`agreement`** (from `agreement`)
`agreement_id` PK, `title`, `agreement_type` (`ثنائية`/`دولية`),
`ratifying_instrument_text`, `signing_date`, `body_text`,
`ratifying_law_id` (FK → `legislation`, from `law_Link`).

**`doctrine_article`** (from `article`)
`article_id` PK, `title`, `authors` (array, split on `/`), `abstract_text`,
`series` (nullable FK → `series` lookup, sparse), `page_range`
(nullable).

**`series`** lookup, seed from the 14-value `drp_type` vocabulary (§9).

**`research`** (from `research`)
`research_id` PK, `title`, `author`, `publish_date`, `body_text`.

**`gazette_reference`**, this is really a field group on `legislation`
(`oj_number`/`oj_publish_date`/`oj_page_range`), not a separate table, since
every law already carries it inline; `gazette_issue` above is the
issue-level rollup.

### Relations table

| relation | from → to | source today | status |
|---|---|---|---|
| `repeals` | `legislation` → `legislation` | `cancellaw_*` fields | done, inline |
| `repealed_by` | inverse of `repeals` | derive by inverting `repeals` | done, computed |
| `amended_articles` | `legislation` → `law_article` | `AmendedArticles.aspx` | * needs crawl |
| `cites_article` | `ruling`/`opinion`/`research`/`doctrine_article` → `law_article` | `RelatedLaws_rptLaws_*` repeaters + inline `LawArticles.aspx?LawArticleID=` links in body text | partial, done wherever the citing page embeds the repeater/link; not a complete citation graph |
| `related_rulings` | `legislation` → `ruling` | `LawRelatedRulings.aspx` | * needs crawl |
| `related_opinions` | `legislation` → `opinion` | `LawRelatedOpinions.aspx` | * needs crawl |
| `related_doctrine` | `legislation` → `doctrine_article` | `LawRelatedReference.aspx` | * needs crawl |
| `related_case_refs` | `legislation` → (bibliographic case refs) | `LawRelatedRulingsReference.aspx` | * needs crawl |
| `related_research` | `legislation` → `research` | `LawRelatedResearches.aspx` | * needs crawl |
| `has_attachments` | `legislation` → attachment docs | `LawRelatedAttachments.aspx` | * needs crawl |
| `ratified_by` | `agreement` → `legislation` | `law_Link` | done, inline |
| `published_in` | `legislation` → `gazette_issue` | reconstructed join (§3), not a stored link | done, computed |

### Recommended parser build order

1. Ship the five self-contained types first (`ruling`, `opinion`,
   `agreement`, `article`, `research`), full text is on the archived page,
   only cleanup (html.unescape, strip Word junk, normalise `<br>`/`<p>`) is
   needed.
2. Ship `legislation` metadata + preamble + `repeals`/`repealed_by`, fully
   available.
3. Reconstruct `gazette_issue` by the join in §3, no crawl needed.
4. Apply the extra blank-record filters from §11.1 before loading anything
   (title contains `غير متوفر`; year/number == 0; date ∈
   {`01/01/0001`,`1/1/0001`,`01/01/1900`} combined with an empty body field).
5. Flag `law_article` / the six `LawRelated*.aspx` families / gap as a
   **separate, explicit scope decision**: either (a) ship legislation
   without article-level full text and without the six relation graphs, and
   surface only the TOC (article numbers + labels) and the panel-exists
   booleans, or (b) extend `ulscrape/targets.py` with new `Target`s for
   `LawArticles.aspx` (paired `LawArticleID`+`LawID`, harvest ids from every
   law's own TOC, no id-space sweep needed, they're all enumerable from
   existing `law` rows) and the six `LawRelated*.aspx&lawId=N` pages (also
   fully enumerable from existing `law` ids, one GET per law that shows a
   given panel). This is a scraper-code decision, out of scope for this
   recon, but the counts above (e.g. ~120 laws × ~6 panels, and up to ~40
   articles/law for the sampled large statutes) size the extra crawl.
