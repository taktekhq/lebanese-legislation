package site

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func on(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// testLaw is a text whose article 2 was replaced by Law 12/2026, in force on
// 1 Oct 2026, and whose page last changed on 30 Oct 2026.
func testLaw(changed *time.Time) *Law {
	amending := LawRow{ID: 99, Kind: "قانون", Number: ptr("12"), IssuedOn: on("2026-09-28"), Title: "قانون تعديل"}
	older := LawRow{ID: 98, Kind: "قانون", Number: ptr("3"), IssuedOn: on("2000-01-01"), Title: "قانون أقدم"}
	l := &Law{LawRow: LawRow{ID: 12, Kind: "قانون", Number: ptr("1"), IssuedOn: on("1964-03-01"), Title: "قانون رقم 1"}, Changed: changed}
	l.List = []*Article{{Number: "1"}, {Number: "2", Links: []Link{
		{Relation: "replace", By: older, ByArticle: "1", Effective: on("2000-01-05")},
		{Relation: "replace", By: amending, ByArticle: "4", Effective: on("2026-10-01")},
	}}, {Number: "3"}}
	return l
}

func requestIn(lang, path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	return r.WithContext(context.WithValue(r.Context(), langCtxKey, lang))
}

// ldObjects reads every JSON-LD object of a page.
func ldObjects(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, part := range strings.Split(body, `<script type="application/ld+json">`)[1:] {
		raw, _, _ := strings.Cut(part, "</script>")
		var one map[string]any
		if json.Unmarshal([]byte(raw), &one) == nil {
			out = append(out, one)
			continue
		}
		var many []map[string]any
		if err := json.Unmarshal([]byte(raw), &many); err != nil {
			t.Fatalf("bad JSON-LD %q: %v", raw, err)
		}
		out = append(out, many...)
	}
	return out
}

func modified(t *testing.T, body, typ string) any {
	t.Helper()
	for _, o := range ldObjects(t, body) {
		if o["@type"] == typ {
			return o["dateModified"]
		}
	}
	t.Fatalf("no %s JSON-LD in the page", typ)
	return nil
}

// An article page says when its text last changed and which text last
// amended the article, in Arabic and English, and its JSON-LD carries the
// same date as dateModified.
func TestArticlePageShowsLastUpdated(t *testing.T) {
	s := noDBServer(t)
	for _, c := range []struct{ lang, prefix, line, by string }{
		{"ar", "", `<time datetime="2026-10-30">آخر تحديث 30 تشرين الأول 2026</time>`, "آخر تعديل بموجب"},
		{"en", "/en", `<time datetime="2026-10-30">Last updated 30 October 2026</time>`, "Last amended by"},
	} {
		r := requestIn(c.lang, c.prefix+"/law/12/article/2/")
		body, err := s.execute(r, "article", s.articlePage(r, testLaw(on("2026-10-30")), 1))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, c.line) {
			t.Errorf("%s: no line %q", c.lang, c.line)
		}
		if !strings.Contains(body, c.by) || !strings.Contains(body, `href="`+c.prefix+`/law/99/"`) {
			t.Errorf("%s: the line does not name the last amending text (Law 12, /law/99/)", c.lang)
		}
		if got := modified(t, body, "Legislation"); got != "2026-10-30" {
			t.Errorf("%s: dateModified %v, want 2026-10-30", c.lang, got)
		}
	}
}

// A page whose change date is not known, or is the text's own date that an
// old build seeded (before the site first fetched the Centre), shows no date
// and no dateModified: never a fake one.
func TestNoDateWithoutAUsableChange(t *testing.T) {
	s := noDBServer(t)
	for _, changed := range []*time.Time{nil, on("1964-03-01"), on("2026-09-24")} {
		r := requestIn("en", "/en/law/12/article/1/")
		body, err := s.execute(r, "article", s.articlePage(r, testLaw(changed), 0))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, "Last updated") || strings.Contains(body, "dateModified") {
			t.Errorf("changed %v: the page shows a date", changed)
		}
	}
}

// A text's own page shows the date as a row of its facts, with the text that
// last amended it, and its Legislation JSON-LD has dateModified.
func TestLawPageShowsLastUpdated(t *testing.T) {
	s := noDBServer(t)
	l := testLaw(on("2026-10-30"))
	r := requestIn("en", "/en/law/12/")
	l.Updated = s.pageUpdated(r, l.Changed, l.lastAmendment())
	ld := legislationLD(l, lawPath(l.ID), "")
	body, err := s.execute(r, "law", Page{Lang: "ar", Translated: true, Path: lawPath(l.ID), LD: []any{ld}, Data: l})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `<dt>Last updated</dt><dd><time datetime="2026-10-30">30 October 2026</time><br><bdi>Last amended by</bdi> <a class="link" href="/en/law/99/" lang="ar">قانون رقم 12 تاريخ 28/09/2026</a>`) {
		t.Errorf("no Last updated row naming Law 12 in %s", body[strings.Index(body, `<dl class="facts">`):][:600])
	}
	if got := modified(t, body, "Legislation"); got != "2026-10-30" {
		t.Errorf("dateModified %v", got)
	}
	if ld["datePublished"] != nil {
		t.Errorf("datePublished %v without a Gazette date", ld["datePublished"])
	}
}

// The amendment named is the latest in force by today, from the amendment
// table or an article's links; one not yet in force, or still under review,
// is not.
func TestLastAmendment(t *testing.T) {
	l := testLaw(nil)
	if by := l.lastAmendment(); by == nil || by.ID != 99 {
		t.Fatalf("last amendment %v, want 99", by)
	}
	future := time.Now().AddDate(0, 1, 0)
	l.Changes = []Change{
		{Status: "review", Effective: on("2026-10-20"), By: LawRow{ID: 50}},
		{Status: "applied", Effective: &future, By: LawRow{ID: 51}},
	}
	if by := l.lastAmendment(); by == nil || by.ID != 99 {
		t.Fatalf("last amendment %v, want 99 (review and future changes do not count)", by)
	}
	l.Changes = append(l.Changes, Change{Status: "applied", Effective: on("2026-10-15"), By: LawRow{ID: 52}})
	if by := l.lastAmendment(); by == nil || by.ID != 52 {
		t.Fatalf("last amendment %v, want 52", by)
	}
	if by := lastArticleAmendment(l.List[0]); by != nil {
		t.Fatalf("article 1 was never amended, got %v", by)
	}
	if by := lastArticleAmendment(l.List[1]); by == nil || by.ID != 99 {
		t.Fatalf("article 2: %v, want 99", by)
	}
}

// A Gazette issue's JSON-LD is a PublicationIssue with its date and, when
// known, dateModified.
func TestGazetteLD(t *testing.T) {
	ld := gazetteLD(Doc{Title: "الجريدة الرسمية، العدد 5"}, "/gazette/7/", ptr("5"), on("1990-02-01"), on("2026-10-30"))
	if ld["@type"] != "PublicationIssue" || ld["datePublished"] != "1990-02-01" || ld["dateModified"] != "2026-10-30" || ld["issueNumber"] != "5" {
		t.Fatalf("%v", ld)
	}
	if ld := gazetteLD(Doc{}, "/gazette/7/", nil, nil, on("1990-02-01")); ld["dateModified"] != nil {
		t.Fatalf("dateModified from a date before the site existed: %v", ld)
	}
}

func TestLongDate(t *testing.T) {
	d := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	if got := longDate("ar", d); got != "2 تشرين الثاني 2026" {
		t.Errorf("ar %q", got)
	}
	if got := longDate("en", d); got != "2 November 2026" {
		t.Errorf("en %q", got)
	}
}
