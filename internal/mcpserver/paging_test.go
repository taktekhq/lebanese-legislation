package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Cursor paging (version 2.0.0): walking every page by next_cursor gives
// the same rows in the same order as one unpaged query in the 1.x SQL, with
// no row twice and none missing, in both orders and with every filter.

// walkServer is a server over one transaction on the fixture, seeded with
// enough rows to page through: 340 more texts in five kinds (two of them
// "Constitution") on 37 dates, some with a Gazette date and some with none;
// 90 Gazette issues on 13 dates, some undated; a text of 250 articles; and
// 160 edges into law 900002 and its articles from dated texts, undated texts
// and rulings. The transaction is rolled back after the test, so the
// fixture the other tests count is untouched.
func walkServer(t *testing.T) (*Server, pgx.Tx) {
	t.Helper()
	if testPool == nil {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	for _, sql := range []string{
		`INSERT INTO legislation_kind (name_ar, name_en) VALUES ('دستور', 'Constitution'), ('الدستور', 'Constitution')`,
		`INSERT INTO legislation (id, kind, number, issued_on, title, status, gazette_date)
		 SELECT 900000 + i, (ARRAY['قانون', 'مرسوم', 'قرار', 'دستور', 'الدستور'])[1 + i % 5], i::text,
		        CASE WHEN i % 17 <> 0 THEN date '1850-01-01' + (i % 37) * 30 END, 'نص ' || i, 'in_force',
		        CASE WHEN i % 3 = 0 AND i % 17 <> 0 THEN date '1850-01-01' + (i % 11) * 45 END
		 FROM generate_series(1, 340) i`,
		`INSERT INTO gazette_issue (id, number, published_on)
		 SELECT 800000 + i, i::text, CASE WHEN i % 10 <> 0 THEN date '1860-01-01' + (i % 13) * 7 END
		 FROM generate_series(1, 90) i`,
		`INSERT INTO legislation_article (law_id, position, number, text_ar)
		 SELECT 900001, i, i::text, 'نص المادة ' || i FROM generate_series(1, 250) i`,
		// from_key is unique per edge, so it names the edge in an answer
		`INSERT INTO graph_edge (id, from_key, to_key, relation, source, status, from_type, from_id, to_law_id, to_article)
		 SELECT 700000 + i,
		        CASE WHEN i % 7 = 0 THEN 'ruling/' || i ELSE 'law/' || (900000 + i % 51) || '/art/' || i END,
		        CASE WHEN i % 4 = 0 THEN 'law/900002' ELSE 'law/900002/art/' || (i % 9) END,
		        CASE WHEN i % 3 = 0 THEN 'amend' ELSE 'cites' END, 'model', 'linked',
		        CASE WHEN i % 7 = 0 THEN 'ruling' ELSE 'legislation' END,
		        CASE WHEN i % 7 = 0 THEN i ELSE 900000 + i % 51 END,
		        900002, CASE WHEN i % 4 = 0 THEN NULL ELSE (i % 9)::text END
		 FROM generate_series(1, 160) i`,
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatalf("%v: %s", err, sql)
		}
	}
	return newServer(tx, Config{RateLimitPerMinute: -1}), tx
}

// truth runs one unpaged query and returns its first column as strings.
func truth(t *testing.T, tx pgx.Tx, sql string, params ...any) []string {
	t.Helper()
	rows, err := tx.Query(context.Background(), sql, params...)
	if err != nil {
		t.Fatalf("%v: %s", err, sql)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(v))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// walk calls tool page by page, following next_cursor, and returns what
// field gives for every row of list, in order. Every page but the last is
// full, every page carries the same total, and only the last has no
// next_cursor.
func walk(t *testing.T, s *Server, tool string, base map[string]any, list, field string, limit int) (got []string, total int) {
	t.Helper()
	tok := ""
	for page := 0; ; page++ {
		a := map[string]any{"limit": limit}
		for k, v := range base {
			a[k] = v
		}
		if tok != "" {
			a["cursor"] = tok
		}
		r := call(t, s, tool, a)
		if r.isError {
			t.Fatalf("%s %v page %d: %v", tool, a, page, r.result)
		}
		if r.result["offset"] != nil || r.result["next_offset"] != nil {
			t.Fatalf("%s still answers with offset: %v", tool, r.result)
		}
		rows := r.result[list].([]any)
		if page == 0 {
			total = num(r.result["total"])
		} else if num(r.result["total"]) != total {
			t.Fatalf("%s %v page %d: total %v, was %d", tool, base, page, r.result["total"], total)
		}
		for _, x := range rows {
			v := x.(map[string]any)[field]
			if f, isNum := v.(float64); isNum {
				v = int64(f) // 2000200346, not 2.000200346e+09
			}
			got = append(got, fmt.Sprint(v))
		}
		next, has := r.result["next_cursor"].(string)
		if !has {
			if len(rows) > limit || (len(rows) == 0 && page > 0) {
				t.Fatalf("%s %v last page %d has %d rows", tool, base, page, len(rows))
			}
			return got, total
		}
		if len(rows) != limit || next == "" {
			t.Fatalf("%s %v page %d: %d rows of %d with next_cursor %q", tool, base, page, len(rows), limit, next)
		}
		if page > 2000 {
			t.Fatalf("%s %v: no end", tool, base)
		}
		tok = next
	}
}

func sameWalk(t *testing.T, what string, got []string, total int, want []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Errorf("%s: %s twice", what, g)
		}
		seen[g] = true
	}
	if !reflect.DeepEqual(got, want) || total != len(want) {
		t.Errorf("%s: walked %d rows (total %d), want %d\n got %v\nwant %v", what, len(got), total, len(want), got, want)
	}
}

func TestWalkListLegislation(t *testing.T) {
	s, tx := walkServer(t)
	const lawDateSQL = "coalesce(l.gazette_date, l.issued_on)"
	cases := []struct {
		args  map[string]any
		where string
		limit []int
	}{
		{map[string]any{}, "", []int{7, 100}},
		{map[string]any{"since": "1850-03", "until": "1851-06-15"}, " AND " + lawDateSQL + " BETWEEN '1850-03-01' AND '1851-06-15'", []int{1, 9}},
		{map[string]any{"kind": "Law"}, " AND (l.kind = 'Law' OR lower(k.name_en) = lower('Law'))", []int{1, 50}},
		{map[string]any{"kind": "مرسوم", "since": "1850"}, " AND (l.kind = 'مرسوم' OR lower(k.name_en) = lower('مرسوم')) AND " + lawDateSQL + " >= '1850-01-01'", []int{4}},
		{map[string]any{"kind": "constitution"}, " AND (l.kind = 'constitution' OR lower(k.name_en) = lower('constitution'))", []int{1, 6}},
		{map[string]any{"kind": "Treaty"}, " AND (l.kind = 'Treaty' OR lower(k.name_en) = lower('Treaty'))", []int{5}},
	}
	for _, c := range cases {
		for _, order := range []string{"newest", "oldest"} {
			dir := "DESC"
			if order == "oldest" {
				dir = "ASC"
			}
			// the 1.x query, without LIMIT and OFFSET
			want := truth(t, tx, `SELECT l.id FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
				WHERE `+lawDateSQL+` IS NOT NULL`+c.where+` ORDER BY `+lawDateSQL+` `+dir+`, l.id `+dir)
			for _, limit := range c.limit {
				a := map[string]any{"order": order}
				for k, v := range c.args {
					a[k] = v
				}
				got, total := walk(t, s, "list_legislation", a, "laws", "id", limit)
				sameWalk(t, fmt.Sprintf("list_legislation %v limit %d", a, limit), got, total, want)
			}
			if c.args["kind"] == "constitution" && len(want) < 100 {
				t.Errorf("the two kinds named Constitution: %d texts", len(want))
			}
		}
	}
}

func TestWalkListGazetteIssues(t *testing.T) {
	s, tx := walkServer(t)
	for _, c := range []struct {
		args  map[string]any
		where string
	}{
		{map[string]any{}, ""},
		{map[string]any{"since": "1860-01-15", "until": "1860-02"}, " AND g.published_on BETWEEN '1860-01-15' AND '1860-02-29'"},
	} {
		for _, order := range []string{"newest", "oldest"} {
			dir := "DESC"
			if order == "oldest" {
				dir = "ASC"
			}
			want := truth(t, tx, `SELECT g.id FROM gazette_issue g WHERE g.published_on IS NOT NULL`+c.where+` ORDER BY g.published_on `+dir+`, g.id `+dir)
			for _, limit := range []int{1, 4, 100} {
				a := map[string]any{"order": order}
				for k, v := range c.args {
					a[k] = v
				}
				got, total := walk(t, s, "list_gazette_issues", a, "issues", "id", limit)
				sameWalk(t, fmt.Sprintf("list_gazette_issues %v limit %d", a, limit), got, total, want)
			}
		}
	}
}

func TestWalkLawTexts(t *testing.T) {
	s, tx := walkServer(t)
	for _, c := range []struct {
		law   int
		limit []int
	}{{900001, []int{1, 7, 50, 200}}, {400, []int{1, 2, 3}}} {
		want := truth(t, tx, `SELECT number FROM legislation_article WHERE law_id = $1 ORDER BY position`, c.law)
		for _, limit := range c.limit {
			got, total := walk(t, s, "get_law_texts", map[string]any{"law_id": c.law}, "articles", "number", limit)
			sameWalk(t, fmt.Sprintf("get_law_texts %d limit %d", c.law, limit), got, total, want)
		}
	}
}

func TestWalkLinks(t *testing.T) {
	s, tx := walkServer(t)
	const date = "coalesce(l.gazette_date, l.issued_on)"
	for _, c := range []struct {
		args map[string]any
		cond string
	}{
		{map[string]any{"key": "law/900002"}, "(ge.to_key = 'law/900002' OR ge.to_law_id = 900002)"},
		{map[string]any{"key": "law/900002", "scope": "law"}, "ge.to_key = 'law/900002'"},
		{map[string]any{"key": "law/900002", "relation": "amend"}, "(ge.to_key = 'law/900002' OR ge.to_law_id = 900002) AND ge.relation = 'amend'"},
		{map[string]any{"key": "law/900002/art/3"}, "ge.to_key = 'law/900002/art/3'"},
		{map[string]any{"key": "law/900014", "direction": "from"}, "(ge.from_key = 'law/900014' OR (ge.from_type = 'legislation' AND ge.from_id = 900014))"},
		{map[string]any{"key": "law/430"}, "(ge.to_key = 'law/430' OR ge.to_law_id = 430)"},
	} {
		for _, order := range []string{"newest", "oldest"} {
			dir := "DESC"
			if order == "oldest" {
				dir = "ASC"
			}
			// the 1.x order: texts with a date first, then by date and edge id
			want := truth(t, tx, `SELECT ge.from_key FROM graph_edge ge
				LEFT JOIN (legislation l JOIN legislation_kind k ON k.name_ar = l.kind) ON ge.from_type = 'legislation' AND l.id = ge.from_id
				WHERE `+c.cond+` ORDER BY (`+date+` IS NULL), `+date+` `+dir+`, ge.id `+dir)
			if len(want) < 2 && c.args["key"] != "law/430" {
				t.Fatalf("%v: %d edges, too few to page", c.args, len(want))
			}
			for _, limit := range []int{1, 6, 200} {
				a := map[string]any{"order": order}
				for k, v := range c.args {
					a[k] = v
				}
				got, total := walk(t, s, "get_links", a, "links", "from_key", limit)
				sameWalk(t, fmt.Sprintf("get_links %v limit %d", a, limit), got, total, want)
			}
		}
	}
	// the walk crosses from dated texts to undated ones (rulings, texts with
	// no date) in both orders
	r := call(t, s, "get_links", map[string]any{"key": "law/900002", "limit": 200}).result
	links := list(r["links"])
	if links[0]["date"] == nil || links[len(links)-1]["date"] != nil {
		t.Errorf("dated first, undated last: %v ... %v", links[0], links[len(links)-1])
	}
}

// A cursor is a condition on the sort key, so a walk started before new
// texts arrive goes on where it was: no text twice, none skipped. With
// offset, a text added ahead of the reader pushed the next page back by one.
func TestCursorStableWhileTextsAreAdded(t *testing.T) {
	s, tx := walkServer(t)
	first := call(t, s, "list_legislation", map[string]any{"since": "1850", "until": "1852", "limit": 10}).result
	shown := lawIDs(first)
	if _, err := tx.Exec(context.Background(), `INSERT INTO legislation (id, kind, number, issued_on, title, status) VALUES (950001, 'قانون', '1', '1852-12-31', 'نص جديد', 'in_force')`); err != nil {
		t.Fatal(err)
	}
	second := call(t, s, "list_legislation", map[string]any{"since": "1850", "until": "1852", "limit": 10, "cursor": first["next_cursor"]}).result
	want := truth(t, tx, `SELECT l.id FROM legislation l WHERE coalesce(l.gazette_date, l.issued_on) BETWEEN '1850-01-01' AND '1852-12-31'
		ORDER BY coalesce(l.gazette_date, l.issued_on) DESC, l.id DESC LIMIT 21`)
	got := fmt.Sprint(append(shown, lawIDs(second)...))
	if want[0] != "950001" || got != "["+strings.Join(want[1:], " ")+"]" {
		t.Errorf("after an insert: %s, want the rows after %v", got, want[:1])
	}
	if num(second["total"]) != num(first["total"])+1 {
		t.Errorf("total counts the new text: %v then %v", first["total"], second["total"])
	}
}

// offset was removed in 2.0.0; a call that passes it is told what to do, and
// a cursor this server did not make is refused. Neither reaches the database.
func TestOffsetRemovedAndBadCursors(t *testing.T) {
	s := noDBServer(Config{})
	const head = "offset was removed in version 2.0.0 (6 Nov 2026). Pass cursor, the next_cursor of the previous answer. "
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"get_law_texts", map[string]any{"law_id": 1, "offset": 50}, head + "For one article use get_article."},
		{"get_law_texts", map[string]any{"law_id": 1, "offset": 0}, head + "For one article use get_article."},
		{"get_links", map[string]any{"key": "law/1", "offset": 50}, head + "To narrow the links use relation."},
		{"list_legislation", map[string]any{"offset": 100}, head + "To jump to a period use since and until; for one text use get_law."},
		{"list_legislation", map[string]any{"offset": "100", "cursor": "x"}, head + "To jump to a period use since and until; for one text use get_law."},
		{"list_gazette_issues", map[string]any{"offset": 2}, head + "To jump to a period use since and until; for one issue use get_gazette_issue."},
	} {
		r := call(t, s, c.name, c.args)
		if !r.isError || r.result["error"] != c.want {
			t.Errorf("%s %v: %v", c.name, c.args, r.result)
		}
	}
	links := linkKey{true, "2025-01-02", 7}.token()
	for _, c := range []struct {
		name   string
		args   map[string]any
		cursor any
	}{
		{"get_law_texts", map[string]any{"law_id": 1}, "nope"},
		{"get_law_texts", map[string]any{"law_id": 1}, 50},
		{"get_law_texts", map[string]any{"law_id": 1}, cursor.Int(1 << 40)},
		{"get_law_texts", map[string]any{"law_id": 1}, cursor.DateID("2025-01-01", 5)},
		{"get_links", map[string]any{"key": "law/1"}, cursor.DateID("2025-01-01", 5)},
		{"get_links", map[string]any{"key": "law/1"}, cursor.Encode("2", "2025-01-01", "5")},
		{"get_links", map[string]any{"key": "law/1"}, cursor.Encode("1", "2025-13-01", "5")},
		{"list_legislation", map[string]any{}, links},
		{"list_legislation", map[string]any{}, cursor.Int(5)},
		{"list_legislation", map[string]any{}, cursor.DateID("2025-01-01", -1<<40)},
		{"list_legislation", map[string]any{}, map[string]any{"date": "2025-01-01"}},
		{"list_gazette_issues", map[string]any{}, "djJ8MjAyNS0wMS0wMXw1"}, // a "v2" token
		{"list_gazette_issues", map[string]any{}, cursor.Encode("2025-1-1", "5")},
	} {
		a := map[string]any{"cursor": c.cursor}
		for k, v := range c.args {
			a[k] = v
		}
		r := call(t, s, c.name, a)
		if !r.isError || r.result["error"] != badCursor {
			t.Errorf("%s %v: %v", c.name, a, r.result)
		}
	}
	if badCursor != "cursor is not one this server made; pass next_cursor unchanged" {
		t.Errorf("bad cursor text: %s", badCursor)
	}
}

// An empty or null cursor is the first page, as omitting it is.
func TestEmptyCursorIsTheFirstPage(t *testing.T) {
	s := dbServer(t)
	want := mustMarshal(call(t, s, "list_gazette_issues", map[string]any{"limit": 2}).result)
	for _, c := range []any{"", nil} {
		if got := mustMarshal(call(t, s, "list_gazette_issues", map[string]any{"limit": 2, "cursor": c}).result); got != want {
			t.Errorf("cursor %#v: %s, want %s", c, got, want)
		}
	}
}

// Agents learn the change from the answers they already read: initialize
// says 2.0.0 and how pages work, tools/list has cursor and no offset, and
// the MCP Registry entry has the same version.
func TestCursorContractIsAdvertised(t *testing.T) {
	s := noDBServer(Config{})
	r := rpc(t, s, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})["result"].(map[string]any)
	if v := r["serverInfo"].(map[string]any)["version"]; v != "2.0.0" || ServerVersion != "2.0.0" {
		t.Errorf("version %v", v)
	}
	if in := r["instructions"].(string); !strings.Contains(in, "next_cursor as cursor") || !strings.Contains(in, "offset was removed in version 2.0.0") {
		t.Errorf("instructions: %s", in)
	}
	if strings.Contains(string(toolsJSON), "offset") {
		t.Error("tools.json still mentions offset")
	}
	var defs []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"inputSchema"`
	}
	if err := json.Unmarshal(toolsJSON, &defs); err != nil {
		t.Fatal(err)
	}
	paged := map[string]bool{"get_law_texts": true, "get_links": true, "list_legislation": true, "list_gazette_issues": true}
	for _, d := range defs {
		c, has := d.InputSchema.Properties["cursor"]
		if has != paged[d.Name] {
			t.Errorf("%s: cursor %v", d.Name, has)
			continue
		}
		if has && (c["type"] != "string" || c["description"] != "next_cursor of the previous answer; omit for the first page" || !strings.Contains(d.Description, "next_cursor")) {
			t.Errorf("%s: cursor %v, description %q", d.Name, c, d.Description)
		}
	}
	b, err := os.ReadFile("../../server.json")
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &reg); err != nil || reg.Version != ServerVersion {
		t.Errorf("server.json version %q, initialize says %q (%v)", reg.Version, ServerVersion, err)
	}
}
