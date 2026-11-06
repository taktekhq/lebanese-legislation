package site

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Listing by date: GET /api/v1/laws/ and GET /api/v1/gazette/. The same
// shapes as the hosted MCP server's tools list_legislation and
// list_gazette_issues, so a client can use either.
//
// Both page with a cursor, never an OFFSET: next_cursor is the date and id of
// the page's last row, and the next page is a WHERE on (date, id) that an
// index answers at any depth. Page 800 of /api/v1/laws/?limit=100 by OFFSET
// read 80,000 laws: 0.85 s, 0.14 s once the ids were picked first (#172).

const (
	listDefaultLimit = 50
	listMaxLimit     = 100
)

// parseDateBound reads YYYY, YYYY-MM or YYYY-MM-DD. A bound is inclusive: as
// a start it is the first day of the period, as an end its last day.
func parseDateBound(s string, end bool) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		t, err := time.Parse(layout, s)
		if err != nil {
			continue
		}
		if end {
			switch layout {
			case "2006":
				t = t.AddDate(1, 0, -1)
			case "2006-01":
				t = t.AddDate(0, 1, -1)
			}
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a date: use YYYY, YYYY-MM or YYYY-MM-DD", s)
}

// listParams are the parameters both listings share.
type listParams struct {
	since, until string // as the caller wrote them
	from, to     *time.Time
	order        string // "newest" or "oldest"
	limit        int
	kind         string
	// after is the sort key of the previous page's last row, read from its
	// next_cursor; the page starts right after it. afterDate is
	// cursor.NoDate for a Gazette issue with no date.
	after     bool
	afterDate time.Time
	afterNone bool // afterDate is cursor.NoDate
	afterID   int64
}

func readListParams(r *http.Request) (listParams, error) {
	q := r.URL.Query()
	p := listParams{since: strings.TrimSpace(q.Get("since")), until: strings.TrimSpace(q.Get("until")),
		order: "newest", kind: strings.TrimSpace(q.Get("kind")),
		limit: clampInt(q.Get("limit"), listDefaultLimit, 1, listMaxLimit)}
	if q.Has("offset") {
		return p, errOffsetRemoved
	}
	if c := q.Get("cursor"); c != "" {
		d, id, ok := cursor.ParseDateID(c)
		if !ok || id <= 0 {
			return p, errBadCursor
		}
		p.after, p.afterID, p.afterNone = true, id, d == cursor.NoDate
		p.afterDate, _ = time.Parse("2006-01-02", d)
	}
	if o := q.Get("order"); o == "oldest" {
		p.order = "oldest"
	} else if o != "" && o != "newest" {
		return p, fmt.Errorf("order is newest or oldest")
	}
	if p.since != "" {
		t, err := parseDateBound(p.since, false)
		if err != nil {
			return p, err
		}
		p.from = &t
	}
	if p.until != "" {
		t, err := parseDateBound(p.until, true)
		if err != nil {
			return p, err
		}
		p.to = &t
	}
	return p, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// where builds the date condition on expr, with args numbered from n.
func (p listParams) where(expr string, args []any) (string, []any) {
	var conds []string
	if p.from != nil {
		args = append(args, *p.from)
		conds = append(conds, fmt.Sprintf("%s >= $%d", expr, len(args)))
	}
	if p.to != nil {
		args = append(args, *p.to)
		conds = append(conds, fmt.Sprintf("%s <= $%d", expr, len(args)))
	}
	return strings.Join(conds, " AND "), args
}

func (p listParams) dir() string {
	if p.order == "oldest" {
		return "ASC"
	}
	return "DESC"
}

// cmp is how a row after the cursor compares with it: below it newest
// first, above it oldest first.
func (p listParams) cmp() string {
	if p.order == "oldest" {
		return ">"
	}
	return "<"
}

// ---------------------------------------------------------------- texts

type apiListedLaw struct {
	apiLawSummary
	GazetteNumber  *string `json:"gazette_number,omitempty"`
	GazetteDate    *string `json:"gazette_date,omitempty"`
	GazetteIssueID *int    `json:"gazette_issue_id,omitempty"`
	Date           *string `json:"date,omitempty"` // coalesce(gazette_date, issued_on): what since, until and the order use
}

type apiLawList struct {
	Since      *string        `json:"since"`
	Until      *string        `json:"until"`
	Kind       *string        `json:"kind"`
	Order      string         `json:"order"`
	Total      int            `json:"total"`
	NextCursor string         `json:"next_cursor,omitempty"`
	Laws       []apiListedLaw `json:"laws"`
}

// kindNames are the Arabic names of the kinds name stands for: the Arabic
// name itself, or every kind whose English name it is ("Constitution" is
// two). The list filters on these names rather than joining
// legislation_kind, so a page is one scan of legislation_pub_kind: decrees
// before 1990 by the join read 9,293 rows and sorted them (16 ms), by
// kind = $1 they read the 101 of the page (0.4 ms).
func (s *Server) kindNames(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT name_ar FROM legislation_kind WHERE name_ar = $1 OR lower(name_en) = lower($1) ORDER BY name_ar`, name)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (s *Server) listLaws(ctx context.Context, p listParams) (apiLawList, error) {
	out := apiLawList{Since: nilIfEmpty(p.since), Until: nilIfEmpty(p.until), Kind: nilIfEmpty(p.kind),
		Order: p.order, Laws: []apiListedLaw{}}
	const date = "coalesce(l.gazette_date, l.issued_on)"
	cond, args := p.where(date, nil)
	// a list by date leaves out the texts with no date, as the MCP Worker's
	// list_legislation does, so both answer the same total
	conds := []string{date + " IS NOT NULL"}
	if cond != "" {
		conds = append(conds, cond)
	}
	if p.kind != "" {
		kinds, err := s.kindNames(ctx, p.kind)
		if err != nil || len(kinds) == 0 {
			return out, err
		}
		if len(kinds) == 1 {
			args = append(args, kinds[0])
			conds = append(conds, fmt.Sprintf("l.kind = $%d", len(args)))
		} else {
			args = append(args, kinds)
			conds = append(conds, fmt.Sprintf("l.kind = ANY($%d)", len(args)))
		}
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	if p.after {
		args = append(args, p.afterDate, p.afterID)
		where += fmt.Sprintf(" AND (%s, l.id) %s ($%d::date, $%d::int)", date, p.cmp(), len(args)-1, len(args))
	}
	// ids first, one more than the page to know there is a next, then the
	// page's rows, as /laws/ (handlers.go)
	order := fmt.Sprintf(" ORDER BY %s %s, l.id %s", date, p.dir(), p.dir())
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+`, l.gazette_number, l.gazette_date,
		(SELECT min(e.issue_id) FROM gazette_entry e WHERE e.law_id = l.id), `+date+
		` FROM (SELECT l.id FROM legislation l`+where+order+fmt.Sprintf(" LIMIT %d", p.limit+1)+
		`) p JOIN legislation l ON l.id = p.id JOIN legislation_kind k ON k.name_ar = l.kind`+order, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var l LawRow
		var gn *string
		var gd, d *time.Time
		var gi *int
		if err := rows.Scan(append(lawRowDest(&l), &gn, &gd, &gi, &d)...); err != nil {
			return out, err
		}
		if len(out.Laws) == p.limit { // the row after the page
			last := out.Laws[len(out.Laws)-1]
			out.NextCursor = cursor.DateID(*last.Date, int64(last.ID))
			break
		}
		out.Laws = append(out.Laws, apiListedLaw{apiLawSummary: s.lawSum(ctx, l, ""), GazetteNumber: gn,
			GazetteDate: ymd(gd), GazetteIssueID: gi, Date: ymd(d)})
	}
	return out, rows.Err()
}

func (s *Server) apiLaws(w http.ResponseWriter, r *http.Request) {
	p, err := readListParams(r)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiErrorBody{Error: err.Error()})
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	out, err := s.listLaws(ctx, p)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- issues

type apiIssueRow struct {
	ID          int     `json:"id"`
	Number      *string `json:"number,omitempty"`
	PublishedOn *string `json:"published_on,omitempty"`
	TextCount   int     `json:"text_count"`
	Source      string  `json:"source"` // "centre", or "scans" for an issue made for texts read from the Gazette's scanned pages
	URL         string  `json:"url"`
	UpdatedAt   *string `json:"updated_at,omitempty"` // the day its page last changed here, when known
}

type apiIssueList struct {
	Since      *string       `json:"since"`
	Until      *string       `json:"until"`
	Order      string        `json:"order"`
	Total      int           `json:"total"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Issues     []apiIssueRow `json:"issues"`
}

// listIssues lists the issues by date, then id. With no since or until the
// issues with no date (3 of 6,851) come last in either order: the dated by
// (published_on, id), then the undated by id, both read from
// gazette_pub_date. A cursor among the undated has the date cursor.NoDate.
func (s *Server) listIssues(ctx context.Context, p listParams) (apiIssueList, error) {
	out := apiIssueList{Since: nilIfEmpty(p.since), Until: nilIfEmpty(p.until), Order: p.order, Issues: []apiIssueRow{}}
	cond, args := p.where("g.published_on", nil)
	where := ""
	if cond != "" {
		where = " WHERE " + cond
	}
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM gazette_issue g"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	dir, n := p.dir(), p.limit+1
	dated := []string{"g.published_on IS NOT NULL"}
	if cond != "" {
		dated = append(dated, cond)
	}
	undated := []string{"g.published_on IS NULL"}
	switch {
	case p.after && p.afterNone: // the previous page ended among the undated
		dated = nil
		args = append(args, p.afterID)
		undated = append(undated, fmt.Sprintf("g.id %s $%d::int", p.cmp(), len(args)))
	case p.after:
		args = append(args, p.afterDate, p.afterID)
		dated = append(dated, fmt.Sprintf("(g.published_on, g.id) %s ($%d::date, $%d::int)", p.cmp(), len(args)-1, len(args)))
	}
	if cond != "" { // a bound leaves out the undated, as published_on >= $1 does
		undated = nil
	}
	var parts []string
	if dated != nil {
		parts = append(parts, fmt.Sprintf("(SELECT g.id, g.published_on FROM gazette_issue g WHERE %s ORDER BY g.published_on %s, g.id %s LIMIT %d)",
			strings.Join(dated, " AND "), dir, dir, n))
	}
	if undated != nil {
		parts = append(parts, fmt.Sprintf("(SELECT g.id, g.published_on FROM gazette_issue g WHERE %s ORDER BY g.id %s LIMIT %d)",
			strings.Join(undated, " AND "), dir, n))
	}
	if len(parts) == 0 {
		return out, nil
	}
	order := fmt.Sprintf(" ORDER BY g.published_on %s NULLS LAST, g.id %s", dir, dir)
	rows, err := s.db.Query(ctx, `SELECT g.id, g.number, g.published_on, (SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::int
		FROM (SELECT * FROM (`+strings.Join(parts, " UNION ALL ")+`) g`+order+fmt.Sprintf(" LIMIT %d", n)+
		`) p JOIN gazette_issue g ON g.id = p.id`+order, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var g apiIssueRow
		var on *time.Time
		if err := rows.Scan(&g.ID, &g.Number, &on, &g.TextCount); err != nil {
			return out, err
		}
		if len(out.Issues) == p.limit { // the row after the page
			last := out.Issues[len(out.Issues)-1]
			d := cursor.NoDate
			if last.PublishedOn != nil {
				d = *last.PublishedOn
			}
			out.NextCursor = cursor.DateID(d, int64(last.ID))
			break
		}
		g.PublishedOn = ymd(on)
		g.Source = "centre"
		if g.ID >= scanIDBase {
			g.Source = "scans"
		}
		g.URL = Base + "/gazette/" + strconv.Itoa(g.ID) + "/"
		g.UpdatedAt = s.listedChange(ctx, "gazette", g.ID)
		out.Issues = append(out.Issues, g)
	}
	return out, rows.Err()
}

func (s *Server) apiGazettes(w http.ResponseWriter, r *http.Request) {
	p, err := readListParams(r)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, apiErrorBody{Error: err.Error()})
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	out, err := s.listIssues(ctx, p)
	if err != nil {
		s.apiFail(w, r, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- amended_by / amended

// apiLawChange is one text that changed this law (amended_by) or that this
// law changed (amended): one entry per relation and other text. Articles are
// the articles of THIS law the other text touches (amended_by), or the
// articles of the other text this law touches (amended); empty when the
// change is to the text as a whole.
type apiLawChange struct {
	Relation    string        `json:"relation"`
	Verb        string        `json:"verb"`
	Law         apiLawSummary `json:"law"`
	EffectiveOn *string       `json:"effective_on,omitempty"`
	Articles    []string      `json:"articles,omitempty"`
}

// changeVerbs are the hosted MCP server's words for each relation.
var changeVerbs = map[string]string{
	"replace": "Replaced or reworded", "amend": "Edited in part", "repeal": "Repealed",
	"insert": "Added to", "suspend": "Suspended", "extend": "Extended",
}

// changeRelation names a change the way the graph does: a part reworded is
// "amend", a part added is "insert", else the action itself.
func changeRelation(c Change) string {
	switch {
	case c.Part != nil && *c.Part == "+":
		return "insert"
	case c.Part != nil && c.Action == "replace":
		return "amend"
	}
	return c.Action
}

// changeRelations are the graph relations that change a text (the MCP's
// CHANGE_RELATIONS).
var changeRelations = []string{"replace", "amend", "repeal", "insert", "suspend", "extend"}

// lawLinks lists the texts that changed law id (toThis) or that it changed,
// from graph_edge, the way the MCP's get_law does: linked and no_article
// edges between legislation, one entry per relation and other text, ordered
// by the other text's date (gazette date, else issue date), then id. Edges to
// or from one article add that article to Articles. The date shown is the
// amending text's: the other text's for amended_by, selfDate (this law's
// gazette date, else issue date) for amended.
func (s *Server) lawLinks(ctx context.Context, id int, toThis bool, selfDate *time.Time) ([]apiLawChange, error) {
	near := "e.from_id" // the other text's side of the edge
	if !toThis {
		near = "e.to_law_id"
	}
	where := "e.to_law_id = $1 AND e.from_type = 'legislation' AND e.from_id <> $1"
	if !toThis {
		where = "e.from_id = $1 AND e.from_type = 'legislation' AND e.to_law_id <> $1"
	}
	rows, err := s.db.Query(ctx, `SELECT e.relation, coalesce(e.to_article, ''), coalesce(l.gazette_date, l.issued_on), `+lawRowCols+`
		FROM graph_edge e JOIN legislation l ON l.id = `+near+` JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE `+where+` AND e.status IN ('linked', 'no_article') AND e.relation = ANY($2)
		ORDER BY coalesce(l.gazette_date, l.issued_on) NULLS FIRST, l.id, e.id`, id, changeRelations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		relation string
		law      int
	}
	idx := map[key]int{}
	var out []apiLawChange
	for rows.Next() {
		var rel, art string
		var on *time.Time
		var l LawRow
		if err := rows.Scan(append([]any{&rel, &art, &on}, lawRowDest(&l)...)...); err != nil {
			return nil, err
		}
		k := key{rel, l.ID}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			verb := changeVerbs[rel]
			if verb == "" {
				verb = "Changed"
			}
			e := apiLawChange{Relation: rel, Verb: verb, Law: s.lawSum(ctx, l, "")}
			if !toThis {
				on = selfDate
			}
			e.EffectiveOn = ymd(on)
			out = append(out, e)
		}
		if art != "" {
			out[i].Articles = appendNew(out[i].Articles, art)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		sortArticles(out[i].Articles)
	}
	return out, nil
}

// sortArticles orders article numbers the way the MCP does (numeric-aware:
// "2" before "10", "12" before "12-1" before "12 مكرر").
func sortArticles(xs []string) {
	sort.SliceStable(xs, func(a, b int) bool { return naturalLess(xs[a], xs[b]) })
}

func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[da:], b[db:]
			continue
		}
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			return ra < rb
		}
		a, b = a[sa:], b[sb:]
	}
	return len(a) < len(b)
}

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// mergeAmendments adds what the amendment table (the changes parsed down to
// the article) knows and the graph lacks: a pair (relation, other text) the
// graph does not list, articles not yet named, and a date when the graph has
// none. The graph's entries keep their order; an extra pair is placed by the
// other text's issue date.
func mergeAmendments(graph, amend []apiLawChange) []apiLawChange {
	type key struct {
		relation string
		law      int
	}
	at := map[key]int{}
	for i, g := range graph {
		at[key{g.Relation, g.Law.ID}] = i
	}
	for _, a := range amend {
		i, ok := at[key{a.Relation, a.Law.ID}]
		if !ok {
			// slot after the last entry whose text is not newer
			pos := len(graph)
			for pos > 0 && a.Law.IssuedOn != nil && graph[pos-1].Law.IssuedOn != nil && *graph[pos-1].Law.IssuedOn > *a.Law.IssuedOn {
				pos--
			}
			graph = append(graph, apiLawChange{})
			copy(graph[pos+1:], graph[pos:])
			graph[pos] = a
			at = map[key]int{}
			for j, g := range graph {
				at[key{g.Relation, g.Law.ID}] = j
			}
			continue
		}
		if graph[i].EffectiveOn == nil {
			graph[i].EffectiveOn = a.EffectiveOn
		}
		for _, art := range a.Articles {
			graph[i].Articles = appendNew(graph[i].Articles, art)
		}
		sortArticles(graph[i].Articles)
	}
	return graph
}

func (s *Server) lawChanges(ctx context.Context, changes []Change) []apiLawChange {
	type key struct {
		relation string
		law      int
	}
	idx := map[key]int{}
	var out []apiLawChange
	var eff []*time.Time // the earliest effective date among a group's changes
	for _, c := range changes {
		rel := changeRelation(c)
		k := key{rel, c.By.ID}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			verb := changeVerbs[rel]
			if verb == "" {
				verb = "Changed"
			}
			out = append(out, apiLawChange{Relation: rel, Verb: verb, Law: s.lawSum(ctx, c.By, "")})
			eff = append(eff, nil)
		}
		if c.Effective != nil && (eff[i] == nil || c.Effective.Before(*eff[i])) {
			eff[i] = c.Effective
		}
		if c.Article != nil && *c.Article != "" {
			out[i].Articles = appendNew(out[i].Articles, *c.Article)
		}
	}
	// Dated by the change, else by the other text's own date; oldest first.
	when := make([]time.Time, len(out))
	for i := range out {
		switch {
		case eff[i] != nil:
			when[i] = *eff[i]
		case out[i].Law.IssuedOn != nil:
			when[i], _ = time.Parse("2006-01-02", *out[i].Law.IssuedOn)
		}
		if !when[i].IsZero() {
			out[i].EffectiveOn = ymd(&when[i])
		}
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return when[order[a]].Before(when[order[b]]) })
	sorted := make([]apiLawChange, len(out))
	for i, o := range order {
		sorted[i] = out[o]
	}
	return sorted
}
