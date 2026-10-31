package site

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRulingFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "0005"), 0o755)
	os.WriteFile(filepath.Join(dir, "0005", "5390.pdf"), []byte("%PDF-1.6 test"), 0o644)
	os.MkdirAll(filepath.Join(dir, "0005", "5391.pdf"), 0o755)
	os.WriteFile(filepath.Join(dir, "secret.pdf"), []byte("%PDF-1.6 secret"), 0o644)
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.SetRulingFiles(dir)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	rec := get("/ruling-files/5390.pdf")
	if rec.Code != 200 || rec.Body.String() != "%PDF-1.6 test" {
		t.Fatalf("5390: status %d, body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content type %q, want application/pdf", ct)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=86400" || rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("headers: %v", rec.Header())
	}
	for _, path := range []string{
		"/ruling-files/5391.pdf",
		"/ruling-files/9999.pdf",
		"/ruling-files/05390.pdf",
		"/ruling-files/5390.html",
		"/ruling-files/5390",
		"/ruling-files/..%2Fsecret.pdf",
		"/ruling-files/%2e%2e%2fsecret.pdf",
		"/ruling-files/0005%2F5390.pdf",
		"/ruling-files/..%5Csecret.pdf",
		"/ruling-files/5390.pdf%00.pdf",
		"/ruling-files/99999999999999999.pdf",
	} {
		if rec := get(path); rec.Code != 404 {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
	s.SetRulingFiles("")
	if rec := get("/ruling-files/5390.pdf"); rec.Code != 404 {
		t.Errorf("with no folder set: status %d, want 404", rec.Code)
	}
}

func rulingPage(t *testing.T, s *Server, lang string, j judgment, paras []string) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/ruling/5390/", nil)
	r = r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
	d := Doc{Crumb: "Rulings", CrumbURL: "/rulings/", Title: "مجلس شورى الدولة قرار رقم 271/2015", Paras: paras}
	if len(paras) == 0 {
		d.Note = s.trOr(r, "The Centre holds this judgment as a file only; read it at the Lebanese University.", "ruling.file_only")
	}
	s.judgmentDoc(r, &d, 5390, j)
	rec := httptest.NewRecorder()
	s.render(rec, r, "doc", 200, Page{Lang: "ar", Title: "t", Path: "/ruling/5390/", Data: d})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestRulingPageWithReading(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	text1, text3 := "مجلس شورى الدولة\nقرار رقم: ٢٧١ <b>x</b>", "بما أن الجهة المستدعية"
	j := judgment{HasFile: true, Bytes: 178874, Model: "gemini-3.8-flash-low", Pages: []DocPage{
		{N: 1, Paras: paras(&text1)}, {N: 3, Paras: paras(&text3)}}}

	en := rulingPage(t, s, "en", j, nil)
	for _, want := range []string{
		`href="https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=5390&amp;type=list"`, "Full judgment (scan, PDF)", "175 KB",
		"Machine reading of the scanned judgment. It may contain errors; the scan is the reference.",
		`id="page-1"`, `id="page-3"`, "Page 3", "قرار رقم: ٢٧١ &lt;b&gt;x&lt;/b&gt;",
	} {
		if !strings.Contains(en, want) {
			t.Errorf("English page lacks %q", want)
		}
	}
	if strings.Index(en, `id="page-1"`) > strings.Index(en, `id="page-3"`) {
		t.Error("pages are out of order")
	}
	if strings.Contains(en, "file only") || strings.Contains(en, "<b>x</b>") {
		t.Error("the file-only note stayed, or the reading was not escaped")
	}
	if strings.Contains(strings.ToLower(en), "gemini") || strings.Contains(en, "/ruling-files/") {
		t.Error("the page names the model or links our copy instead of the source")
	}
	if !strings.Contains(en, `<div class="reading" lang="ar" dir="rtl">`) {
		t.Error("the reading is not marked as right-to-left Arabic")
	}
	ar := rulingPage(t, s, "ar", j, nil)
	for _, want := range []string{"الحكم كاملاً (نسخة ممسوحة، PDF)", "قراءة آلية للحكم الممسوح ضوئياً. قد تتضمن أخطاء؛ والنسخة الممسوحة هي المرجع.", "صفحة 3"} {
		if !strings.Contains(ar, want) {
			t.Errorf("Arabic page lacks %q", want)
		}
	}
}

func TestRulingPageFileOnly(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}

	got := rulingPage(t, s, "en", judgment{HasFile: true, Bytes: 5 << 20}, nil)
	if !strings.Contains(got, `href="https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=5390&amp;type=list"`) || !strings.Contains(got, "5.0 MB") ||
		!strings.Contains(got, "its text has not been read yet") || strings.Contains(got, "Machine reading") {
		t.Error("a ruling with a scan and no reading should link the scan and say it is unread")
	}

	got = rulingPage(t, s, "en", judgment{}, nil)
	if !strings.Contains(got, "as a file only; read it at the Lebanese University") || strings.Contains(got, "RulingFile.aspx") {
		t.Error("a ruling with nothing held should keep the file-only note and show no link")
	}

	text := "ملخص الحكم"
	j := judgment{HasFile: true, Bytes: 100, Model: "gemini-x", Pages: []DocPage{{N: 1, Paras: []string{"نص الصفحة"}}}}
	got = rulingPage(t, s, "en", j, paras(&text))
	if i := strings.Index(got, "ملخص الحكم"); i < 0 || i > strings.Index(got, "نص الصفحة") {
		t.Error("the summary should come before the reading")
	}
}

func TestReadingFirstText(t *testing.T) {
	var none *DocReading
	if none.firstText() != "" {
		t.Error("nil reading")
	}
	r := &DocReading{Pages: []DocPage{{N: 1}, {N: 2, Paras: []string{"أول\nسطر", "ثان"}}}}
	if got := r.firstText(); got != "أول سطر ثان" {
		t.Errorf("firstText = %q", got)
	}
}

func TestFirstTextSpellsOutDisputes(t *testing.T) {
	r := &DocReading{Pages: []DocPage{{Paras: []string{"المستأنف \uE000جوزيت\uE001جوزفين\uE002 مقصود"}}}}
	if got := r.firstText(); got != "المستأنف ⟦جوزيت | جوزفين⟧ مقصود" {
		t.Errorf("firstText = %q", got)
	}
}

func TestRulingPageExplainsHighlights(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	marked := "المستأنف \uE000جوزيت\uE001جوزفين\uE002 مقصود"
	plain := "نص بلا خلاف"
	with := judgment{HasFile: true, Bytes: 100, Pages: []DocPage{{N: 1, Text: marked, Paras: paras(&marked)}}}
	without := judgment{HasFile: true, Bytes: 100, Pages: []DocPage{{N: 1, Text: plain, Paras: paras(&plain)}}}
	en := rulingPage(t, s, "en", with, nil)
	if !strings.Contains(en, "Highlighted words, like") || !strings.Contains(en, "neither is chosen") ||
		strings.Count(en, `<mark class="unsure"`) != 2 {
		t.Error("a reading with highlights should explain them with a sample")
	}
	if ar := rulingPage(t, s, "ar", with, nil); !strings.Contains(ar, "الكلمات المظلّلة، مثل") {
		t.Error("the Arabic page lacks the explanation")
	}
	if got := rulingPage(t, s, "en", without, nil); strings.Contains(got, "Highlighted words") {
		t.Error("a reading without highlights should not explain them")
	}
}

func TestRulingPageWarnsOnLowConfidencePages(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	one, two := "سطر بخط اليد", "نص مطبوع"
	j := judgment{HasFile: true, Bytes: 100, Pages: []DocPage{
		{N: 1, Text: one, Paras: paras(&one), LowConfidence: true},
		{N: 2, Text: two, Paras: paras(&two)},
	}}
	en := rulingPage(t, s, "en", j, nil)
	if strings.Count(en, "Low-confidence reading") != 1 || strings.Count(en, "RulingFile.aspx?RuliID=5390") != 1 {
		t.Errorf("one warning for the one low-confidence page, and the scan linked once (its button):\n%s", en)
	}
	if ar := rulingPage(t, s, "ar", j, nil); strings.Count(ar, "قراءة منخفضة الثقة") != 1 {
		t.Error("the Arabic page lacks the warning")
	}
}

func TestFirstTextPrefersCheckedPages(t *testing.T) {
	r := &DocReading{Pages: []DocPage{{N: 1, Paras: []string{"قراءة أولى"}, LowConfidence: true}, {N: 2, Paras: []string{"نص مراجع"}}}}
	if got := r.firstText(); got != "نص مراجع" {
		t.Errorf("firstText = %q", got)
	}
	only := &DocReading{Pages: []DocPage{{N: 1, Paras: []string{"قراءة أولى"}, LowConfidence: true}}}
	if got := only.firstText(); got != "قراءة أولى" {
		t.Errorf("only low confidence: firstText = %q", got)
	}
}

func TestUndefinedColumn(t *testing.T) {
	if !undefinedColumn(fmt.Errorf("query: %w", &pgconn.PgError{Code: "42703"})) {
		t.Error("42703 is an undefined column")
	}
	if undefinedColumn(&pgconn.PgError{Code: "42P01"}) || undefinedColumn(nil) {
		t.Error("only 42703")
	}
}
