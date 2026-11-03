package mcpserver

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/indexing"
)

// Row shapes and small helpers shared by the tools. Every query reads the
// corpus as the D1 snapshot flattened it (internal/snapshot): a law row
// carries its kind's English name, its code, its source page and English
// title, joined here at request time instead of once at export time.

// Base is the site every url and source link points into.
const Base = "https://lebaneselegislation.com"

// NotLegalAdvice is the line every tool result carries.
const NotLegalAdvice = "Not legal advice. Official texts from the Lebanese University's Centre for Research and Studies in Legal Informatics."

// querier is what the tools need from the database: *pgxpool.Pool, a
// *pgx.Conn or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// lawRow is the snapshot's legislation row.
type lawRow struct {
	ID             int
	Kind           string
	KindEn         *string
	Number         *string
	IssuedOn       *string
	Title          string
	Status         string
	GazetteNumber  *string
	GazetteDate    *string
	GazetteIssueID *int
	ArticleCount   int
	CodeSlug       *string
	CodeNameEn     *string
	CodeNameFr     *string
	SourceURL      *string
	TitleEn        *string
	Changed        *time.Time // page_state.changed_at: when the text's page last changed
}

// lawCols and lawFrom select a lawRow (alias l). The join to
// legislation_kind is an inner join, as in the snapshot: a text whose kind
// has no row there is not served.
const lawCols = `l.id, l.kind, k.name_en, l.number, to_char(l.issued_on, 'YYYY-MM-DD'), l.title, l.status,
	l.gazette_number, to_char(l.gazette_date, 'YYYY-MM-DD'),
	(SELECT min(ge.issue_id) FROM gazette_entry ge WHERE ge.law_id = l.id),
	(SELECT count(*) FROM legislation_article la WHERE la.law_id = l.id)::int,
	c.slug, c.name_en, c.name_fr, sp.url, nullif(te.title_en, ''), ps.changed_at`

const lawFrom = `legislation l ` + lawJoins

// lawJoins joins the rest of a lawRow onto legislation l.
const lawJoins = `JOIN legislation_kind k ON k.name_ar = l.kind
	LEFT JOIN code c ON c.law_id = l.id
	LEFT JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
	LEFT JOIN legislation_title_en te ON te.law_id = l.id
	LEFT JOIN page_state ps ON ps.kind = 'law' AND ps.id = l.id`

// lawDate is the snapshot's coalesce(nullif(gazette_date, ”), issued_on).
const lawDate = `coalesce(l.gazette_date, l.issued_on)`

func (l *lawRow) dest() []any {
	return []any{&l.ID, &l.Kind, &l.KindEn, &l.Number, &l.IssuedOn, &l.Title, &l.Status, &l.GazetteNumber, &l.GazetteDate,
		&l.GazetteIssueID, &l.ArticleCount, &l.CodeSlug, &l.CodeNameEn, &l.CodeNameFr, &l.SourceURL, &l.TitleEn, &l.Changed}
}

// scanLaws reads rows whose first columns are lawCols, then extra.
func scanLaws(rows pgx.Rows, extra func() []any) ([]lawRow, error) {
	defer rows.Close()
	var out []lawRow
	for rows.Next() {
		var l lawRow
		d := l.dest()
		if extra != nil {
			d = append(d, extra()...)
		}
		if err := rows.Scan(d...); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Server) loadLaw(ctx context.Context, id int64) (*lawRow, error) {
	if id <= 0 || id > math.MaxInt32 {
		return nil, nil // no law has it
	}
	var l lawRow
	err := s.db.QueryRow(ctx, `SELECT `+lawCols+` FROM `+lawFrom+` WHERE l.id = $1`, id).Scan(l.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// lawSummary is the short form of a law every tool returns.
func lawSummary(l *lawRow) obj {
	o := obj{{"id", l.ID}, {"kind", l.Kind}, {"title", l.Title}, {"status", l.Status}, {"article_count", l.ArticleCount}, {"url", lawURL(l.ID, l.Kind)}}
	o.nonEmpty("title_en", l.TitleEn)
	o.nonEmpty("kind_en", l.KindEn)
	o.nonEmpty("number", l.Number)
	o.nonEmpty("issued_on", l.IssuedOn)
	o.nonEmpty("code", l.CodeNameEn)
	o.nonEmpty("source_url", l.SourceURL)
	o.changed(l.Changed)
	return o
}

// changed sets updated_at, the day a page last changed here (page_state),
// when indexing.ChangedOn accepts the date: never the text's own date.
func (o *obj) changed(t *time.Time) {
	if t == nil {
		return
	}
	if d := indexing.ChangedOn(*t, time.Now()); d != "" {
		o.set("updated_at", d)
	}
}

// pageChanged is page_state.changed_at of page kind/id, nil when it has none.
func (s *Server) pageChanged(ctx context.Context, kind string, id int) (*time.Time, error) {
	var t *time.Time
	err := s.db.QueryRow(ctx, `SELECT changed_at FROM page_state WHERE kind = $1 AND id = $2`, kind, id).Scan(&t)
	if isNoRows(err) {
		return nil, nil
	}
	return t, err
}

// wordForKind is the word in a text's address on the site: /law/ for laws
// and the constitution, /decree/ for decrees, /text/ for every other kind.
// Without the kind it is /law/, which the site redirects to the right word.
func wordForKind(kind string) string {
	switch {
	case kind == "":
		return "law"
	case strings.HasPrefix(kind, "مرسوم"):
		return "decree"
	case strings.HasPrefix(kind, "قانون") || kind == "الدستور" || kind == "دستور":
		return "law"
	}
	return "text"
}

func lawURL(id int, kind string) string {
	return Base + "/" + wordForKind(kind) + "/" + strconv.Itoa(id) + "/"
}

// articleURL percent-encodes the article's number as encodeURIComponent does.
func articleURL(lawID int, number, kind string) string {
	return lawURL(lawID, kind) + "article/" + encodeURIComponent(jsTrim(number)) + "/"
}

// encodeURIComponent escapes everything but A-Z a-z 0-9 - _ . ! ~ * ' ( ).
func encodeURIComponent(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	for _, keep := range []string{"!", "'", "(", ")", "*"} {
		e = strings.ReplaceAll(e, url.QueryEscape(keep), keep)
	}
	return e
}

func withAdvice(result any) obj {
	return obj{{"result", result}, {"not_legal_advice", NotLegalAdvice}}
}

// clampInt is Math.max(lo, Math.min(hi, Math.trunc(v))) for a number, def otherwise.
func clampInt(v any, def, lo, hi int) int {
	f, ok := numberArg(v)
	if !ok {
		return def
	}
	f = truncf(f)
	if f < float64(lo) {
		return lo
	}
	if f > float64(hi) {
		return hi
	}
	return int(f)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
