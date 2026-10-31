package site

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/i18n"
)

func ptr[T any](v T) *T { return &v }

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func decree() *Law {
	return &Law{
		LawRow: LawRow{ID: 193806, Kind: "مرسوم", KindEn: ptr("Decree"), Number: ptr("16436"), IssuedOn: day(1964, 5, 25),
			Title: "بتحديد الشهادات والشروط الخاصة لتعيين الموظفين الفنيين في وزارة الصحة العامة", Status: "in_force", Articles: 74},
		GazetteNumber: ptr("45"), GazetteDate: day(1964, 6, 4),
		List: []*Article{{Number: "1"}},
	}
}

var (
	en = i18n.New("en")
	ar = i18n.New("ar")
)

func TestIdentIn(t *testing.T) {
	l := decree()
	for _, c := range []struct {
		b    *i18n.Bundle
		want string
	}{{en, "Decree No. 16436 of 25 May 1964"}, {ar, "مرسوم رقم 16436 تاريخ 25/05/1964"}} {
		if got := identIn(c.b, l.LawRow); got != c.want {
			t.Errorf("%s: got %q, want %q", c.b.Lang, got, c.want)
		}
	}
	noNum := LawRow{Kind: "دستور", KindEn: ptr("Constitution"), IssuedOn: day(1926, 5, 23)}
	if got := identIn(en, noNum); got != "Constitution of 23 May 1926" {
		t.Errorf("no number: %q", got)
	}
	noDate := LawRow{Kind: "قانون", KindEn: ptr("Law"), Number: ptr("44")}
	if got := identIn(en, noDate); got != "Law No. 44" {
		t.Errorf("no date: %q", got)
	}

	noEn := LawRow{Kind: "قرار مشترك", Number: ptr("5"), IssuedOn: day(2000, 1, 1)}
	if got := identIn(en, noEn); got != noEn.Ident() {
		t.Errorf("no English kind: %q", got)
	}
}

func TestTitleIdentifierFirstAndNeverCut(t *testing.T) {
	l := decree()
	for _, b := range []*i18n.Bundle{en, ar} {
		got := lawTitle(b, l)
		ident := identIn(b, l.LawRow)
		if !strings.HasPrefix(got, ident) {
			t.Errorf("%s title %q does not lead with %q", b.Lang, got, ident)
		}
		if n := len([]rune(got)); n > titleMax {
			t.Errorf("%s title is %d characters: %q", b.Lang, n, got)
		}
	}

	long := decree()
	long.Number = ptr("123456789/2015 and 987654321/2016 and more")
	if got := lawTitle(en, long); got != identIn(en, long.LawRow) {
		t.Errorf("long identifier: got %q", got)
	}

	l.TitleEn = ptr("Setting the certificates and conditions for appointing technical staff")
	if got := lawTitle(en, l); !strings.Contains(got, "Setting the certificates") {
		t.Errorf("English subject missing: %q", got)
	}
	if got := lawTitle(ar, l); strings.Contains(got, "Setting") {
		t.Errorf("English subject leaked into the Arabic title: %q", got)
	}

	same := decree()
	same.Title = identIn(en, same.LawRow)
	if got := lawTitle(en, same); got != same.Title {
		t.Errorf("repeated identifier: %q", got)
	}
}

func TestArticleTitle(t *testing.T) {
	l := decree()
	l.Code = ptr("Penal Code")
	if got := artTitle(en, l, "459"); got != "Art. 459, Penal Code" {
		t.Errorf("code article title: %q", got)
	}
	l.Code = nil
	if got := artTitle(en, l, "3"); got != "Art. 3, Decree No. 16436 of 25 May 1964" {
		t.Errorf("article title: %q", got)
	}
	if got := artTitle(ar, l, "3"); !strings.HasPrefix(got, "المادة 3") {
		t.Errorf("Arabic article title: %q", got)
	}
}

func TestAnswerLineFromRecordOnly(t *testing.T) {
	l := decree()
	got := lawAnswer(en, l)
	for _, want := range []string{
		"Decree No. 16436 of 25 May 1964.", "Official Gazette issue 45 of 4 Jun 1964", "Articles: 74.",
		"No repeal recorded in our sources.", "Text in Arabic, as published.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("answer %q lacks %q", got, want)
		}
	}
	if strings.Contains(strings.ToLower(got), "in force") || strings.Contains(got, "Repealed") {
		t.Errorf("answer claims a status the record does not hold: %q", got)
	}

	l.Status = "repealed"
	if got := lawAnswer(en, l); !strings.Contains(got, "Status: repealed.") || strings.Contains(got, "No repeal recorded") {
		t.Errorf("repealed answer: %q", got)
	}

	bare := &Law{LawRow: LawRow{Kind: "قانون", KindEn: ptr("Law"), Status: "in_force"}}
	got = lawAnswer(en, bare)
	if strings.Contains(got, "Gazette") || strings.Contains(got, "Articles") || strings.Contains(got, "changing") {
		t.Errorf("bare answer invents facts: %q", got)
	}

	l.Status = "in_force"
	l.Changes = []Change{{By: LawRow{ID: 1}}, {By: LawRow{ID: 2}}}
	if got := lawAnswer(en, l); !strings.Contains(got, "changing it: 2.") {
		t.Errorf("amended count missing: %q", got)
	}
	for _, b := range []*i18n.Bundle{en, ar} {
		for _, s := range []string{lawAnswer(b, l), lawDesc(b, l), articleAnswer(b, l, "1"), lawTitle(b, l)} {
			if strings.ContainsAny(s, "\u2014–") {
				t.Errorf("%s: dash in %q", b.Lang, s)
			}
		}
	}
}

func TestAnswerLineArabic(t *testing.T) {
	got := lawAnswer(ar, decree())
	for _, want := range []string{"مرسوم رقم 16436 تاريخ 25/05/1964.", "الجريدة الرسمية العدد 45", "عدد المواد: 74.", "لا إلغاء مسجل"} {
		if !strings.Contains(got, want) {
			t.Errorf("Arabic answer %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "نافذ") {
		t.Errorf("Arabic answer says in force: %q", got)
	}
}

func TestCodeAnswerNamesTheCode(t *testing.T) {
	l := decree()
	l.Code = ptr("Penal Code")
	if got := lawAnswer(en, l); !strings.HasPrefix(got, "Penal Code: Decree No. 16436 of 25 May 1964.") {
		t.Errorf("code answer: %q", got)
	}
	if got := lawAnswer(ar, l); !strings.HasPrefix(got, l.Title+": ") {
		t.Errorf("Arabic code answer: %q", got)
	}
}

func TestMetaDescription(t *testing.T) {
	l := decree()
	l.Changes = []Change{{By: LawRow{ID: 1}}}
	for _, b := range []*i18n.Bundle{en, ar} {
		d := lawDesc(b, l)
		if n := len([]rune(d)); n > descMax {
			t.Errorf("%s description is %d characters: %q", b.Lang, n, d)
		}
		if !strings.HasPrefix(d, identIn(b, l.LawRow)+".") {
			t.Errorf("%s description does not lead with the identifier: %q", b.Lang, d)
		}
		if !strings.Contains(d, "45") {
			t.Errorf("%s description lacks the Gazette issue: %q", b.Lang, d)
		}
	}

	if d := articleDesc(en, l, "1", "ينشر هذا المرسوم"); !strings.HasPrefix(d, "Article 1 of Decree No. 16436") || len([]rune(d)) > descMax {
		t.Errorf("article description: %q", d)
	}
}

func TestLegislationLDHonest(t *testing.T) {
	l := decree()
	l.TitleEn = ptr("Setting the certificates")
	l.Code = ptr("Some Code")
	ld := legislationLD(l, "/decree/193806/", "answer line")
	if _, ok := ld["legislationLegalForce"]; ok {
		t.Errorf("legal force claimed with no repeal in the record: %v", ld["legislationLegalForce"])
	}
	if _, ok := ld["numberOfItems"]; ok {
		t.Errorf("numberOfItems is not a Legislation property")
	}
	if ld["@id"] != Base+"/decree/193806/" || ld["url"] != ld["@id"] {
		t.Errorf("Arabic page ids: %v %v", ld["@id"], ld["url"])
	}
	if ld["@type"] != "Legislation" || ld["description"] != "answer line" || ld["legislationDate"] != "1964-05-25" || ld["datePublished"] != "1964-06-04" {
		t.Errorf("shape: %v", ld)
	}
	if alt, _ := ld["alternateName"].([]string); len(alt) != 2 {
		t.Errorf("alternateName: %v", ld["alternateName"])
	}
	l.Status = "repealed"
	if got := legislationLD(l, "/decree/193806/", "")["legislationLegalForce"]; got != "https://schema.org/NotInForce" {
		t.Errorf("repealed text: %v", got)
	}

	b, err := json.Marshal(legislationLD(l, "/decree/193806/", ""))
	if err != nil || !strings.Contains(string(b), `"@type":"Legislation"`) {
		t.Errorf("marshal: %v %s", err, b)
	}
}

func TestEnglishLDPointsAtEnglishPages(t *testing.T) {
	in := []any{breadcrumbs("Legislation", "/laws/", "x", "/law/1/"), map[string]any{"@type": "Legislation", "url": Base + "/law/1/"}}
	out := englishLD(in)
	items := out[0].(map[string]any)["itemListElement"].([]any)
	if got := items[0].(map[string]any)["item"]; got != Base+"/en/" {
		t.Errorf("home crumb: %v", got)
	}
	if got := items[2].(map[string]any)["item"]; got != Base+"/en/law/1/" {
		t.Errorf("last crumb: %v", got)
	}
	if got := in[0].(map[string]any)["itemListElement"].([]any)[0].(map[string]any)["item"]; got != Base+"/" {
		t.Errorf("input was changed: %v", got)
	}
	if out[1].(map[string]any)["url"] != Base+"/law/1/" {
		t.Errorf("a non-list object was changed")
	}
}

func TestCutWords(t *testing.T) {
	if got := cutWords("short", 10); got != "short" {
		t.Errorf("short: %q", got)
	}
	got := cutWords("Authorizing the conclusion of the agreement", 20)
	if n := len([]rune(got)); n > 20 || !strings.HasSuffix(got, "…") || strings.Contains(got, "concl") {
		t.Errorf("cut: %q (%d)", got, n)
	}
}

func TestPagesRenderAnswerLine(t *testing.T) {
	s := onpageServer(t)
	for _, c := range []struct{ path, title, answer, h1 string }{
		{"/en/decree/193806/", "Decree No. 16436 of 25 May 1964", "Decree No. 16436 of 25 May 1964. Published in Official Gazette issue 45", `<h1 lang="ar"`},
		{"/decree/193806/", "مرسوم رقم 16436 تاريخ 25/05/1964", "مرسوم رقم 16436 تاريخ 25/05/1964. نشر في الجريدة الرسمية العدد 45", `<h1 lang="ar"`},
		{"/en/decree/193806/article/1/", "Art. 1, Decree No. 16436", "Article 1 of Decree No. 16436 of 25 May 1964.", `<h1 lang="ar"`},
	} {
		code, body, _ := getHTML(t, s, c.path)
		if code != 200 {
			t.Fatalf("%s: status %d", c.path, code)
		}
		if !strings.Contains(body, "<title>"+c.title) {
			t.Errorf("%s: title does not lead with %q", c.path, c.title)
		}
		i := strings.Index(body, `<p class="answer"`)
		if i < 0 || !strings.Contains(body[i:i+400], c.answer) {
			t.Errorf("%s: answer line missing or wrong", c.path)
		}
		if i > 0 && strings.Index(body, "<main") > i {
			t.Errorf("%s: answer line is outside main", c.path)
		}
		if strings.Contains(body, "legislationLegalForce\":\"https://schema.org/InForce") {
			t.Errorf("%s: claims InForce", c.path)
		}
		if strings.Contains(body, "numberOfItems") && !strings.Contains(c.path, "codes") {
			t.Errorf("%s: numberOfItems", c.path)
		}
	}
}

func TestEnglishTitleSlot(t *testing.T) {
	s := onpageServer(t)
	var id int
	if err := s.db.QueryRow(context.Background(), `SELECT t.law_id FROM legislation_title_en t JOIN legislation l ON l.id = t.law_id
		WHERE l.number IS NOT NULL AND l.issued_on IS NOT NULL ORDER BY t.law_id LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no English titles loaded: %v", err)
	}
	var titleEn string
	_ = s.db.QueryRow(context.Background(), `SELECT title_en FROM legislation_title_en WHERE law_id = $1`, id).Scan(&titleEn)
	_, body, _ := getHTML(t, s, "/en"+lawPath(id))
	if !strings.Contains(body, "Unofficial English title") || !strings.Contains(body, `<h1 lang="en"`) || !strings.Contains(body, "Official title, in Arabic") {
		t.Errorf("English slot missing on /en%s", lawPath(id))
	}
	if !regexp.MustCompile(`(?s)Unofficial English title.*<h1 lang="en".*Official title, in Arabic`).MatchString(body) {
		t.Errorf("English title is not above the Arabic one")
	}
	_, ar, _ := getHTML(t, s, lawPath(id))
	if strings.Contains(ar, "Unofficial English title") || strings.Contains(ar, `<h1 lang="en"`) {
		t.Errorf("the English slot shows on the Arabic page")
	}
	if !strings.Contains(ar, `<h1 lang="ar"`) {
		t.Errorf("Arabic page lost its Arabic h1")
	}
}

func TestListPagesLastUpdated(t *testing.T) {
	s := onpageServer(t)
	for _, p := range []string{"/en/laws/", "/en/codes/", "/en/gazette/", "/laws/", "/codes/", "/gazette/"} {
		code, body, _ := getHTML(t, s, p)
		if code != 200 {
			t.Fatalf("%s: %d", p, code)
		}
		if !regexp.MustCompile(`<p class="updated"><time datetime="\d{4}-\d\d-\d\d">`).MatchString(body) {
			t.Errorf("%s: no Last updated line", p)
		}
		if !strings.Contains(body, "<h2") {
			t.Errorf("%s: no h2", p)
		}
		if strings.Count(body, "<h1") != 1 {
			t.Errorf("%s: %d h1", p, strings.Count(body, "<h1"))
		}
	}
	_, body, _ := getHTML(t, s, "/en/codes/")
	if !strings.Contains(body, "<h3 lang=\"ar\">") {
		t.Errorf("/codes/: no h3 per code")
	}
}

func onpageServer(t *testing.T) *Server {
	t.Helper()
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		return testServer(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil || pool.Ping(ctx) != nil {
		t.Skipf("no Postgres at LEBLEG_DSN: %v", err)
	}
	t.Cleanup(pool.Close)
	s, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}
