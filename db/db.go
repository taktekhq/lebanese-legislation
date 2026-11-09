// Package db carries the corpus schema, so every tool builds the same database.
package db

import _ "embed"

// Schema creates the functions, tables and views; Indexes is applied after loading.
var (
	//go:embed schema.sql
	Schema string
	//go:embed indexes.sql
	Indexes string
	// OfficialSchema creates the table of texts published from official scans.
	//go:embed official.sql
	OfficialSchema string
	// Names gives the Centre's kinds and courts English and French names.
	//go:embed names.tsv
	Names string
	// Codes is the curated list of principal codes.
	//go:embed codes.tsv
	Codes string
	// AmendmentSchema creates the amendment and version tables and views.
	//go:embed amendments.sql
	AmendmentSchema string
	// GraphSchema creates the graph tables and views.
	//go:embed graph.sql
	GraphSchema string
	// Amendments is a person's decisions on amendments (apply, reject, repeal).
	//go:embed amendments.tsv
	Amendments string
	// Amounts lists texts that change every sum of money in another text
	// without naming its articles.
	//go:embed amounts.tsv
	Amounts string
	// DateCorrections fixes dates the Centre printed wrong: a text signed
	// after its own Gazette publication, a year typo, a placeholder date.
	// One row per field: law id, field, printed, corrected (empty: not
	// known), method, reason. The printed value is kept in date_correction.
	//go:embed date_corrections.tsv
	DateCorrections string
	// TitlesEn are unofficial English titles for the subject line of a text,
	// written by a model: law id, title, model. Shown as unofficial.
	//go:embed titles_en.tsv
	TitlesEn string
	// CentreErrors are errors in the Centre's own text of an article,
	// shown next to it and never corrected in it.
	//go:embed centre_errors.tsv
	CentreErrors string
	// CouncilRulings are the Constitutional Council's decisions on a text:
	// the text or articles it annulled, with the decision and its source,
	// shown next to them. The wording stays as published.
	//go:embed council_rulings.tsv
	CouncilRulings string
	// ArticleCorrections are an article's wording where the Centre's is
	// wrong, read by a person from the printed edition, or quoted from the
	// Centre's article page where an amending law's copy is wrong: minor
	// versions.
	//go:embed article_corrections.tsv
	ArticleCorrections string
	// Aliases are names a text is cited by that are not its title, checked
	// by a person: name, law id, note.
	//go:embed aliases.tsv
	Aliases string
	// Judgements are a model's checked answers about amendments the
	// patterns couldn't settle (judge).
	//go:embed model/judgements.jsonl
	Judgements string
	// Prod adds the read-optimized tables of the -db-prod image (law_card,
	// article_card, possibly_affected as a table). No new data.
	//go:embed prod.sql
	Prod string
)
