package site

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("LEBLEG_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}

	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("no local Postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("no local Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	s, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func getJSON(t *testing.T, s *Server, path string, v any) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	resp := rec.Result()
	if v != nil && resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("GET %s: decode: %v", path, err)
		}
	}
	return resp
}

func TestAPIRoot(t *testing.T) {
	s := testServer(t)
	var out map[string]any
	resp := getJSON(t, s, "/api/v1/", &out)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if out["name"] == nil || out["mcp"] == nil {
		t.Errorf("missing fields: %v", out)
	}
}

func TestAPISearch(t *testing.T) {
	s := testServer(t)
	var out apiSearchResult
	resp := getJSON(t, s, "/api/v1/search/?q="+"%D9%85%D9%88%D8%AC%D8%A8%D8%A7%D8%AA", &out)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if out.LawCount == 0 && out.ArtCount == 0 {
		t.Errorf("expected some hits for a common legal word, got none: %+v", out)
	}
	for _, l := range out.Laws {
		if l.URL == "" || l.Title == "" {
			t.Errorf("law summary missing fields: %+v", l)
		}
	}

	var empty apiSearchResult
	resp = getJSON(t, s, "/api/v1/search/?q=", &empty)
	if resp.StatusCode != 200 || empty.LawCount != 0 {
		t.Errorf("empty query: status %d, %+v", resp.StatusCode, empty)
	}

	var lawsOnly apiSearchResult
	getJSON(t, s, "/api/v1/search/?q=%D9%82%D8%A7%D9%86%D9%88%D9%86&type=laws", &lawsOnly)
	if len(lawsOnly.Rulings) != 0 || len(lawsOnly.Articles) != 0 || len(lawsOnly.Gazette) != 0 {
		t.Errorf("type=laws returned other categories: %+v", lawsOnly)
	}
}

func TestAPILawAndArticle(t *testing.T) {
	s := testServer(t)
	var search apiSearchResult
	getJSON(t, s, "/api/v1/search/?q=%D9%85%D9%88%D8%AC%D8%A8%D8%A7%D8%AA&type=articles&limit=5", &search)
	if len(search.Articles) == 0 {
		t.Skip("no article hits to follow in this database")
	}
	hit := search.Articles[0]

	var law apiLawDetail
	resp := getJSON(t, s, "/api/v1/law/"+strconv.Itoa(hit.Law.ID)+"/", &law)
	if resp.StatusCode != 200 {
		t.Fatalf("law %d: status %d", hit.Law.ID, resp.StatusCode)
	}
	if law.ID != hit.Law.ID || law.Title == "" || law.URL == "" {
		t.Errorf("law detail missing fields: %+v", law)
	}
	if len(law.Articles) == 0 {
		t.Errorf("law %d has article_count %d but no articles listed", law.ID, law.ArticleCount)
	}

	art := law.Articles[0]
	var article apiArticleDetail
	resp = getJSON(t, s, "/api/v1/law/"+strconv.Itoa(hit.Law.ID)+"/article/"+art.Number+"/", &article)
	if resp.StatusCode != 200 {
		t.Fatalf("article %s: status %d", art.Number, resp.StatusCode)
	}
	if article.TextAr == "" {
		t.Errorf("article %s has empty text_ar", art.Number)
	}
	if article.NotLegalAdvice == "" {
		t.Errorf("article missing the not-legal-advice line")
	}

	resp = getJSON(t, s, "/api/v1/law/999999999/", &law)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown law: status %d, want 404", resp.StatusCode)
	}
}

func TestAPIRuling(t *testing.T) {
	s := testServer(t)
	var search apiSearchResult
	getJSON(t, s, "/api/v1/search/?q=%D9%85%D8%AD%D9%83%D9%85%D8%A9&type=rulings&limit=1", &search)
	if len(search.Rulings) == 0 {
		t.Skip("no ruling hits in this database")
	}
	var ruling apiRulingDetail
	resp := getJSON(t, s, "/api/v1/ruling/"+strconv.Itoa(search.Rulings[0].ID)+"/", &ruling)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ruling.Title == "" || ruling.URL == "" || ruling.NotLegalAdvice == "" {
		t.Errorf("ruling detail missing fields: %+v", ruling)
	}

	resp = getJSON(t, s, "/api/v1/ruling/999999999/", &ruling)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown ruling: status %d, want 404", resp.StatusCode)
	}
}

func TestAPICacheHeaders(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("GET", "/api/v1/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Errorf("expected a Cache-Control header on a 200 API response")
	}
}

func TestAPIArticleInForceWording(t *testing.T) {
	s := testServer(t)
	var article apiArticleDetail
	resp := getJSON(t, s, "/api/v1/law/244483/article/73/", &article)
	if resp.StatusCode == http.StatusNotFound {
		t.Skip("law 244483 not in this database")
	}
	if article.CentreTextAr == nil {
		t.Skip("article 73 has no newer version in this database")
	}
	if article.TextAr == *article.CentreTextAr {
		t.Errorf("text_ar is still the Centre's wording")
	}
	if !strings.Contains(article.TextAr, "في جميع مراحل التحقيق") {
		t.Errorf("text_ar is not the 2023 wording: %.80s", article.TextAr)
	}
	if article.TextFr != nil {
		t.Errorf("text_fr should move to centre_text_fr once the wording changed")
	}
}

func TestRulingJudgmentSearch(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()

	var ro string
	if s.db.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&ro) == nil && ro == "on" {
		t.Skip("read-only database: this test writes; run it on a built database of your own")
	}
	const word = "خرنوبيتشكو"
	var a, b int
	var titleB string
	if _, err := s.db.Exec(ctx, `SELECT 1 FROM ruling_page LIMIT 1`); err != nil {
		t.Skipf("this database predates ruling_page (rebuild it): %v", err)
	}

	rows, err := s.db.Query(ctx, `SELECT id, title FROM ruling r WHERE text_ar IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM ruling_page p WHERE p.ruling_id = r.id)
		AND NOT EXISTS (SELECT 1 FROM ruling_file f WHERE f.ruling_id = r.id) ORDER BY id LIMIT 2`)
	if err != nil {
		t.Skipf("no rulings: %v", err)
	}
	var ids []int
	var titles []string
	for rows.Next() {
		var id int
		var ti string
		rows.Scan(&id, &ti)
		ids, titles = append(ids, id), append(titles, ti)
	}
	rows.Close()
	if len(ids) < 2 {
		t.Skip("fewer than two rulings in this database")
	}
	a, b, titleB = ids[0], ids[1], titles[1]
	clean := func() {
		s.db.Exec(ctx, `DELETE FROM ruling_page WHERE ruling_id IN ($1, $2)`, a, b)
		s.db.Exec(ctx, `DELETE FROM ruling_file WHERE ruling_id IN ($1, $2)`, a, b)
		s.db.Exec(ctx, `UPDATE ruling SET title = $2 WHERE id = $1`, b, titleB)
	}
	clean()
	t.Cleanup(clean)

	for _, q := range []string{
		`INSERT INTO ruling_file (ruling_id, pages, bytes, sha256, fetched_at) VALUES (` + strconv.Itoa(a) + `, 3, 178874, 'x', now())`,
		`INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at) VALUES (` + strconv.Itoa(a) + `, 1, 'قرار رقم ٢٧١ ` + word + `', 'gemini-test', now())`,
		`INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at) VALUES (` + strconv.Itoa(a) + `, 3, 'الصفحة الأخيرة', 'gemini-test', now())`,
		`INSERT INTO ruling_page (ruling_id, n, text_ar, model, read_at) VALUES (` + strconv.Itoa(b) + `, 1, '` + word + ` أيضاً', 'gemini-test', now())`,
	} {
		if _, err := s.db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(ctx, `UPDATE ruling SET title = title || ' ' || $2 WHERE id = $1`, b, word); err != nil {
		t.Fatal(err)
	}

	var out apiSearchResult
	getJSON(t, s, "/api/v1/search/?type=rulings&q="+word, &out)
	if len(out.Rulings) != 2 || out.RulCount != 2 {
		t.Fatalf("hits = %+v (count %d), want both rulings", out.Rulings, out.RulCount)
	}
	if out.Rulings[0].ID != b || out.Rulings[0].JudgmentPage != 0 || out.Rulings[1].ID != a || out.Rulings[1].JudgmentPage != 1 {
		t.Errorf("order or pages wrong: a=%d b=%d, got %+v", a, b, out.Rulings)
	}
	if !strings.Contains(out.Rulings[1].Snippet, word) {
		t.Errorf("the judgment hit's excerpt should come from the page: %q", out.Rulings[1].Snippet)
	}

	out = apiSearchResult{}
	getJSON(t, s, "/api/v1/search/?type=rulings&limit=1&offset=1&q="+word, &out)
	if len(out.Rulings) != 1 || out.Rulings[0].ID != a {
		t.Errorf("offset 1: %+v", out.Rulings)
	}

	out = apiSearchResult{}
	getJSON(t, s, "/api/v1/search/?type=rulings&q=271+"+word, &out)
	if len(out.Rulings) != 1 || out.Rulings[0].ID != a {
		t.Errorf("271 + word: %+v", out.Rulings)
	}

	var d apiRulingDetail
	getJSON(t, s, "/api/v1/ruling/"+strconv.Itoa(a)+"/", &d)
	if d.PDFURL != "https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID="+strconv.Itoa(a)+"&type=list" {
		t.Errorf("pdf_url = %q, want the scan on UL's site", d.PDFURL)
	}
	if d.PageCount != 3 || len(d.Pages) != 2 ||
		d.Pages[0].N != 1 || d.Pages[1].N != 3 || !strings.Contains(d.Pages[0].TextAr, "٢٧١") ||
		d.PagesModel != "gemini-test" || !strings.Contains(d.PagesNote, "may contain errors") {
		t.Errorf("judgment in the detail: %+v", d)
	}
	var plain apiRulingDetail
	getJSON(t, s, "/api/v1/ruling/"+strconv.Itoa(b)+"/", &plain)
	if plain.PDFURL != "" || len(plain.Pages) != 1 {
		t.Errorf("ruling b has pages but no file: %+v", plain)
	}

	req := httptest.NewRequest("GET", "/en/ruling/"+strconv.Itoa(a)+"/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	page := rec.Body.String()
	for _, want := range []string{"Full judgment (scan, PDF)",
		"https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=" + strconv.Itoa(a) + "&amp;type=list",
		"Machine reading of the scanned judgment. It may contain errors", `id="page-3"`} {
		if rec.Code != 200 || !strings.Contains(page, want) {
			t.Errorf("ruling page (status %d) lacks %q", rec.Code, want)
		}
	}

	if strings.Contains(page, "/ruling-files/") || strings.Contains(strings.ToLower(page), "gemini-test") {
		t.Error("the ruling page links our copy or names the model")
	}
}

func TestKindNearAndLength(t *testing.T) {
	s := testServer(t)
	for _, q := range []string{"مرسوم اشتراعي 1306", "مرسوم 1306", "المرسوم الاشتراعي رقم 1306"} {
		var out apiSearchResult
		resp := getJSON(t, s, "/api/v1/search/?type=laws&q="+url.QueryEscape(q), &out)
		if resp.StatusCode != 200 || len(out.Laws) == 0 {
			t.Fatalf("%s: status %d, %d laws", q, resp.StatusCode, len(out.Laws))
		}
		if !strings.Contains(out.Laws[0].Title, "وزارة الخارجية") {
			t.Errorf("%s: first is %q, want the Foreign Ministry's rules", q, out.Laws[0].Title)
		}
	}

	var out apiSearchResult
	getJSON(t, s, "/api/v1/search/?type=laws&q="+url.QueryEscape("ممارسة كتابة العدل القانون رقم 337"), &out)
	if len(out.Laws) == 0 || !strings.Contains(out.Laws[0].Title, "الكتاب العدل") {
		t.Errorf("notaries law 337/1994 not first: %+v", out.Laws)
	}
}

func TestAPISearchRescue(t *testing.T) {
	s := testServer(t)
	for i := 0; i < 100 && s.vocab.Load() == nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	for _, c := range []struct{ q, via string }{
		{"civil marriage", "الزواج المدني"},
		{"al ta3adi fi l bina2", "التعدي"},
		{"kenaan", "كنعان"},
		{"٩٥٩ اصول محاكمات مدنيه", ""},
	} {
		var out apiSearchResult
		resp := getJSON(t, s, "/api/v1/search/?q="+url.QueryEscape(c.q), &out)
		if resp.StatusCode != 200 {
			t.Fatalf("%q: status %d", c.q, resp.StatusCode)
		}
		if c.via == "" {
			continue
		}
		if out.SearchedInstead == "" || !strings.Contains(out.SearchedInstead, strings.Fields(c.via)[0]) {
			t.Errorf("%q: searched_instead = %q, want it to hold %q", c.q, out.SearchedInstead, c.via)
		}
		if out.LawCount+out.ArtCount+out.RulCount == 0 {
			t.Errorf("%q: rescue found nothing: %+v", c.q, out)
		}
		if out.SearchedInsteadKind == "" {
			t.Errorf("%q: searched_instead_kind empty", c.q)
		}
	}

	var plain apiSearchResult
	getJSON(t, s, "/api/v1/search/?q=%D9%85%D9%88%D8%AC%D8%A8%D8%A7%D8%AA", &plain)
	if plain.SearchedInstead != "" {
		t.Errorf("a plain hit set searched_instead = %q", plain.SearchedInstead)
	}
}

func TestAPITitleEn(t *testing.T) {
	s := testServer(t)
	var te string
	var id int
	if err := s.db.QueryRow(context.Background(), `SELECT law_id, title_en FROM legislation_title_en ORDER BY law_id LIMIT 1`).Scan(&id, &te); err != nil {
		t.Skipf("no English titles: %v", err)
	}
	var law apiLawDetail
	getJSON(t, s, "/api/v1/law/"+strconv.Itoa(id)+"/", &law)
	if law.TitleEn == nil || *law.TitleEn != te || law.Title == "" || law.Title == te {
		t.Errorf("law %d: title_en = %v, want %q beside the Arabic title %q", id, law.TitleEn, te, law.Title)
	}
	var out apiSearchResult
	getJSON(t, s, "/api/v1/search/?type=laws&limit=50&q="+url.QueryEscape(te), &out)
	got := false
	for _, l := range out.Laws {
		got = got || l.ID == id
		if l.TitleEn == nil && l.ID == id {
			t.Errorf("hit %d lacks title_en", id)
		}
	}
	if !got {
		t.Errorf("searching its English title %q did not find law %d (%d laws)", te, id, len(out.Laws))
	}
}
