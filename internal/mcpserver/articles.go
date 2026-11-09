package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
)

// articleRow combines the Centre's legislation_article text
// plus the in-force fields of article_in_force (db/amendments.sql) and the
// calculated current amounts verified by internal/calculatedcurrent.
type articleRow struct {
	LawID       int
	Position    int
	Number      string
	Note        *string
	TextAr      string // the Centre's wording, scan readings written ⟦one | two⟧
	TextFr      *string
	InForceFrom *string
	SectionID   *int
	// The wording in force today when an amendment changed the Centre's
	// text (nil when version is 1.0 or the wording is the same), the
	// article's state, whether the latest change still lacks a full
	// wording, and since when.
	CurrentTextAr *string
	Version       string
	State         string
	Pending       bool
	ValidFrom     *string
	// The calculated current amounts, as the snapshot exported them (JSON).
	Calculated *string
}

// articleColsComputed and articleFromComputed select an articleRow's raw
// columns (alias a) in the computed layout. The two lateral reads are article_in_force's latest and worded versions,
// for this article only: the view itself reads every version first.
const articleColsComputed = `a.law_id, a.position, a.number, a.note, a.text_ar, a.text_fr, a.in_force_from, a.section_id,
	CASE WHEN st.state = 'repealed' OR v.state = 'repealed' THEN NULL ELSE coalesce(w.text_ar, a.text_ar) END,
	coalesce(w.major || '.' || w.minor, '1.0'), st.state,
	st.state = 'in_force' AND coalesce(v.state = 'in_force' AND v.text_ar IS NULL AND (w.major IS NULL OR w.major < v.major), false),
	v.valid_from,
	(al.status <> 'in_force'
	 OR EXISTS (SELECT 1 FROM amendment x WHERE x.law_id = a.law_id
	   AND x.article_number IS NULL AND x.action = 'repeal' AND x.part IS NULL AND x.status = 'applied')
	 OR EXISTS (SELECT 1 FROM council_ruling cr WHERE cr.law_id = a.law_id AND (cr.number = '' OR cr.number = a.number) AND cr.effect IN ('annulled', 'not_in_force')))`

const articleFromComputed = `legislation_article a
	JOIN legislation al ON al.id = a.law_id
	LEFT JOIN LATERAL (SELECT av.state, av.text_ar, av.major, av.valid_from FROM article_version av
	  WHERE av.law_id = a.law_id AND av.article_number = a.number ORDER BY av.major DESC, av.minor DESC LIMIT 1) v ON true
	LEFT JOIN LATERAL (SELECT aw.text_ar, aw.major, aw.minor FROM article_version aw
	  WHERE aw.law_id = a.law_id AND aw.article_number = a.number AND aw.text_ar IS NOT NULL ORDER BY aw.major DESC, aw.minor DESC LIMIT 1) w ON true
	CROSS JOIN LATERAL (SELECT CASE WHEN a.note ~ '^\s*مادة ملغا' THEN 'repealed' ELSE coalesce(v.state, 'in_force') END AS state) st`

// scanReadings writes a span the two machine readings of a Gazette scan
// disagree on (U+E000 one U+E001 two U+E002) as ⟦one | two⟧, which an
// assistant reading the MCP can see and explain.
var scanReadings = strings.NewReplacer("", "⟦", "", " | ", "", "⟧").Replace

func (s *Server) queryArticles(ctx context.Context, where string, args ...any) ([]articleRow, error) {
	L := s.sql(ctx)
	rows, err := s.db.Query(ctx, `SELECT `+L.articleCols+` FROM `+L.articleFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []articleRow
	for rows.Next() {
		var a articleRow
		var inForce, validFrom *time.Time
		var current *string
		var annulled bool
		if err := rows.Scan(&a.LawID, &a.Position, &a.Number, &a.Note, &a.TextAr, &a.TextFr, &inForce, &a.SectionID,
			&current, &a.Version, &a.State, &a.Pending, &validFrom, &annulled); err != nil {
			return nil, err
		}
		a.finish(current, inForce, validFrom, annulled)
		out = append(out, a)
	}
	return out, rows.Err()
}

// finish derives the response columns from the raw article fields.
func (a *articleRow) finish(current *string, inForce, validFrom *time.Time, annulled bool) {
	// Only carry the in-force wording when it differs from the Centre's.
	if a.Version == "1.0" || (current != nil && *current == a.TextAr) {
		current = nil
	}
	a.CurrentTextAr = current
	source := a.TextAr
	if current != nil {
		source = *current
	}
	from := validFrom
	if inForce != nil && (from == nil || inForce.After(*from)) {
		from = inForce
	}
	if r := calculatedcurrent.Calculate(calculatedcurrent.Input{LawID: a.LawID, Number: a.Number, Source: source, State: a.State, Pending: a.Pending, Annulled: annulled, ValidFrom: from}); r != nil {
		if b, err := json.Marshal(r); err == nil {
			v := string(b)
			a.Calculated = &v
		}
	}
	a.TextAr = scanReadings(a.TextAr)
	a.InForceFrom = dateStr(inForce)
	a.ValidFrom = dateStr(validFrom)
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// firstArticle is the article of law with that printed number at the
// lowest position (the Centre repeats a number within a text a handful of
// times).
func (s *Server) firstArticle(ctx context.Context, lawID int64, number string) (*articleRow, error) {
	rows, err := s.queryArticles(ctx, `a.law_id = $1 AND a.number = $2 ORDER BY a.position LIMIT 1`, lawID, number)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// articleTexts is the wording part of an article, the same rules for
// get_article and get_law_texts: text_ar is the wording in force today.
// When an amendment replaced the Centre's wording, the Centre's (and its
// French, which follows the old wording) move to centre_text_*, so an
// assistant quoting text_ar never quotes a superseded article.
func articleTexts(a *articleRow, lawRepealed bool) obj {
	repealedArticle := a.State == "repealed"
	amended := a.CurrentTextAr != nil && *a.CurrentTextAr != "" || repealedArticle
	var o obj
	if !repealedArticle {
		if a.CurrentTextAr != nil {
			o.set("text_ar", *a.CurrentTextAr)
		} else {
			o.set("text_ar", a.TextAr)
		}
	}
	if !amended {
		o.str("text_fr", a.TextFr)
	}
	o.set("version", a.Version)
	if amended {
		o.str("current_since", a.ValidFrom)
	}
	if a.Pending {
		o.set("pending_change", "a later change to this article has no full wording yet; check the Official Gazette")
	}
	if amended && a.TextAr != "" {
		o.set("centre_text_ar", a.TextAr)
	}
	if amended {
		o.str("centre_text_fr", a.TextFr)
	}
	o.str("in_force_from", a.InForceFrom)
	o.set("repealed", lawRepealed || repealedArticle)
	return o
}

var (
	isoDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	sha256Re = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// calculatedCurrent validates the exported record before it is returned,
// checking the exact current source, and whitelists its fields so a
// malformed export cannot introduce extra claims.
func calculatedCurrent(a *articleRow, lawStatus string, today string) obj {
	if a == nil || a.LawID != 244565 || lawStatus != "in_force" || a.State != "in_force" || a.Pending || a.Calculated == nil || *a.Calculated == "" {
		return nil
	}
	if today < "2024-09-19" {
		return nil
	}
	for _, d := range []*string{a.ValidFrom, a.InForceFrom} {
		if d != nil && *d != "" && (!isoDate.MatchString(*d) || *d > today) {
			return nil
		}
	}
	var c struct {
		Kind               *string  `json:"kind"`
		TextAr             *string  `json:"text_ar"`
		SourceTextAr       *string  `json:"source_text_ar"`
		SourceSHA256       *string  `json:"source_sha256"`
		IsPublishedWording *bool    `json:"is_published_wording"`
		Multiplier         *float64 `json:"multiplier"`
		Authority          *struct {
			LawID       *float64 `json:"law_id"`
			Article     *string  `json:"article"`
			URL         *string  `json:"url"`
			EffectiveOn *string  `json:"effective_on"`
		} `json:"authority"`
	}
	if err := json.Unmarshal([]byte(*a.Calculated), &c); err != nil {
		return nil
	}
	source := a.TextAr
	if a.CurrentTextAr != nil {
		source = *a.CurrentTextAr
	}
	au := c.Authority
	if c.Kind == nil || *c.Kind != "statutory_monetary_adjustment" || c.TextAr == nil || jsTrim(*c.TextAr) == "" ||
		c.SourceTextAr == nil || *c.SourceTextAr != source || c.SourceSHA256 == nil || !sha256Re.MatchString(*c.SourceSHA256) ||
		c.IsPublishedWording == nil || *c.IsPublishedWording || c.Multiplier == nil || *c.Multiplier != 50 ||
		au == nil || au.LawID == nil || *au.LawID != 330819 || au.Article == nil || *au.Article != "1" ||
		au.URL == nil || *au.URL != "https://lebaneselegislation.com/decree/330819/article/1/" ||
		au.EffectiveOn == nil || *au.EffectiveOn != "2024-09-19" {
		return nil
	}
	sum := sha256.Sum256([]byte(*c.SourceTextAr))
	if hex.EncodeToString(sum[:]) != *c.SourceSHA256 {
		return nil
	}
	return obj{{"kind", *c.Kind}, {"text_ar", *c.TextAr}, {"source_text_ar", *c.SourceTextAr}, {"source_sha256", *c.SourceSHA256},
		{"is_published_wording", false}, {"multiplier", 50},
		{"authority", obj{{"law_id", 330819}, {"article", "1"}, {"url", *au.URL}, {"effective_on", "2024-09-19"}}}}
}

type centreNote struct{ en, ar string }

// centreErrors: notes on errors in the Centre's own text of a law's
// articles (one article repeating another, a stray letter), by number.
func (s *Server) centreErrors(ctx context.Context, lawID int64, number *string) (map[string]centreNote, error) {
	q := `SELECT number, note_en, note_ar FROM centre_error WHERE law_id = $1`
	params := []any{lawID}
	if number != nil {
		q += ` AND number = $2`
		params = append(params, *number)
	}
	rows, err := s.db.Query(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]centreNote{}
	for rows.Next() {
		var n, en, ar string
		if err := rows.Scan(&n, &en, &ar); err != nil {
			return nil, err
		}
		out[n] = centreNote{en, ar}
	}
	return out, rows.Err()
}

func (n centreNote) obj() obj { return obj{{"en", n.en}, {"ar", n.ar}} }
