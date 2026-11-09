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
// corpus through joins at request time: a law row
// carries its kind's English name, its code, its source page and English
// title.

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

// layout is how a query reads a lawRow (alias l) and an articleRow
// (alias a). lawCols and lawFrom select a lawRow; lawJoins joins the rest of
// it onto legislation l (and code c, which some queries filter on). The
// join to legislation_kind is an inner join, as in the snapshot: a text
// whose kind has no row there is not served. articleCols and articleFrom
// select an articleRow's raw columns.
type layout struct {
	name                     string
	lawCols, lawJoins        string
	lawFrom                  string
	articleCols, articleFrom string
	affected                 string // possibly_affected's rows
	links                    bool   // get_law's links come from law_link
}

// computed reads every column from the corpus tables at request time: the
// -db image, where db/prod.sql has not run.
var computed = layout{
	name: "computed",
	lawCols: `l.id, l.kind, k.name_en, l.number, to_char(l.issued_on, 'YYYY-MM-DD'), l.title, l.status,
	l.gazette_number, to_char(l.gazette_date, 'YYYY-MM-DD'),
	(SELECT min(ge.issue_id) FROM gazette_entry ge WHERE ge.law_id = l.id),
	(SELECT count(*) FROM legislation_article la WHERE la.law_id = l.id)::int,
	c.slug, c.name_en, c.name_fr, sp.url, nullif(te.title_en, ''), ps.changed_at`,
	lawJoins: `JOIN legislation_kind k ON k.name_ar = l.kind
	LEFT JOIN code c ON c.law_id = l.id
	LEFT JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
	LEFT JOIN legislation_title_en te ON te.law_id = l.id
	LEFT JOIN page_state ps ON ps.kind = 'law' AND ps.id = l.id`,
	articleCols: articleColsComputed,
	articleFrom: articleFromComputed,
	affected:    "possibly_affected",
}

// cards reads the same columns from law_card and article_card, and
// possibly_affected from possibly_affected_t, which db/prod.sql computes with the expressions above (the -db-prod image).
// law_card holds only laws whose kind has a legislation_kind row, so the
// inner join keeps the same rows.
var cards = layout{
	name: "cards",
	lawCols: `lc.id, lc.kind, lc.kind_en, lc.number, lc.issued_on, lc.title, lc.status, lc.gazette_number, lc.gazette_date,
	lc.gazette_issue_id, lc.article_count, lc.code_slug, lc.code_name_en, lc.code_name_fr, lc.source_url, lc.title_en, lc.changed_at`,
	lawJoins: `JOIN law_card lc ON lc.id = l.id LEFT JOIN code c ON c.law_id = l.id`,
	articleCols: `a.law_id, a.position, a.number, a.note, a.text_ar, a.text_fr, a.in_force_from, a.section_id,
	a.current_text, a.version, a.state, a.pending, a.valid_from, a.annulled`,
	articleFrom: `article_card a`,
	affected:    "possibly_affected_t",
}

// linked is cards plus law_link, get_law's amended_by and amended lists
// precomputed by db/prod.sql in the order lawLevelLinks returns them.
var linked layout

func init() {
	computed.lawFrom = `legislation l ` + computed.lawJoins
	cards.lawFrom = `legislation l ` + cards.lawJoins
	linked = cards
	linked.name, linked.links = "linked", true
}

// prodVersion is the mark db/prod.sql puts on law_card and article_card,
// linkVersion the one on law_link. The server reads a table only when its
// mark matches, so an image built from another prod.sql (other columns)
// falls back to computing them instead of failing.
const (
	prodVersion = "lebleg-prod 1"
	linkVersion = "lebleg-prod 2"
)

// sql is the layout this database has: linked when law_card and
// article_card carry prodVersion and law_link carries linkVersion, cards
// without that law_link, computed otherwise. It asks the database once; a
// failed check is not remembered and reads computed (correct on all, only
// slower).
func (s *Server) sql(ctx context.Context) *layout {
	if l := s.layout.Load(); l != nil {
		return l
	}
	var both, links bool
	if err := s.db.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass('law_card'), 'pg_class') = $1
		AND obj_description(to_regclass('article_card'), 'pg_class') = $1, false),
		coalesce(obj_description(to_regclass('law_link'), 'pg_class') = $2, false)`, prodVersion, linkVersion).Scan(&both, &links); err != nil {
		return &computed
	}
	l := &computed
	if both && links {
		l = &linked
	} else if both {
		l = &cards
	}
	s.layout.CompareAndSwap(nil, l)
	return s.layout.Load()
}

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
	L := s.sql(ctx)
	err := s.db.QueryRow(ctx, `SELECT `+L.lawCols+` FROM `+L.lawFrom+` WHERE l.id = $1`, id).Scan(l.dest()...)
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
