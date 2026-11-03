package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMarks(t *testing.T) {
	const title = `title="قراءتان مختلفتان: راجع ملف PDF الأصلي"`
	cases := map[string]string{
		"نص <عادي>": "نص &lt;عادي&gt;",
		"الإمضاء: \uE000إميل لحود\uE001جوزاف عون\uE002": `الإمضاء: <mark class="unsure" ` + title + `>إميل لحود<span class="unsure__or"> / </span>جوزاف عون</mark>`,
		"\uE000\uE001|\uE002 ١": `<mark class="unsure" ` + title + `>…<span class="unsure__or"> / </span>|</mark> ١`,
	}
	for in, want := range cases {
		if got := string(marks(in)); got != want {
			t.Errorf("marks(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScanRedirect(t *testing.T) {
	dsn, _ := weakFixtureDB(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	const sourceURL = "https://saderlex.com/gazette/pdf/14588"
	if _, err := pool.Exec(ctx, `INSERT INTO legislation_scan (law_id, source, source_id, source_url, pages)
 VALUES (101, 'saderlex', 14588, $1, 1)`, sourceURL); err != nil {
		t.Fatal(err)
	}
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.db = pool
	for _, path := range []string{"/scan/101/page-1.png", "/scan/101/source.pdf", "/scan/101/other/nested.file"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != sourceURL {
			t.Errorf("GET %s: status %d, Location %q; want 301 to %s", path, rec.Code, rec.Header().Get("Location"), sourceURL)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/scan/102/page-1.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing scan: status %d, want 404", rec.Code)
	}
}
