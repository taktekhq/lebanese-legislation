package site

// Paging by cursor (searchcursor.go, api_list.go): every page followed by
// next_cursor, concatenated, is the same rows as one unpaged ORDER BY, with
// none twice and none missing. The corpus walks need LEBLEG_DSN (read only);
// the fixture walks, with many ties and undated rows, need LEBLEG_TEST_DSN.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// ---------------------------------------------------------------- the contract, no database

func TestSearchCursorRoundTrip(t *testing.T) {
	var next [4]searchPos
	next[partLaws] = searchPos{phase: lawsEnglish, key: []string{"1", "0", "2017-02-10", "2000200594"}}
	next[partArticles] = searchPos{phase: phaseRanked, key: []string{keyDouble(6.060792922973633e-05), "1", "2000200594", "1107"}}
	next[partRulings] = searchPos{phase: phaseRanked, key: []string{"0", "1", keyReal(6.0759614e-05), cursor.NoDate, "2000157573"}}
	next[partGazette] = searchPos{phase: phaseDone}
	tok := encodeSearchCursor("", next)
	if len(tok) > 300 {
		t.Errorf("a search token for all four parts is %d characters", len(tok))
	}
	c, ok := decodeSearchCursor(tok, "")
	if !ok {
		t.Fatalf("does not decode: %q", tok)
	}
	for p := range 4 {
		got := c.pos(p)
		if got.phase != next[p].phase || !slices.Equal(got.key, next[p].key) {
			t.Errorf("part %d: %+v, want %+v", p, *got, next[p])
		}
	}
	if argReal(c.pos(partRulings).key[2]) != float32(6.0759614e-05) || argDouble(c.pos(partArticles).key[0]) != 6.060792922973633e-05 {
		t.Error("a rank does not read back to the same float")
	}
	// one part: its own fields only; a part from its phase's start has no key
	one := encodeSearchCursor("rulings", [4]searchPos{partRulings: {phase: phaseRanked}})
	if c, ok := decodeSearchCursor(one, "rulings"); !ok || c.pos(partRulings).phase != phaseRanked || c.pos(partRulings).key != nil {
		t.Errorf("rulings from the start: %v %+v", ok, c)
	}
	if encodeSearchCursor("", [4]searchPos{}) != "" || encodeSearchCursor("laws", [4]searchPos{partArticles: {phase: phaseRanked}}) != "" {
		t.Error("a search with every part done has a next_cursor")
	}
	for _, bad := range []struct{ tok, typ string }{
		{one, ""},     // made for one part, read for four
		{one, "laws"}, // another part's fields
		{"x", "laws"}, // not a token
		{cursor.Encode("4", "0", "0", cursor.NoDate, "0"), "laws"},             // no phase 4
		{cursor.Encode("2", "0", "0", cursor.NoDate, "0"), "gazette"},          // gazette has 3 fields
		{cursor.Encode("1", "x", cursor.NoDate), "gazette"},                    // a date that is not one
		{cursor.Encode("1", "2020-01-01", "1.5"), "gazette"},                   // an id that is not an integer
		{cursor.Encode("1", "1", "nan?", cursor.NoDate, "3"), "laws"},          // a rank that is not a number
		{cursor.Encode("1", "1", "0.5", cursor.NoDate, "99999999999"), "laws"}, // past int32
	} {
		if _, ok := decodeSearchCursor(bad.tok, bad.typ); ok {
			t.Errorf("accepted %q for type %q", bad.tok, bad.typ)
		}
	}
}

// offset answers 400 with what to pass instead, and so does a cursor the API
// did not make, before anything is read.
func TestPagingErrors(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		path string
		h    func(*Server, http.ResponseWriter, *http.Request)
		want error
	}{
		{"/api/v1/laws/?offset=100", (*Server).apiLaws, errOffsetRemoved},
		{"/api/v1/laws/?offset=0&limit=10", (*Server).apiLaws, errOffsetRemoved},
		{"/api/v1/gazette/?since=2020&offset=50", (*Server).apiGazettes, errOffsetRemoved},
		{"/api/v1/search/?q=x&offset=10", (*Server).apiSearch, errOffsetRemoved},
		{"/api/v1/search/?offset=10", (*Server).apiSearch, errOffsetRemoved},
		{"/api/v1/laws/?cursor=abc", (*Server).apiLaws, errBadCursor},
		{"/api/v1/laws/?cursor=" + cursor.Int(5), (*Server).apiLaws, errBadCursor},
		{"/api/v1/gazette/?cursor=" + cursor.DateID("2020-01-01", 0), (*Server).apiGazettes, errBadCursor},
		{"/api/v1/search/?q=x&type=laws&cursor=" + cursor.DateID("2020-01-01", 5), (*Server).apiSearch, errBadCursor},
		{"/api/v1/search/?q=x&cursor=" + encodeSearchCursor("laws", [4]searchPos{partLaws: {phase: phaseRanked}}), (*Server).apiSearch, errBadCursor},
	} {
		rec := httptest.NewRecorder()
		c.h(s, rec, httptest.NewRequest("GET", c.path, nil))
		var body apiErrorBody
		json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != 400 || body.Error != c.want.Error() {
			t.Errorf("%s: %d %q, want 400 %q", c.path, rec.Code, body.Error, c.want)
		}
	}
}

// ---------------------------------------------------------------- walking

// walk follows next_cursor from path to the last page and returns every
// page, decoded as T. It fails past 5,000 pages.
func walk[T any](t *testing.T, s *Server, path string, next func(T) string) []T {
	t.Helper()
	var pages []T
	sep := "&"
	if !strings.Contains(path, "?") {
		sep = "?"
	}
	for cur := ""; len(pages) < 5000; {
		p := path
		if cur != "" {
			p += sep + "cursor=" + cur
		}
		var page T
		if resp := getJSON(t, s, p, &page); resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d", p, resp.StatusCode)
		}
		pages = append(pages, page)
		if cur = next(page); cur == "" {
			return pages
		}
	}
	t.Fatalf("%s: more than 5,000 pages", path)
	return nil
}

// listCase is one listing: its parameters, both as the URL and as the SQL
// the listing ran before cursors (the same rows, by OFFSET).
type listCase struct {
	since, until, kind string
	oldest             bool
}

func (c listCase) query(limit int) string {
	v := url.Values{"limit": {strconv.Itoa(limit)}}
	for k, x := range map[string]string{"since": c.since, "until": c.until, "kind": c.kind} {
		if x != "" {
			v.Set(k, x)
		}
	}
	if c.oldest {
		v.Set("order", "oldest")
	}
	return v.Encode()
}

func (c listCase) bounds(t *testing.T) (from, to *time.Time) {
	if c.since != "" {
		f, err := parseDateBound(c.since, false)
		if err != nil {
			t.Fatal(err)
		}
		from = &f
	}
	if c.until != "" {
		e, err := parseDateBound(c.until, true)
		if err != nil {
			t.Fatal(err)
		}
		to = &e
	}
	return from, to
}

func (c listCase) dir() string {
	if c.oldest {
		return "ASC"
	}
	return "DESC"
}

// oldLaws is /api/v1/laws/ before cursors: the kind joined, NULLS LAST.
func (c listCase) oldLaws(t *testing.T, pool *pgxpool.Pool) []int {
	from, to := c.bounds(t)
	rows, err := pool.Query(t.Context(), `SELECT l.id FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE coalesce(l.gazette_date, l.issued_on) IS NOT NULL
		AND ($1::date IS NULL OR coalesce(l.gazette_date, l.issued_on) >= $1) AND ($2::date IS NULL OR coalesce(l.gazette_date, l.issued_on) <= $2)
		AND ($3 = '' OR l.kind = $3 OR lower(k.name_en) = lower($3))
		ORDER BY coalesce(l.gazette_date, l.issued_on) `+c.dir()+` NULLS LAST, l.id `+c.dir(), from, to, c.kind)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// oldIssues is /api/v1/gazette/ before cursors.
func (c listCase) oldIssues(t *testing.T, pool *pgxpool.Pool) []int {
	from, to := c.bounds(t)
	rows, err := pool.Query(t.Context(), `SELECT g.id FROM gazette_issue g
		WHERE ($1::date IS NULL OR g.published_on >= $1) AND ($2::date IS NULL OR g.published_on <= $2)
		ORDER BY g.published_on `+c.dir()+` NULLS LAST, g.id `+c.dir(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// walkLists walks each case of both lists, limit a page, and compares the
// ids with the old query's.
func walkLists(t *testing.T, s *Server, pool *pgxpool.Pool, limit int, laws, issues []listCase) {
	t.Helper()
	for _, c := range laws {
		pages := walk(t, s, "/api/v1/laws/?"+c.query(limit), func(p apiLawList) string { return p.NextCursor })
		var got []int
		for i, p := range pages {
			if p.Total != pages[0].Total || len(p.Laws) > limit || (len(p.Laws) < limit && i < len(pages)-1) {
				t.Errorf("laws %+v page %d: total %d (first %d), %d laws", c, i, p.Total, pages[0].Total, len(p.Laws))
			}
			for _, l := range p.Laws {
				got = append(got, l.ID)
			}
		}
		want := c.oldLaws(t, pool)
		if !slices.Equal(got, want) || pages[0].Total != len(want) {
			t.Errorf("laws %+v: %d ids by cursor (total %d), %d by the old query; first difference at %d", c, len(got), pages[0].Total, len(want), firstDiff(got, want))
		}
	}
	for _, c := range issues {
		pages := walk(t, s, "/api/v1/gazette/?"+c.query(limit), func(p apiIssueList) string { return p.NextCursor })
		var got []int
		for _, p := range pages {
			for _, g := range p.Issues {
				got = append(got, g.ID)
			}
		}
		want := c.oldIssues(t, pool)
		if !slices.Equal(got, want) || pages[0].Total != len(want) {
			t.Errorf("gazette %+v: %d ids by cursor (total %d), %d by the old query; first difference at %d", c, len(got), pages[0].Total, len(want), firstDiff(got, want))
		}
	}
}

func firstDiff[T comparable](a, b []T) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) == len(b) {
		return -1
	}
	return min(len(a), len(b))
}

// searchIDs are a search's rows, part by part: law ids, article keys
// (law/number), ruling ids, Gazette issue ids.
type searchIDs [4][]string

func (ids *searchIDs) add(r apiSearchResult) {
	for _, l := range r.Laws {
		ids[partLaws] = append(ids[partLaws], strconv.Itoa(l.ID))
	}
	for _, a := range r.Articles {
		ids[partArticles] = append(ids[partArticles], strconv.Itoa(a.Law.ID)+"/"+a.Number)
	}
	for _, x := range r.Rulings {
		ids[partRulings] = append(ids[partRulings], strconv.Itoa(x.ID))
	}
	for _, g := range r.Gazette {
		ids[partGazette] = append(ids[partGazette], strconv.Itoa(g.ID))
	}
}

// unpagedSearch is the first page of a search as apiSearch runs it, with a
// page big enough for every row: one ORDER BY per part, no cursor.
func unpagedSearch(t *testing.T, s *Server, q, typ string) apiSearchResult {
	t.Helper()
	const big = 20000
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out := apiSearchResult{Q: q}
	var err error
	switch typ {
	case "laws":
		err = s.apiFindLaws(ctx, q, 0, big, nil, &out)
	case "articles":
		err = s.apiFindArticles(ctx, q, big, nil, &out)
	case "rulings":
		err = s.apiFindRulings(ctx, q, big, nil, &out)
	case "gazette":
		err = s.apiFindGazette(ctx, q, big, nil, &out)
	default:
		err = s.apiFindAll(ctx, q, 0, big, nil, &out)
	}
	if err != nil {
		t.Fatal(err)
	}
	s.apiRescue(ctx, q, typ, 0, true, big, &out)
	if typ == "" || typ == "laws" {
		s.apiFindLawsEn(ctx, q, 0, big, &out)
	}
	if encodeSearchCursor(typ, out.next) != "" {
		t.Fatalf("%q %s: more than %d rows in a part", q, typ, big)
	}
	return out
}

// walkSearch walks a search by next_cursor and compares each part with the
// unpaged search: the same rows in the same order, none twice. It returns
// how many pages it took.
func walkSearch(t *testing.T, s *Server, q, typ string, limit int) int {
	t.Helper()
	path := "/api/v1/search/?q=" + url.QueryEscape(q) + "&limit=" + strconv.Itoa(limit)
	if typ != "" {
		path += "&type=" + typ
	}
	pages := walk(t, s, path, func(r apiSearchResult) string { return r.NextCursor })
	var got, want searchIDs
	for i, p := range pages {
		got.add(p)
		for part, n := range []int{len(p.Laws), len(p.Articles), len(p.Rulings), len(p.Gazette)} {
			if n > limit {
				t.Errorf("%q %s page %d: %d rows in part %d", q, typ, i, n, part)
			}
		}
	}
	want.add(unpagedSearch(t, s, q, typ))
	for part := range 4 {
		g, w := got[part], want[part]
		if !slices.Equal(g, w) {
			t.Errorf("%q %s part %s: %d rows by cursor, %d unpaged; first difference at %d", q, typ, searchParts[part].name, len(g), len(w), firstDiff(g, w))
		}
		seen := map[string]bool{}
		for _, id := range g {
			if seen[id] {
				t.Errorf("%q %s part %s: %s listed twice", q, typ, searchParts[part].name, id)
			}
			seen[id] = true
		}
	}
	t.Logf("%q %s by %d: %d pages; laws %d, articles %d, rulings %d, gazette %d", q, typ, limit, len(pages),
		len(got[partLaws]), len(got[partArticles]), len(got[partRulings]), len(got[partGazette]))
	return len(pages)
}

// ---------------------------------------------------------------- on the corpus

func TestCursorWalkCorpus(t *testing.T) {
	s := testServer(t)
	s.SetRateLimit(0, 1)
	pool := s.db
	walkLists(t, s, pool, 100, []listCase{
		{since: "2024"}, {since: "2024", oldest: true},
		{since: "2000", until: "2010", kind: "Law"}, {since: "2000", until: "2010", kind: "قانون", oldest: true},
		{kind: "Constitution"}, {kind: "constitution", oldest: true},
		{until: "1930-06", kind: "Decree"}, {kind: "no such kind"},
	}, []listCase{
		{}, {oldest: true}, // every issue, the 3 undated last both ways
		{since: "2000", until: "2005"}, {since: "2000", until: "2005", oldest: true},
	})
	for _, c := range []struct {
		q, typ string
		limit  int
	}{
		{"technology", "laws", 50}, // 4 Arabic titles, then the English ones without those 4
		{"شيك", "rulings", 50},     // 550 by summary, then the judgments only
		{"الغرامة الاكراهية", "articles", 7},
		{"شيك", "articles", 50},
		{"12", "gazette", 20},
		{"شيك", "", 50}, // every part in one cursor
	} {
		walkSearch(t, s, c.q, c.typ, c.limit)
	}
	// get_law's outline, 400 articles at a time by articles_cursor, is the
	// whole outline of the longest text
	ctx := t.Context()
	var long int
	if err := pool.QueryRow(ctx, `SELECT law_id FROM legislation_article GROUP BY 1 ORDER BY count(*) DESC, 1 LIMIT 1`).Scan(&long); err != nil {
		t.Fatal(err)
	}
	_, whole, err := s.lawDetail(ctx, long)
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, a := range whole.Articles {
		want = append(want, a.Number)
	}
	pages := 0
	for c := ""; pages < 100; pages++ {
		res, isErr, err := s.toolGetLaw(ctx, json.RawMessage(fmt.Sprintf(`{"id": %d, "articles_limit": 400, "articles_cursor": %q}`, long, c)))
		if err != nil || isErr {
			t.Fatalf("get_law %d from %q: %v %v", long, c, res, err)
		}
		d := res.(map[string]any)["result"].(mcpLawDetail)
		for _, a := range d.Articles {
			got = append(got, a.Number)
		}
		if c = d.ArticlesNextCursor; c == "" {
			break
		}
	}
	if !slices.Equal(got, want) || pages != (len(want)-1)/400 {
		t.Errorf("get_law %d by 400: %d articles in %d pages, the outline has %d", long, len(got), pages+1, len(want))
	}
}

// ---------------------------------------------------------------- on a fixture

// cursorFixture has many rows that rank alike and rows with no date:
//
//   - 300 texts on seven days, a third with a Gazette date, 10 with no date;
//     five kinds, two of them "Constitution"; titles in five shapes, one with
//     "pilot" in Latin letters; texts 5, 10, 15 are codes; 75 have an English
//     title "Pilot ...", 15 of those also "pilot" in the Arabic one;
//   - 240 articles, in three ranks;
//   - 70 rulings, 30 matching by summary, 25 to 70 by their judgment's pages;
//   - 120 Gazette issues on five days, 6 with no date.
const cursorFixture = `
INSERT INTO legislation_kind (name_ar, name_en) VALUES ('قانون', 'Law'), ('مرسوم', 'Decree'),
  ('دستور', 'Constitution'), ('الدستور', 'Constitution'), ('تعميم', NULL);
INSERT INTO legislation (id, kind, number, issued_on, gazette_date, title, status)
SELECT i, (ARRAY['قانون', 'مرسوم', 'دستور', 'الدستور', 'تعميم'])[1 + i % 5], i::text,
  CASE WHEN i % 30 <> 0 THEN date '2020-01-01' + (i % 7) * 31 END,
  CASE WHEN i % 3 = 0 AND i % 30 <> 0 THEN date '2020-01-15' + (i % 4) * 31 END,
  'نص تجربة ' || (ARRAY['أول', 'ثان', 'ثالث في التجربة', 'رابع', 'pilot خامس'])[1 + i % 5], 'in_force'
FROM generate_series(1, 300) i;
INSERT INTO code (slug, law_id, name_en, position) VALUES ('code-a', 5, 'Code A', 1), ('code-b', 10, 'Code B', 2), ('code-c', 15, 'Code C', 3);
INSERT INTO legislation_title_en (law_id, title_en, model)
SELECT i, 'Pilot programme text ' || i, 'test' FROM generate_series(4, 300, 4) i;
INSERT INTO legislation_article (law_id, position, number, text_ar)
SELECT l, p, p::text, repeat('تجربة ', 1 + (l + p) % 3) || 'نص المادة' FROM generate_series(1, 80) l, generate_series(1, 3) p;
INSERT INTO gazette_issue (id, number, published_on)
SELECT i, (i % 13)::text, CASE WHEN i % 20 <> 0 THEN date '2019-01-01' + (i % 5) * 40 END FROM generate_series(1, 120) i;
INSERT INTO gazette_entry (issue_id, law_id) SELECT i, i FROM generate_series(1, 120) i;
INSERT INTO ruling (id, number, year, decided_on, title, text_ar)
SELECT i, i::text, 2021, CASE WHEN i % 9 <> 0 THEN date '2021-01-01' + i % 4 END, 'حكم رقم ' || i,
  CASE WHEN i <= 30 THEN repeat('تجربة ', 1 + i % 2) || 'خلاصة' ELSE 'خلاصة أخرى' END
FROM generate_series(1, 70) i;
INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at)
SELECT r, n, repeat('تجربة ', 1 + (r + n) % 3) || 'صفحة', 'm', now() FROM generate_series(25, 70) r, generate_series(1, 2) n;
`

// cursorFixtureServer makes a database of its own on the disposable server
// (LEBLEG_TEST_DSN), with the schema, the fixture and the indexes.
func cursorFixtureServer(t *testing.T) (*Server, *pgxpool.Pool) {
	t.Helper()
	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := fmt.Sprintf("cursor_walk_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Skipf("cannot create a database: %v", err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), dsn); err == nil {
			c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	own := replaceDB(dsn, name)
	conn, err := pgx.Connect(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{db.Schema, db.AmendmentSchema, db.GraphSchema, db.OfficialSchema, cursorFixture, db.Indexes, "ANALYZE"} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatalf("%.60s: %v", q, err)
		}
	}
	conn.Close(context.Background())
	pool, err := pgxpool.New(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	s.SetRateLimit(0, 1)
	return s, pool
}

func testDSN(t *testing.T) string {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	return dsn
}

func TestCursorWalkFixture(t *testing.T) {
	s, pool := cursorFixtureServer(t)
	var all []listCase
	for _, c := range []listCase{
		{}, {kind: "Constitution"}, {kind: "قانون", since: "2020-02", until: "2020-05"}, {since: "2020-03-15"}, {kind: "nothing"},
	} {
		all = append(all, c, listCase{since: c.since, until: c.until, kind: c.kind, oldest: true})
	}
	issues := []listCase{{}, {oldest: true}, {since: "2019-02", until: "2019-06"}, {since: "2019-02", until: "2019-06", oldest: true}}
	for _, limit := range []int{7, 1, 100} {
		walkLists(t, s, pool, limit, all, issues)
	}
	// the undated issues come last both ways, by id
	for _, oldest := range []bool{false, true} {
		ids := listCase{oldest: oldest}.oldIssues(t, pool)
		tail := []int{20, 40, 60, 80, 100, 120}
		if !oldest {
			slices.Reverse(tail)
		}
		if !slices.Equal(ids[len(ids)-6:], tail) {
			t.Errorf("oldest %v: the list ends %v, want the undated %v", oldest, ids[len(ids)-6:], tail)
		}
	}
	for _, c := range []struct {
		q, typ string
		limit  int
	}{
		{"تجربة", "laws", 7},     // 300 titles, ranked alike in five shapes
		{"pilot", "laws", 7},     // 60 Arabic titles, then 60 English ones (15 found by both, listed once)
		{"pilot", "laws", 50},    //
		{"تجربة", "articles", 7}, // 240 articles in three ranks
		{"تجربة", "rulings", 7},  // 30 by summary, then 40 by the judgment only; the change falls inside page 5
		{"تجربة", "rulings", 1},  //
		{"تجربة", "rulings", 10}, // the summaries fill page 3 exactly
		{"تجربة", "rulings", 15}, //
		{"1", "gazette", 7},      // one undated among them
		{"تجربة", "", 7},         // the parts end on different pages
		{"pilot", "", 3},         //
		{"12", "", 2},            //
	} {
		walkSearch(t, s, c.q, c.typ, c.limit)
	}
	// "pilot": the English titles leave out the 15 the Arabic titles found
	out := unpagedSearch(t, s, "pilot", "laws")
	if len(out.Laws) != 120 || out.LawCount != 135 {
		t.Errorf("pilot: %d laws listed (want 120), law_count %d (want 60 + 75)", len(out.Laws), out.LawCount)
	}
	// a page boundary inside the undated issues: page 17 of 7 ends on the 5th
	pages := walk(t, s, "/api/v1/gazette/?limit=7", func(p apiIssueList) string { return p.NextCursor })
	if len(pages) != 18 || len(pages[17].Issues) != 1 || pages[17].Issues[0].PublishedOn != nil {
		t.Errorf("gazette by 7: %d pages, the last %+v", len(pages), pages[len(pages)-1].Issues)
	}
	if _, _, ok := cursor.ParseDateID(pages[16].NextCursor); !ok || !strings.Contains(decodeTok(pages[16].NextCursor), cursor.NoDate) {
		t.Errorf("the cursor among the undated is %q", decodeTok(pages[16].NextCursor))
	}
}

func decodeTok(tok string) string {
	p, _ := cursor.Decode(tok, 2)
	return strings.Join(p, "|")
}
