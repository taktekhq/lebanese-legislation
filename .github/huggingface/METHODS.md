# Provenance and limitations

The source is the Centre for Research and Studies in Legal Informatics, Lebanese University. Source pages and fetch dates remain in the data. The Centre's published legal text is retained; structure, links and machine readings are separate contributions. This is not an official publication or an endorsed service.

## Machine readings and corrections

Texts the Centre has not published yet are represented by readings
from scans of the Official Gazette's own pages: two machine readings,
compared word by word. They are in `legislation` like any other text, with
ids from 2,000,000,000, and listed in `legislation_scan`. Where the readings
disagree, the article text keeps both, marked U+E000 reading one, U+E001
reading two, U+E002. These copies are unofficial: check them against the
original Gazette PDF, which `legislation_scan.source_url` and the text's page on
the site link on SaderLex. When the Centre publishes a text, its version
replaces the scanned one.

The full judgment behind a ruling page is a scanned PDF with no text layer
(older ones are handwritten). The scans are on the site and in the related [Lebanese Court Scans dataset](https://huggingface.co/datasets/taktekhq/lebanese-court-scans), not in these Parquet tables.
`ruling_file` lists the ones we hold, and `ruling_page` holds the text Gemini
read from them, one row per page. It is a machine reading: it may contain
errors, nothing in it is corrected or rewritten, and the scan is the
reference. The reading is still under way, so these two tables grow.

Some dates the Centre prints are wrong: a text signed after its own Gazette
publication, a year typo (a decree of 31/12/2025 printed as 31/12/2026), a
placeholder 01/01 for a Gazette whose day is not known. `legislation` holds
the corrected date; `date_correction` keeps the date as the Centre prints it,
the correction, and the evidence (the text's own signing line, the Gazette
issue that published it, the texts numbered around it, or two Gemini readers
agreeing on a date found in that evidence). Where nothing settles it, the
signing date is left unknown rather than shown wrong.


English titles in `legislation` and `legislation_title_en` are generated from Arabic titles, not translations of the full text. Arabic titles remain the reference. Graph links, amendment versions, current article states and possible repeal effects are derived aids, not legal determinations. A `possibly_affected` row is a lead to investigate, not a finding. See [SCHEMA.md](SCHEMA.md) for provenance fields and pending states.

## Reuse and citation

Taktek does not own the source legal documents. We add no rights or restrictions of our own. The Hub licence is `other`. Source attribution does not itself grant third-party rights. Credit:

> Source: Centre for Research and Studies in Legal Informatics, Lebanese University.


```bibtex
@dataset{lebanese_legislation,
  title        = {Lebanese Legislation: the relational database of Lebanese law},
  author       = {{Taktek, LLC}},
  year         = {2026},
  url          = {https://huggingface.co/datasets/taktekhq/lebanese-legislation},
  note         = {Texts: Centre for Research and Studies in Legal Informatics, Lebanese University}
}
```

To cite one law or article, use its address on
[lebaneselegislation.com](https://lebaneselegislation.com): `/law/<id>/` and
`/law/<id>/article/<n>/` do not change.

