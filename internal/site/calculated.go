package site

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
)

// Search helpers for the JSON API and the MCP server: an article a query
// cites comes first, and so do the Civil Procedure articles whose calculated
// current amount contains the query (a search for 5,000,000,000 finds the
// article that says 100,000,000 in its published wording). The calculated
// wording itself comes from internal/calculatedcurrent (amounts.go); nothing
// here does arithmetic or changes a text.

// calculatedLaw is the only law with calculated current amounts: the Code of
// Civil Procedure, whose sums decree 330819 multiplied by 50.
const calculatedLaw = 244565

// asciiDigits writes Arabic-Indic and Persian digits as 0-9, as ar_digits does in SQL.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		}
		return r
	}, s)
}

// compactAmount drops thousands separators and spaces, so 5,000,000,000 and 5000000000 match.
func compactAmount(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ',' || r == '٬' || r == ' ' || r == ' ' {
			return -1
		}
		return r
	}, s)
}

// calculatedMatches finds Civil Procedure articles whose calculated wording
// contains q, for a query with a digit in it (an amount). The snippet stays
// the published text; the hit says the match came from the calculated
// wording. The list of such articles is kept for ten minutes.
func (s *Server) calculatedMatches(ctx context.Context, q string) []apiArticleHit {
	needle := compactAmount(strings.ToLower(asciiDigits(strings.TrimSpace(q))))
	if len([]rune(needle)) < 3 || !strings.ContainsAny(needle, "0123456789") {
		return nil
	}
	v, err := s.memo.get("calculated-articles", 10*time.Minute, func() (any, error) {
		law, err := s.loadLaw(ctx, calculatedLaw, true)
		if err != nil {
			return nil, err
		}
		records := law.calculatedCollection()
		var list []calculatedArticle
		seen := map[string]bool{}
		for _, a := range law.List {
			if r := records[a.Number]; r != nil && !seen[a.Number] {
				seen[a.Number] = true
				list = append(list, calculatedArticle{a.Number, a.Shown(), r})
			}
		}
		return list, nil
	})
	if err != nil {
		return nil
	}
	var out []apiArticleHit
	var summary *apiLawSummary
	for _, a := range v.([]calculatedArticle) {
		if !strings.Contains(compactAmount(strings.ToLower(asciiDigits(a.record.TextAr))), needle) {
			continue
		}
		if summary == nil {
			sum, ok := s.lawSummaryByID(ctx, calculatedLaw)
			if !ok {
				return nil
			}
			summary = &sum
		}
		out = append(out, apiArticleHit{Law: *summary, Number: a.number, Snippet: shortSnippet(a.shown),
			URL: Base + artPath(calculatedLaw, a.number), CalculatedCurrent: a.record, MatchedCalculated: true})
	}
	return out
}

// calculatedArticle is one article with a calculated current amount.
type calculatedArticle struct {
	number, shown string
	record        *calculatedcurrent.Record
}

// lawSummaryByID is one law's summary as the API lists it.
func (s *Server) lawSummaryByID(ctx context.Context, id int) (apiLawSummary, bool) {
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id)
	if err != nil {
		return apiLawSummary{}, false
	}
	laws, err := scanLawRows(rows)
	if err != nil || len(laws) != 1 {
		return apiLawSummary{}, false
	}
	return s.lawSum(ctx, laws[0], s.source(ctx, "law", id)), true
}

// shortSnippet is the first 240 characters of a text.
func shortSnippet(text string) string {
	r := []rune(text)
	if len(r) > 240 {
		return string(append(r[:240], '…'))
	}
	return text
}

var citedPath = regexp.MustCompile(`/(\d+)/article/([^/]+)/$`)

// citedArticle resolves a query that cites an article, such as "المادة 86 من
// قانون اصول المحاكمات المدنية", the way the website's search does, so the
// API and MCP put that article first.
func (s *Server) citedArticle(ctx context.Context, q string) (apiArticleHit, bool) {
	m := citedPath.FindStringSubmatch(s.articleRef(ctx, q))
	if m == nil {
		return apiArticleHit{}, false
	}
	id, _ := strconv.Atoi(m[1])
	var text string
	if s.db.QueryRow(ctx, `SELECT coalesce(text_ar, '') FROM legislation_article WHERE law_id = $1 AND number = $2 ORDER BY position LIMIT 1`, id, m[2]).Scan(&text) != nil {
		return apiArticleHit{}, false
	}
	law, ok := s.lawSummaryByID(ctx, id)
	if !ok {
		return apiArticleHit{}, false
	}
	return apiArticleHit{Law: law, Number: m[2], Snippet: shortSnippet(text), URL: Base + artPath(id, m[2]), Cited: true}, true
}
