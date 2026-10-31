package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The public dump carries article_calculated_current: Civil Procedure (law
// 244565) amounts multiplied by 50 under decree 330819, as exported by the
// canonical builder. No arithmetic happens here.
const calculatedLaw = 244565

var calculatedAuthority = map[string]any{
	"law_id": float64(330819), "article": "1",
	"url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19",
}

var calculatedFrom = time.Date(2024, 9, 19, 0, 0, 0, 0, time.UTC)

// calculatedCurrent returns the stored calculated wording for an article when
// the shown text is exactly the in-force source it was calculated from, and
// nil otherwise, so the original text always stands on its own.
func (s *Server) calculatedCurrent(ctx context.Context, lawID int, number, shown string, repealed bool) json.RawMessage {
	if lawID != calculatedLaw || repealed || shown == "" {
		return nil
	}
	var present bool
	if err := s.db.QueryRow(ctx, `SELECT to_regclass('public.article_calculated_current') IS NOT NULL`).Scan(&present); err != nil || !present {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.source_sha256, c.calculated_current::text FROM article_calculated_current c
		JOIN article_in_force f ON (f.law_id,f.number,f.position)=(c.law_id,c.number,c.position)
		JOIN legislation l ON l.id=c.law_id
		WHERE c.law_id=$1 AND c.number=$2 AND f.text_ar=$3
		AND f.state='in_force' AND f.pending IS FALSE
		AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
		AND l.status NOT IN ('repealed','annulled')
		AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)`, lawID, number, shown)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var found []json.RawMessage
	for rows.Next() {
		var hash, raw string
		if rows.Scan(&hash, &raw) != nil {
			return nil
		}
		if validCalculated(hash, []byte(raw), shown, time.Now()) {
			found = append(found, json.RawMessage(raw))
		}
	}
	if rows.Err() != nil || len(found) != 1 {
		return nil
	}
	return found[0]
}

// validCalculated applies the same checks as the export: scope, authority,
// multiplier and the SHA-256 of the exact source text.
func validCalculated(rowHash string, raw []byte, source string, now time.Time) bool {
	var obj struct {
		Kind               string         `json:"kind"`
		IsPublishedWording *bool          `json:"is_published_wording"`
		Multiplier         *float64       `json:"multiplier"`
		Authority          map[string]any `json:"authority"`
		SourceTextAr       string         `json:"source_text_ar"`
		SourceSHA256       string         `json:"source_sha256"`
		TextAr             string         `json:"text_ar"`
	}
	if json.Unmarshal(raw, &obj) != nil || now.Before(calculatedFrom) {
		return false
	}
	sum := sha256.Sum256([]byte(source))
	digest := hex.EncodeToString(sum[:])
	if obj.Kind != "statutory_monetary_adjustment" || obj.IsPublishedWording == nil || *obj.IsPublishedWording ||
		obj.Multiplier == nil || *obj.Multiplier != 50 || len(obj.Authority) != len(calculatedAuthority) {
		return false
	}
	for k, v := range calculatedAuthority {
		if obj.Authority[k] != v {
			return false
		}
	}
	return obj.SourceTextAr == source && rowHash == digest && obj.SourceSHA256 == digest &&
		strings.TrimSpace(obj.TextAr) != "" && obj.TextAr != source
}

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
		if r == ',' || r == '٬' || r == ' ' || r == '\u00a0' {
			return -1
		}
		return r
	}, s)
}

// calculatedMatches finds Civil Procedure articles whose calculated wording
// contains q, as the Python service did, so a search for a current amount
// (5,000,000,000) finds its article. The snippet stays the original text; the
// hit says the match came from the calculated wording.
func (s *Server) calculatedMatches(ctx context.Context, q string) []apiArticleHit {
	needle := compactAmount(strings.ToLower(asciiDigits(strings.TrimSpace(q))))
	if len([]rune(needle)) < 3 {
		return nil
	}
	var present bool
	if err := s.db.QueryRow(ctx, `SELECT to_regclass('public.article_calculated_current') IS NOT NULL`).Scan(&present); err != nil || !present {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.number, c.source_sha256, c.calculated_current::text, f.text_ar
		FROM article_calculated_current c
		JOIN article_in_force f ON (f.law_id,f.number,f.position)=(c.law_id,c.number,c.position)
		JOIN legislation l ON l.id=c.law_id
		WHERE c.law_id=$1 AND f.state='in_force' AND f.pending IS FALSE
		AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
		AND l.status NOT IN ('repealed','annulled')
		AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)
		ORDER BY c.position`, calculatedLaw)
	if err != nil {
		return nil
	}
	type match struct{ number, text string }
	var found []match
	for rows.Next() {
		var number, hash, raw, source string
		if rows.Scan(&number, &hash, &raw, &source) != nil {
			rows.Close()
			return nil
		}
		var obj struct {
			TextAr string `json:"text_ar"`
		}
		if !validCalculated(hash, []byte(raw), source, time.Now()) || json.Unmarshal([]byte(raw), &obj) != nil {
			continue
		}
		if strings.Contains(compactAmount(strings.ToLower(asciiDigits(obj.TextAr))), needle) {
			found = append(found, match{number, source})
		}
	}
	rows.Close()
	if rows.Err() != nil || len(found) == 0 {
		return nil
	}
	law, ok := s.lawSummaryByID(ctx, calculatedLaw)
	if !ok {
		return nil
	}
	out := make([]apiArticleHit, 0, len(found))
	for _, m := range found {
		out = append(out, apiArticleHit{Law: law, Number: m.number, Snippet: shortSnippet(m.text),
			URL: Base + artPath(calculatedLaw, m.number), MatchedCalculated: true})
	}
	return out
}

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
