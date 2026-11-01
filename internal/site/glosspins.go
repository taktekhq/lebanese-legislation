package site

import (
	"context"
	"strings"
)

// Some questions name one text, not a topic: "cybercrime" is Law 81 of 2018
// (electronic transactions and personal data), which holds the Lebanese
// offences against information systems, while a title search for the word
// finds only decrees about the cybercrime bureau. These terms put that text
// first in a search, and the other results follow it. The list is small and
// kept by hand; it points at a text, it never writes wording shown as law.
//
// The hosted MCP server keeps a copy of these terms, so it ranks the same
// text first.

// lawEtransactions is Law 81 of 10 Oct 2018, المعاملات الإلكترونية والبيانات ذات الطابع الشخصي.
const lawEtransactions = 278573

var glossPinTerms = map[string]int{
	"cybercrime": lawEtransactions, "cybercrimes": lawEtransactions,
	"cyber crime": lawEtransactions, "cyber crimes": lawEtransactions,
	"cyber-crime": lawEtransactions, "cyber-crimes": lawEtransactions,
	"information crime": lawEtransactions, "information crimes": lawEtransactions,
	"computer crime": lawEtransactions, "computer crimes": lawEtransactions,
	"الجرائم المعلوماتية": lawEtransactions, "الجرائم الالكترونية": lawEtransactions,
	"الجرائم الإلكترونية": lawEtransactions, "جرائم المعلوماتية": lawEtransactions,
	"جرائم معلوماتية": lawEtransactions, "جرائم الكترونية": lawEtransactions,
}

// pinArStop are Arabic words that never change which text is meant.
var pinArStop = map[string]bool{"قانون": true, "القانون": true, "لبنان": true, "في": true}

// glossPins maps pinKey of each term to the text's id.
var glossPins = map[string]int{}

func init() {
	for k, id := range glossPinTerms {
		glossPins[pinKey(k)] = id
	}
}

// pinKey folds a question to the form the pinned terms are kept in: Latin
// lower case, accents and hyphens dropped, the little words of glossStop
// left out; Arabic with spelling folded, the article ال (as often as it begins) of each word and
// the words قانون and لبنان left out.
func pinKey(q string) string {
	var out []string
	if hasArabic(q) {
		for _, w := range strings.Fields(arNormGo(q)) {
			if pinArStop[w] {
				continue
			}
			for len([]rune(w)) > 4 && strings.HasPrefix(w, "ال") { // "الالكترونية" and "الكترونية" are one word
				w = string([]rune(w)[2:])
			}
			out = append(out, w)
		}
		return strings.Join(out, " ")
	}
	for _, t := range latinTokens(strings.ReplaceAll(q, "-", " ")) {
		if !glossStop[t] {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// pinnedLaw returns the id of the text a question names, or 0.
func pinnedLaw(q string) int {
	if k := pinKey(q); k != "" {
		return glossPins[k]
	}
	return 0
}

// pinnedRow loads the text q names, nil when q names none, the text is not in
// this database, or year asks for another year.
func (s *Server) pinnedRow(ctx context.Context, q string, year int) *LawRow {
	id := pinnedLaw(q)
	if id == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id)
	if err != nil {
		return nil
	}
	got, err := scanLawRows(rows)
	if err != nil || len(got) == 0 {
		return nil
	}
	if year != 0 && (got[0].IssuedOn == nil || got[0].IssuedOn.Year() != year) {
		return nil
	}
	return &got[0]
}

// withPin puts the pinned row first in laws (once), and says whether it was
// not there before, so the count of matches grows by one.
func withPin(laws []LawRow, pin *LawRow, limit int) ([]LawRow, bool) {
	if pin == nil {
		return laws, false
	}
	out := []LawRow{*pin}
	added := true
	for _, l := range laws {
		if l.ID == pin.ID {
			added = false
			continue
		}
		out = append(out, l)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, added
}
