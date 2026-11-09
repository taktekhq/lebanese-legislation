package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/wordsearch"
)

// buildIndex writes the fixture's word lists (as the image build does) and
// returns the file's path.
func buildIndex(t *testing.T, db wordsearch.Querier) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "search-index")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := wordsearch.BuildIndex(context.Background(), db, f); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// searchCalls are search_legislation calls over the fixture: the questions
// of the other tests, every kind, years, numbers, Latin and French, and the
// first words of every title and article.
func searchCalls(t *testing.T) []map[string]any {
	t.Helper()
	qs := []string{"قانون", "عقد", "عقد العمل", "قانون العقوبات المادة 37", "المحكمة", "الدستور", "penal code", "labour law",
		"law 44 of 2015", "قانون رقم 44 تاريخ 2015", "contrat de travail", "employment", "الجنسية", "والخامسة", "في", "في على من",
		"المادة", "اللبناني", "اللبنانية", "١٩٩٩", "2015", "44", "430", "obligations", "Code pénal", "الجريدة الرسمية عدد 39"}
	ctx := context.Background()
	rows, err := testPool.Query(ctx, `SELECT split_part(title, ' ', 1) || ' ' || split_part(title, ' ', 2) FROM legislation
		UNION ALL SELECT split_part(text_ar, ' ', 2) FROM legislation_article WHERE text_ar <> ''
		UNION ALL SELECT split_part(title, ' ', 1) FROM ruling
		UNION ALL SELECT split_part(title_en, ' ', 2) FROM legislation_title_en`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			t.Fatal(err)
		}
		if q = strings.TrimSpace(q); q != "" {
			qs = append(qs, q)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var calls []map[string]any
	for i, q := range qs {
		calls = append(calls, map[string]any{"query": q})
		kind := []string{"laws", "articles", "rulings", "gazette"}[i%4]
		calls = append(calls, map[string]any{"query": q, "kind": kind, "limit": 1 + i%20})
		if i%3 == 0 {
			calls = append(calls, map[string]any{"query": q, "year": []int{2015, 1983, 2000}[i%3]})
		}
	}
	return calls
}

// With the word lists, search_legislation answers as without them, byte
// for byte.
func TestSearchIndexAgrees(t *testing.T) {
	plain := dbServer(t)
	ctx := context.Background()
	path := buildIndex(t, testPool)
	s := newServer(testPool, Config{RateLimitPerMinute: -1, SearchIndex: path})
	if !s.WaitSearchIndex(ctx) {
		t.Fatal("the index built from this database did not load")
	}
	if k := s.known(ctx); k == nil || k.Index == nil {
		t.Fatalf("known %v: no index", k)
	}
	calls := searchCalls(t)
	for _, args := range calls {
		body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "search_legislation", "arguments": args}}
		got, want := post(s, body, nil).Body.String(), post(plain, body, nil).Body.String()
		if got != want {
			t.Errorf("%v: with the index\n%.400s\nwithout\n%.400s", args, got, want)
		}
		if strings.Contains(got, `"error"`) {
			t.Errorf("%v: %.300s", args, got)
		}
	}
	if len(calls) < 100 {
		t.Errorf("only %d calls", len(calls))
	}
	if n := s.known(ctx).Index.Fallbacks(); n*4 > int64(len(calls)) {
		t.Errorf("the index left %d of %d searches to the SQL", n, len(calls))
	}
}

// Without its file, or with a file from another database, the server
// searches with the SQL and answers the same.
func TestSearchIndexFallsBack(t *testing.T) {
	plain := dbServer(t)
	ctx := context.Background()
	// a file from another database: one more law
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO legislation (id, kind, title, status) SELECT max(id) + 1, min(kind), 'قانون آخر', 'in_force' FROM legislation`); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	other := buildIndex(t, tx)
	tx.Rollback(ctx)
	for _, path := range []string{filepath.Join(t.TempDir(), "none"), other} {
		s := newServer(testPool, Config{RateLimitPerMinute: -1, SearchIndex: path})
		if s.WaitSearchIndex(ctx) {
			t.Errorf("%s: loaded", path)
		}
		for _, q := range []string{"عقد العمل", "المحكمة", "penal code"} {
			body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "search_legislation", "arguments": map[string]any{"query": q}}}
			if got, want := post(s, body, nil).Body.String(), post(plain, body, nil).Body.String(); got != want || strings.Contains(got, `"error"`) {
				t.Errorf("%s %q: %.300s\nwant %.300s", path, q, got, want)
			}
		}
		if k := s.known(ctx); k == nil || k.Index != nil {
			t.Errorf("%s: known %v", path, k)
		}
	}
}
