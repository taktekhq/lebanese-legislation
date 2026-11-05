# Lebanese Legislation: the database

All of Lebanese law as one relational PostgreSQL database, kept as text so
that git records every change: laws, decrees and decisions with their
chapters and articles (Arabic, and French where published), the Official
Gazette, court rulings, advisory opinions, agreements, doctrine, and the
references between them.

Built from the Lebanese University's Centre for Research and Studies in Legal
Informatics (legallaw.ul.edu.lb). Every row records the page it was read from
and the SHA-256 of that page as served (`source_page`). A free public
service; the website is lebaneselegislation.com.

## Load it

```sh
createdb lebleg
data/load.sh "dbname=lebleg"
```

Needs only `psql` (PostgreSQL 16 or later). About six minutes, most of it
building the search indexes.

The nightly build writes each day's export here. Every law text in it is checked
against its source page in [`archive/`](https://github.com/taktekhq/lebanese-legislation-archive).

Every change to this folder runs [the data workflow](../.github/workflows/data.yml):
it loads the database from these files, exports the Hugging Face Parquet
tables and checks the row counts against `stats.json`.

## What is here

| File | What |
|---|---|
| `schema.sql`, `indexes.sql` | the schema (tables, views, search functions) and its indexes |
| `TABLES` | load order, parents first |
| `tables/<table>/COLUMNS` | the columns each file holds, in order |
| `tables/<table>/<shard>.tsv` | rows in PostgreSQL `COPY` text format, one per line, sorted by key |
| `shards.json` | the key range per file (fixed, so files never reshuffle) |
| `stats.json` | rows per table |
| `article_calculated_current.jsonl` | current monetary amounts calculated under decree 13909/2024 ([details](../docs/calculated-current-release.md)) |
| `article_en.jsonl` | the unofficial English translation of the popular texts' articles, by machine (Gemini); loaded by `load.sh` (`article_en.sql`) only where the Arabic it was made from is unchanged. The Arabic is the official text |
| `gazette_annex.jsonl` | what we read ourselves from the Official Gazette's page images where the typed text lacks it: the violation tables annexed to Law 243/2012 (389 rows) and Articles 49 to 54 of the Social Security Law as first published in 1963. Machine transcription (two Gemini readings; where they differed, Gemini decided from the page), unofficial; made by `lebleg_annex.py` in [taktekhq/lebgazette-data](https://github.com/taktekhq/lebgazette-data), loaded by `load.sh` (`gazette_annex.sql`) where the text is in the database |
| `popular_texts.txt` | the texts whose English pages search engines may index (5+ views in 60 days); `build-db` reads it. The nightly that writes `tables/` builds without it, so `tables/noindex_page` holds no `english` rows: after loading, `build-db -only english` adds them (the static-site workflow does) |

Search vectors (`search_exact`, `search_stem`, `search_fr`) are generated
columns: they are not in the files, Postgres computes them on load.

## Reading the history

An unchanged row is an unchanged line, so `git log -p data/tables/legislation_article/`
shows exactly which articles changed and when.

## The tables

| Table | One row per |
|---|---|
| `legislation` | law, decree, decision… (the Centre's LawID is the key) |
| `legislation_section` | book, part, chapter, section, nested |
| `legislation_article` | article, consolidated; amendment notes; pictures where the text is a scan |
| `legislation_kind`, `court` | the Centre's vocabularies, with English and French names |
| `gazette_issue`, `gazette_entry` | Official Gazette issues and the texts each published |
| `code` | the principal codes, curated |
| `ruling`, `opinion`, `agreement`, `doctrine_article`, `research` | the other document types |
| `reference` | a document pointing at a text or article; `method` says whether the Centre's editors, our parser or a model made the link |
| `source_page` | the raw page behind every row |
