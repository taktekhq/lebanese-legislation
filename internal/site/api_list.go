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
)

// Listing by date: GET /api/v1/laws/ and GET /api/v1/gazette/. The same
// shapes as the hosted MCP server's tools list_legislation and
// list_gazette_issues, so a client can use either.

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
	since, until  string // as the caller wrote them
	from, to      *time.Time
	order         string // "newest" or "oldest"
	limit, offset int
	kind          string
}

func readListParams(r *http.Request) (listParams, error) {
	q := r.URL.Query()
	p := listParams{since: strings.TrimSpace(q.Get("since")), until: strings.TrimSpace(q.Get("until")),
		order: "newest", kind: strings.TrimSpace(q.Get("kind")),
		limit: clampInt(q.Get("limit"), listDefaultLimit, 1, listMaxLimit), offset: clampInt(q.Get("offset"), 0, 0, 1<<30)}
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
	Offset     int            `json:"offset"`
	NextOffset *int           `json:"next_offset,omitempty"`
	Laws       []apiListedLaw `json:"laws"`
}

func (s *Server) listLaws(ctx context.Context, p listParams) (apiLawList, error) {
	out := apiLawList{Since: nilIfEmpty(p.since), Until: nilIfEmpty(p.until), Kind: nilIfEmpty(p.kind),
		Order: p.order, Offset: p.offset, Laws: []apiListedLaw{}}
	const date = "coalesce(l.gazette_date, l.issued_on)"
	cond, args := p.where(date, nil)
	// a list by date leaves out the texts with no date, as the MCP Worker's
	// list_legislation does, so both answer the same total
	conds := []string{date + " IS NOT NULL"}
	if cond != "" {
		conds = append(conds, cond)
	}
	if p.kind != "" {
		args = append(args, p.kind)
		conds = append(conds, fmt.Sprintf("(l.kind = $%d OR lower(k.name_en) = lower($%d))", len(args), len(args)))
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	from := ` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind` + where
	if err := s.db.QueryRow(ctx, "SELECT count(*)"+from, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+`, l.gazette_number, l.gazette_date,
		(SELECT min(e.issue_id) FROM gazette_entry e WHERE e.law_id = l.id), `+date+from+
		fmt.Sprintf(" ORDER BY %s %s NULLS LAST, l.id %s LIMIT %d OFFSET %d", date, p.dir(), p.dir(), p.limit, p.offset), args...)
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
		out.Laws = append(out.Laws, apiListedLaw{apiLawSummary: s.lawSum(ctx, l, ""), GazetteNumber: gn,
			GazetteDate: ymd(gd), GazetteIssueID: gi, Date: ymd(d)})
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if next := p.offset + len(out.Laws); next < out.Total && len(out.Laws) > 0 {
		out.NextOffset = &next
	}
	return out, nil
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
}

type apiIssueList struct {
	Since      *string       `json:"since"`
	Until      *string       `json:"until"`
	Order      string        `json:"order"`
	Total      int           `json:"total"`
	Offset     int           `json:"offset"`
	NextOffset *int          `json:"next_offset,omitempty"`
	Issues     []apiIssueRow `json:"issues"`
}

func (s *Server) listIssues(ctx context.Context, p listParams) (apiIssueList, error) {
	out := apiIssueList{Since: nilIfEmpty(p.since), Until: nilIfEmpty(p.until), Order: p.order, Offset: p.offset, Issues: []apiIssueRow{}}
	cond, args := p.where("g.published_on", nil)
	where := ""
	if cond != "" {
		where = " WHERE " + cond
	}
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM gazette_issue g"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query(ctx, `SELECT g.id, g.number, g.published_on, (SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::int
		FROM gazette_issue g`+where+fmt.Sprintf(" ORDER BY g.published_on %s NULLS LAST, g.id %s LIMIT %d OFFSET %d", p.dir(), p.dir(), p.limit, p.offset), args...)
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
		g.PublishedOn = ymd(on)
		g.Source = "centre"
		if g.ID >= scanIDBase {
			g.Source = "scans"
		}
		g.URL = Base + "/gazette/" + strconv.Itoa(g.ID) + "/"
		out.Issues = append(out.Issues, g)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if next := p.offset + len(out.Issues); next < out.Total && len(out.Issues) > 0 {
		out.NextOffset = &next
	}
	return out, nil
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
