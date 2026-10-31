package site

import (
	"context"
	"strings"
)

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

var pinArStop = map[string]bool{"قانون": true, "القانون": true, "لبنان": true, "في": true}

var glossPins = map[string]int{}

func init() {
	for k, id := range glossPinTerms {
		glossPins[pinKey(k)] = id
	}
}

func pinKey(q string) string {
	var out []string
	if hasArabic(q) {
		for _, w := range strings.Fields(arNormGo(q)) {
			if pinArStop[w] {
				continue
			}
			for len([]rune(w)) > 4 && strings.HasPrefix(w, "ال") {
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

func pinnedLaw(q string) int {
	if k := pinKey(q); k != "" {
		return glossPins[k]
	}
	return 0
}

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
