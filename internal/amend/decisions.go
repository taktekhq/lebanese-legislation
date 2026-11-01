package amend

import (
	"fmt"
	"strconv"
	"strings"
)

// A person's decisions on amendments (db/amendments.tsv), read by the build
// (the pattern pass's changes) and the graph (the model's links).

// The decisions a row may take.
const (
	DecideApply  = "apply"  // the change is right: apply it
	DecideReject = "reject" // misread: neither the pattern pass's change nor the model's link is shown
	DecideRepeal = "repeal" // the amending article repeals the article: apply it as a repeal
)

// Decision is one row: the amending text and article, the amended text and
// article, and what to do.
type Decision struct {
	ByLaw     int
	ByArticle string
	Law       int
	Article   string
	Decision  string
	Note      string
	Line      string // the row as written, for the build's notes
}

// Matches says whether the decision is about the link from byLaw's article
// byArt to law's article art.
func (d Decision) Matches(byLaw int, byArt string, law int, art string) bool {
	return d.ByLaw == byLaw && d.ByArticle == byArt && d.Law == law && d.Article == art
}

// ParseDecisions reads db/amendments.tsv. A row with a decision it doesn't
// know, or an id that isn't a number, is an error: a person's decision is
// never dropped in silence.
func ParseDecisions(s string) ([]Decision, error) {
	var out []Decision
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "by_law_id\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			return nil, fmt.Errorf("amendments.tsv line %d: expected at least 5 columns", i+1)
		}
		byLaw, err1 := strconv.Atoi(f[0])
		law, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("amendments.tsv line %d: a text id is not a number", i+1)
		}
		switch f[4] {
		case DecideApply, DecideReject, DecideRepeal:
		default:
			return nil, fmt.Errorf("amendments.tsv line %d: unknown decision %q", i+1, f[4])
		}
		d := Decision{ByLaw: byLaw, ByArticle: f[1], Law: law, Article: f[3], Decision: f[4], Line: line}
		if len(f) > 5 {
			d.Note = f[5]
		}
		out = append(out, d)
	}
	return out, nil
}
