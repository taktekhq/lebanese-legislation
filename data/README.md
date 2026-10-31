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
