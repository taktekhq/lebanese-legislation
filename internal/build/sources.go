package build

import (
	"fmt"
	"strconv"
	"strings"
)

// Where an article's wording comes from. Every writer of law text
// (legislation_article.text_ar and text_fr, article_version.text_ar) stores
// the source's kind and id in the same row as the text, and nothing else
// about it: no hash. The wording guard (internal/provenance) reads that
// source from the archive before export, snapshot and the Hugging Face
// upload, and blocks a text that is not a span of it. So a writer cannot
// vouch for its own text.
const (
	// SourceUL: a page of the Lebanese University's Centre, id
	// "lawtext/<law id>" (archive/lawtext/, through its index).
	SourceUL = "ul"
	// SourceSaderlex: a text read from Gazette scans, id "<SaderLex id>",
	// the lebgazette sader-bundle folder of that id.
	SourceSaderlex = "saderlex"
	// SourceAmendment: the wording an amendment published, id
	// "<amending law id>/<amending article number>": that article's own
	// UL or SaderLex source.
	SourceAmendment = "amendment"
	// SourceEdit: a person's wording, id the edit's (approved_edits.tsv,
	// or "article_corrections/<law id>/<article>" for
	// db/article_corrections.tsv). It passes only by its text's hash.
	SourceEdit = "edit"
)

// SourceKinds are the kinds the writers store, for the guard's tests.
var SourceKinds = []string{SourceUL, SourceSaderlex, SourceAmendment, SourceEdit}

func ulSource(lawID int) string { return "lawtext/" + strconv.Itoa(lawID) }

// LawSource is the source of the text a law's own article holds: its UL
// page, or the scan it was read from.
func LawSource(lawID int) (kind, id string) {
	if lawID >= ScanIDBase {
		return SourceSaderlex, strconv.Itoa(lawID - ScanIDBase)
	}
	return SourceUL, ulSource(lawID)
}

// AmendmentSource is the id of an amending article's published wording.
func AmendmentSource(byLaw int, byArticle string) string {
	return fmt.Sprintf("%d/%s", byLaw, byArticle)
}

// CorrectionSource is the edit id of a db/article_corrections.tsv row.
func CorrectionSource(law int, number string) string {
	return fmt.Sprintf("article_corrections/%d/%s", law, number)
}

// CorrectionText is a db/article_corrections.tsv row's wording as the build
// stores it: trimmed, and each "\n" written in the file a line break, so a
// wording of several paragraphs fits on one line.
func CorrectionText(field string) string {
	return strings.TrimSpace(strings.ReplaceAll(field, `\n`, "\n"))
}
