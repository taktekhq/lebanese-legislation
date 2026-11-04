# Tables and loading


One Parquet file per table, in `data/`. Columns and how each was built are
described in full at
[lebaneselegislation.com/data/](https://lebaneselegislation.com/data/).

| Table | What it holds |
|---|---|
| `legislation` | One row per law, decree, decision and other text: kind, number, dates, title, status (in force or repealed), Official Gazette details. `title_en` is an unofficial English title (NULL when there is none): a model (Claude Sonnet) wrote it from the Arabic title alone, to help people and search engines find a text. The Arabic title and text are the official version and the more accurate one; the English is never a translation of the text. |
| `legislation_title_en` | The same unofficial English titles on their own (`law_id`, `title_en`, `model`), for joins. A reading aid for search and browsing, not an official title: the Arabic title in `legislation` is the reference. |
| `legislation_article` | One row per article, in Arabic (and French where published), with the Centre's notes. |
| `legislation_section` | Books, parts, chapters and sections of each text. |
| `legislation_kind` | The kinds of text, with English and French names. |
| `code` | The principal codes, with stable short names. |
| `amendment` | Changes one text made to another, read by the build's patterns. |
| `article_version` | Versions of amended articles, oldest first. |
| `legislation_version` | Each text's versions over time: version 1 as issued, then one row per date that applied changes took effect, with the amending texts (`amended_by`) and how many changes (`changes`). |
| `article_in_force` | Every article's state today, one row per article (join on `law_id` and `position`, since a number can appear twice in one text): `state` (in_force or repealed: by an amendment we read, or by the Centre's own note "مادة ملغاة"), the wording in force when it differs from the Centre's, its `version` and `text_source`, and `pending` when a later change has no full wording yet. |
| `possibly_affected` | Older texts still marked in force that a general repeal clause ("every provision contrary to this law is repealed") may reach: a text with such a clause (`repealing_law_id`, `repealing_article`) that also names an older in-force text (`law_id`, and `article_number` when it names one). A lead to check, not a finding. |
| `centre_error` | Errors in the Centre's own text of an article (one article repeating another, a stray letter, a typo in a number), with a note in English and Arabic. The Centre's text is kept as published; where a person supplied the correct wording from the printed edition, it is in `article_version` as a minor version with `text_source` editorial. |
| `graph_edge` | Every link between two items: amends, repeals, cites, applies, publishes, and the Centre's own notes. See 'The graph' below. |
| `graph_node` | Every item of the graph: texts, articles, rulings, opinions, research, gazette issues. |
| `ruling` | Court rulings: court, number, year, date, panel, text. |
| `ruling_file` | The scanned full judgment behind a ruling page (a PDF on the site, not in this dataset): its size, SHA-256, when it was fetched, and its page count once read. |
| `ruling_page` | The text of those scans, one row per page, read by machine (Gemini). It may contain errors; the scan is the reference. `low_confidence` marks pages that are hard to read (mostly handwriting). |
| `court` | Courts and chambers. |
| `opinion` | Advisory opinions. |
| `agreement` | International agreements and their ratifying texts. |
| `doctrine_article` | Doctrine: legal articles, with abstracts. |
| `research` | Research papers. |
| `gazette_issue` | Official Gazette issues. |
| `gazette_entry` | Which texts each Gazette issue published. |
| `reference` | Links the Centre's pages give: a ruling's cited articles, a repealed text's repealing text. |
| `source_page` | The raw page behind each row on the Centre's site, with its address and fetch dates. |
| `legislation_scan` | Texts read from scans of the Official Gazette's pages, for what the Centre has not published yet: where the scans came from, the original PDF on SaderLex (`source_url`), pages, and how far the two readings agree. |
| `legislation_alias` | The old id of a scanned text the Centre has since published, and the Centre's id that replaced it. |
| `date_correction` | Dates the Centre printed wrong: the printed date, the corrected one (empty when not known), how it was settled and the evidence. |

## Graph fields


`graph_edge` holds every link we know between two items, and `graph_node`
every item. Items have keys: `law/<id>`, `law/<id>/art/<number>`,
`ruling/<id>`, `opinion/<id>`, `research/<id>`, `gazette/<id>`.

- `relation`: `replace`, `amend`, `repeal`, `insert`, `suspend`, `extend`,
  `general_repeal`, `cites`, `applies`, `derogates`, `interprets`, `publishes`,
  `note_amended_by`, `note_repealed_by`, `note_added_by`.
- `source`: `model` (machine-derived links with checked supporting quotations), `centre` (links on the Centre's own
  pages), `parser` (rule-based patterns).
- `status`: `linked`, `no_article` (the text is in the data, the article
  number isn't), `unresolved` (the cited text isn't in the data yet;
  `to_cite` says what was cited), `general` (a general repeal naming no text).

## Load examples


```python
from datasets import load_dataset
articles = load_dataset("taktekhq/lebanese-legislation", "legislation_article", split="train")
```

```python
import pandas as pd
edges = pd.read_parquet("hf://datasets/taktekhq/lebanese-legislation/data/graph_edge.parquet")
```

```sql
-- DuckDB
SELECT * FROM 'hf://datasets/taktekhq/lebanese-legislation/data/legislation.parquet'
WHERE status = 'in_force' LIMIT 10;
```

