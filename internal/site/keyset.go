package site

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Lists page with a cursor (internal/cursor), never an OFFSET. ?after=<token>
// shows the rows after the last one of the page that linked it (older), and
// ?before=<token> the rows before its first one (newer). The first page has
// neither: it is the page the static export stores and the sitemaps list. The
// later ones come from the origin, answered by an index (db/indexes.sql,
// "Lists") in about a millisecond at any depth, and are noindex like every
// address with a query.
//
// There are no numbered pages: page n would need the n-1 pages before it
// read and thrown away. The old addresses (/opinions/page/3/, /laws/?page=3)
// move to the list's first page. A reader looking for one text searches for
// it; a list is read from the newest.

// dateKey is the sort key of a list by date, newest first with the undated
// last: the expression db/indexes.sql indexes, with the id after it.
func dateKey(col string) string { return "coalesce(" + col + ", date '" + cursor.NoDate + "')" }

// listPage is which page of a list a request asks for.
type listPage struct {
	after, before bool
	date          string // the cursor row's sort date; empty for a list by id alone
	id            int64
}

func (p listPage) first() bool { return !p.after && !p.before }

// readListPage reads ?after= or ?before=. ok is false when the token is not
// one this site made, or when both are given. byDate says the list sorts by
// a date and then the id (a DateID token), else by the id alone (an Int).
func readListPage(q url.Values, byDate bool) (p listPage, ok bool) {
	a, b := q.Get("after"), q.Get("before")
	if a == "" && b == "" {
		return p, !q.Has("after") && !q.Has("before")
	}
	if a != "" && b != "" {
		return p, false
	}
	tok := a
	p.after, p.before = a != "", b != ""
	if b != "" {
		tok = b
	}
	if byDate {
		p.date, p.id, ok = cursor.ParseDateID(tok)
	} else {
		p.id, ok = cursor.ParseInt(tok)
	}
	return p, ok
}

// where is the page's condition on the list's sort key and id ("" on the
// first page); its arguments are appended to args.
func (p listPage) where(key, id string, args *[]any) string {
	if p.first() {
		return ""
	}
	op := "<"
	if p.before {
		op = ">"
	}
	if p.date == "" {
		*args = append(*args, p.id)
		return fmt.Sprintf("%s %s $%d", id, op, len(*args))
	}
	*args = append(*args, p.date, p.id)
	return fmt.Sprintf("(%s, %s) %s ($%d::date, $%d)", key, id, op, len(*args)-1, len(*args))
}

// order is the ORDER BY of the page's query: newest first, except a page of
// newer rows, read upwards from the cursor and turned round after.
func (p listPage) order(key, id string) string {
	dir := "DESC"
	if p.before {
		dir = "ASC"
	}
	if key == "" {
		return " ORDER BY " + id + " " + dir
	}
	return " ORDER BY " + key + " " + dir + ", " + id + " " + dir
}

// and joins a list's own condition and the page's.
func and(conds ...string) string {
	var out []string
	for _, c := range conds {
		if c != "" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(out, " AND ")
}

// settle takes a page's rows as the query read them, one more than size so
// that it shows whether more follow, and puts them newest first. It reports
// whether there are older and newer rows than the ones shown. A page of newer
// rows that reaches the top of the list reports top: the caller sends the
// reader to the first page instead.
func settle[T any](p listPage, rows []T, size int) (shown []T, older, newer, top bool) {
	more := len(rows) > size
	if more {
		rows = rows[:size]
	}
	switch {
	case p.before:
		slices.Reverse(rows)
		return rows, true, more, !more
	case p.after:
		return rows, more, true, false
	}
	return rows, more, false, false
}

// pageLinks are the pager's two addresses for a list whose rows run from
// first to last (their cursor tokens), keeping the list's other parameters.
func pageLinks(q url.Values, first, last string, older, newer bool) (olderURL, newerURL template.URL) {
	keep := url.Values{}
	for k, v := range q {
		if k != "after" && k != "before" && k != "page" {
			keep[k] = v
		}
	}
	link := func(k, tok string) template.URL {
		v := url.Values{}
		for kk, vv := range keep {
			v[kk] = vv
		}
		v.Set(k, tok)
		return template.URL("?" + v.Encode())
	}
	if older {
		olderURL = link("after", last)
	}
	if newer {
		newerURL = link("before", first)
	}
	return olderURL, newerURL
}

// dateTok and idTok are the cursor tokens of one row.
func dateTok(d *time.Time, id int) string { return cursor.DateID(cursor.Date(d), int64(id)) }
func idTok(id int) string                 { return cursor.Int(int64(id)) }

// oldPage matches the numbered addresses lists had: /opinions/page/3/.
var oldPage = regexp.MustCompile(`^(.*/)page/[^/]+/$`)

// cursorPaging settles which page of a list a request asks for, or answers it
// itself (done): an old numbered address or ?page= moves (301) to the list's
// first page with its other parameters, a token this site did not make, or a
// page of newer rows that reaches the top, to the first page too (302).
func (s *Server) cursorPaging(w http.ResponseWriter, r *http.Request, byDate bool) (p listPage, done bool) {
	q := r.URL.Query()
	if m := oldPage.FindStringSubmatch(r.URL.Path); m != nil {
		http.Redirect(w, r, s.u(r, m[1])+firstPageQuery(q), http.StatusMovedPermanently)
		return p, true
	}
	if q.Has("page") {
		http.Redirect(w, r, s.u(r, r.URL.Path)+firstPageQuery(q), http.StatusMovedPermanently)
		return p, true
	}
	p, ok := readListPage(q, byDate)
	if !ok {
		http.Redirect(w, r, s.u(r, r.URL.Path)+firstPageQuery(q), http.StatusFound)
		return p, true
	}
	return p, false
}

// toFirstPage sends a reader whose page of newer rows reached the top to the
// list's first page.
func (s *Server) toFirstPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.u(r, r.URL.Path)+firstPageQuery(r.URL.Query()), http.StatusFound)
}

// firstPageQuery is a list's query without its page: "" or "?kind=...".
func firstPageQuery(q url.Values) string {
	keep := url.Values{}
	for k, v := range q {
		if k != "after" && k != "before" && k != "page" {
			keep[k] = v
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return "?" + keep.Encode()
}
