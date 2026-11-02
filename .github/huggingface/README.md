---
pretty_name: Lebanese Legislation
language:
- ar
- fr
license: other
license_name: public-texts-credit-the-centre
license_link: https://lebaneselegislation.com/data/
tags:
- legal
- law
- lebanon
- arabic
- legislation
- knowledge-graph
size_categories:
- 100K<n<1M
configs:
- config_name: legislation
  data_files: data/legislation.parquet
- config_name: legislation_title_en
  data_files: data/legislation_title_en.parquet
- config_name: legislation_version
  data_files: data/legislation_version.parquet
- config_name: possibly_affected
  data_files: data/possibly_affected.parquet
- config_name: legislation_article
  data_files: data/legislation_article.parquet
- config_name: legislation_section
  data_files: data/legislation_section.parquet
- config_name: legislation_kind
  data_files: data/legislation_kind.parquet
- config_name: code
  data_files: data/code.parquet
- config_name: amendment
  data_files: data/amendment.parquet
- config_name: article_version
  data_files: data/article_version.parquet
- config_name: article_in_force
  data_files: data/article_in_force.parquet
- config_name: article_calculated_current
  data_files: data/article_calculated_current.parquet
- config_name: centre_error
  data_files: data/centre_error.parquet
- config_name: graph_edge
  data_files: data/graph_edge.parquet
- config_name: graph_node
  data_files: data/graph_node.parquet
- config_name: ruling
  data_files: data/ruling.parquet
- config_name: ruling_file
  data_files: data/ruling_file.parquet
- config_name: ruling_page
  data_files: data/ruling_page.parquet
- config_name: court
  data_files: data/court.parquet
- config_name: opinion
  data_files: data/opinion.parquet
- config_name: agreement
  data_files: data/agreement.parquet
- config_name: doctrine_article
  data_files: data/doctrine_article.parquet
- config_name: research
  data_files: data/research.parquet
- config_name: gazette_issue
  data_files: data/gazette_issue.parquet
- config_name: gazette_entry
  data_files: data/gazette_entry.parquet
- config_name: reference
  data_files: data/reference.parquet
- config_name: source_page
  data_files: data/source_page.parquet
- config_name: legislation_scan
  data_files: data/legislation_scan.parquet
- config_name: legislation_alias
  data_files: data/legislation_alias.parquet
- config_name: date_correction
  data_files: data/date_correction.parquet
---

# Lebanese Legislation

Lebanese law in Parquet: over 100,000 laws, decrees and decisions, about 390,000 articles, 47,000 court rulings, 6,800 Official Gazette issues and 270,000 links between texts. Coverage runs from 1904 to 2026, in Arabic and French where published.

Use it to search texts, follow amendments or build legal research tools. This is the dataset behind [lebaneselegislation.com](https://lebaneselegislation.com), maintained by [Taktek](https://taktek.io). Browse both datasets in the [collection](https://huggingface.co/collections/taktekhq/lebanese-legislation).

## Download options and related projects

The PostgreSQL dump with saved search vectors is in [`releases/`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/tree/main/releases). Each release folder holds `corpus.dump` and a `manifest.json` with its SHA-256; `releases/latest.json` points to the newest. Dataset versions are separate from software and container versions.

For local services, use the [canonical code repository](https://github.com/taktekhq/lebanese-legislation) and its [Docker image links](https://github.com/taktekhq/lebanese-legislation#run-the-services-locally). Browse the [public library](https://lebaneselegislation.com/), inspect [court scans](https://huggingface.co/datasets/taktekhq/lebanese-court-scans), or try the [local handwriting demo](https://huggingface.co/spaces/taktekhq/lebanese-legal-handwriting). [Dactelo](https://dactelo.com/) is the related Arabic handwriting project. Code licensing does not replace this dataset’s source rights and attribution.

## Start here

```python
from datasets import load_dataset

articles = load_dataset(
    "taktekhq/lebanese-legislation", "legislation_article", split="train"
)
```

Each table has a Parquet file in `data/`. See [SCHEMA.md](SCHEMA.md) for tables, joins and more load examples. For PostgreSQL and pgvector, see the [database and services](https://github.com/taktekhq/lebanese-legislation).

## Calculated monetary amounts

`article_calculated_current.parquet` is an optional derived sidecar. It contains
`law_id` (integer), `number` (text), `position` (integer), `source_sha256` (text)
and `calculated_current` (JSON text). Join on law, article number and position,
then verify the source text and its SHA-256 against `article_in_force`.

The object contains the calculated Arabic article, its exact published input,
input hash, multiplier, and authority. `is_published_wording` is false. Use this
object for current monetary amounts when present and identify the result as
calculated under decree 13909/2024, article 1, effective 2024-09-19. It covers only
the 34 reviewed Civil Procedure articles (law 244565), including 56 monetary
occurrences. All original texts and versions retain their published wording.
Other laws, changed sources, repealed or annulled articles, and pending or future
wording receive no record. Older dataset snapshots may omit this file.

## What to check

Most source texts come from the [Centre for Research and Studies in Legal Informatics, Lebanese University](https://legallaw.ul.edu.lb/). Please credit the Centre when reusing them. This is an independent, unofficial dataset, without state or University endorsement. The licence is `other`; we add no rights or restrictions over source documents.

Source text, machine readings and editorial corrections have different provenance. English titles are machine-generated search aids. Judgment transcriptions and recent Gazette readings may contain errors. They are not reviewed ground truth. Check the page images and authoritative publication before relying on them. Derived amendment links and in-force states also need checking. See [METHODS.md](METHODS.md) for uncertainty markers, corrections, provenance and citation.

The structured dataset is rebuilt nightly. Individual tables can be incomplete. Judgment reading is still under way. Original court PDFs are available separately in [Lebanese Court Scans](https://huggingface.co/datasets/taktekhq/lebanese-court-scans).

You can also use the [JSON API](https://lebaneselegislation.com/developers/) or [MCP server](https://lebaneselegislation.com/ai/). This dataset is not legal advice. Send corrections to nizar@taktek.io.

## Vectors

[`vectors/vectors.parquet`](https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet) contains 1,547,937 semantic search chunks,
embedded with `gemini-embedding-001` in 768 dimensions. Columns: `id`,
`document`, `model`, `hash`, `values` (`FLOAT[768]`). The file is published openly
in the `taktekhq/lebanese-legislation` Hugging Face dataset.

Read it directly with DuckDB:

```sql
SELECT id, document, model, hash, values
FROM read_parquet('https://huggingface.co/datasets/taktekhq/lebanese-legislation/resolve/main/vectors/vectors.parquet')
LIMIT 5;
```

The hosted search uses Cloudflare Vectorize index `lebleg-chunks` instead of
storing vectors in PostgreSQL. The [vectors workflow](https://github.com/taktekhq/lebanese-legislation/blob/main/.github/workflows/vectors.yml)
runs `build`, then `publish` and `vectorize`. `cmd/export-vectors` exports the
chunks; `cmd/vectorize-sync` uploads only changed vectors on later runs.
