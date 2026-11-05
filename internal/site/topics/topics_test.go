package topics

// The topic pages' tests. The structure checks run everywhere. The
// grounding checks (every cited article exists, every quote is an exact
// piece of its article's stored text, every number a statement makes is in a
// cited article) need the database: they run when LEBLEG_DSN is set (or a
// local database named lebleg is reachable) and are skipped otherwise, like
// the other database tests of this module.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTopicsParse(t *testing.T) {
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestStructure(t *testing.T) {
	for _, tp := range All() {
		for _, p := range Structure(tp) {
			t.Error(p)
		}
	}
}

func testDB(t *testing.T) DB {
	t.Helper()
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("LEBLEG_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("no database: %v", err)
	}
	t.Cleanup(pool.Close)
	return DB{Pool: pool}
}

// TestGrounding is the verifier: a quote that is not an exact piece of the
// cited article's text, a cited article that does not exist, or a number in
// a statement that no cited article holds, fails the build.
func TestGrounding(t *testing.T) {
	db := testDB(t)
	for _, tp := range All() {
		for _, p := range Grounding(tp, db) {
			t.Error(p)
		}
	}
}

// fake is a Source for the checker's own tests.
type fake map[string]Facts

func (f fake) Article(law int, number string) (Facts, bool, error) {
	x, ok := f[strings.Join([]string{string(rune('0' + law)), number}, "/")]
	return x, ok, nil
}

func sample() *Topic {
	para := func(text string, cites ...string) Para { return Para{Text: text, Cites: cites} }
	c := func(lang string) Content {
		return Content{Title: "T " + lang, Question: "Q?", Description: "D",
			Answer: para("Annual leave is 15 days. It is paid.", "a1"),
			Sections: []Section{
				{Heading: "Leave", Paras: []Para{para("Fifteen days, per Article 7.", "a1")}},
				{Heading: "Rest", Paras: []Para{para("Rest is 36 hours.", "a2"), {Text: "Not covered here.", Note: true}}},
				{Heading: "Hours", Paras: []Para{para("Hours are 48 a week.", "a2")},
					Table: &Table{Head: []string{"x", "y"}, Rows: []Row{{Cells: []string{"week", "48"}, Cites: []string{"a2"}}}}},
			}}
	}
	return &Topic{Slug: "sample", Published: "2026-10-21", Updated: "2026-10-21",
		Articles: []Cite{{ID: "a1", Law: 1, Article: "7", Quote: "اجازة سنوية خمسة عشر يوما"}, {ID: "a2", Law: 1, Article: "8", Quote: "راحة اسبوعية لا تقل عن 36 ساعة"}},
		EN:       c("en"), AR: c("ar")}
}

func TestCheckerAcceptsGoodTopic(t *testing.T) {
	src := fake{"1/7": {Text: "لكل اجير الحق في اجازة سنوية خمسة عشر يوما باجر كامل"}, "1/8": {Text: "راحة اسبوعية لا تقل عن 36 ساعة. الحد الاعلى 48 ساعة"}}
	if p := Check(sample(), src); len(p) > 0 {
		t.Errorf("good topic flagged: %v", p)
	}
}

func TestCheckerCatchesBadQuoteMissingArticleAndNumber(t *testing.T) {
	src := fake{"1/7": {Text: "لكل اجير الحق في اجازة سنوية عشرين يوما"}, "1/8": {Text: "راحة اسبوعية لا تقل عن 36 ساعة"}}
	tp := sample()
	p := strings.Join(Grounding(tp, src), "\n")
	if !strings.Contains(p, "quote is not an exact piece") {
		t.Errorf("a quote not in the article passed:\n%s", p)
	}
	if !strings.Contains(p, "number 15") {
		t.Errorf("15 days against a text that says 20 passed:\n%s", p)
	}
	if !strings.Contains(p, "number 48") {
		t.Errorf("48 hours, absent from the cited article, passed:\n%s", p)
	}
	delete(src, "1/8")
	if p := strings.Join(Grounding(tp, src), "\n"); !strings.Contains(p, "does not exist") {
		t.Errorf("a missing article passed:\n%s", p)
	}
}

func TestStructureCatchesUncitedClaims(t *testing.T) {
	tp := sample()
	tp.EN.Sections[0].Paras[0].Cites = nil
	tp.EN.Sections[1].Paras[0].Cites = []string{"zz"}
	tp.EN.Sections[2].Paras[0].Text = "Dash \u2014 here."
	p := strings.Join(Structure(tp), "\n")
	for _, want := range []string{"no cite and is not marked as a note", `"zz" which is not in articles`, "em dash", "differ in shape"} {
		if !strings.Contains(p, want) {
			t.Errorf("structure check missed %q:\n%s", want, p)
		}
	}
}

// A cite may point at a row of a table annexed to a text or at an article's
// first wording, read from the Gazette's pages (reading.go): the checker
// holds them to the same rules as an article (5 Nov 2026).
func TestCheckerReadingCites(t *testing.T) {
	tp := sample()
	tp.Articles = append(tp.Articles,
		Cite{ID: "r1", Law: 1, Article: "annex-3-22", Quote: "استعمال أي جهاز من اجهزة الاتصال اثناء القيادة"},
		Cite{ID: "o1", Law: 1, Article: "original-52", Quote: "٥٠ ٪ من التعويض المذكور اذا اشترك خمس سنوات على الاكثر"})
	for _, c := range []*Content{&tp.EN, &tp.AR} {
		c.Sections[0].Paras = append(c.Sections[0].Paras,
			Para{Text: "Using a phone while driving is in category 3 of the annexed tables and costs 3 points.", Cites: []string{"r1"}},
			Para{Text: "In 1963, 5 years of contributions or less gave 50% of the indemnity.", Cites: []string{"o1"}})
	}
	src := fake{"1/7": {Text: "لكل اجير الحق في اجازة سنوية خمسة عشر يوما باجر كامل"}, "1/8": {Text: "راحة اسبوعية لا تقل عن 36 ساعة. الحد الاعلى 48 ساعة"},
		"1/annex-3-22":  {Text: "استعمال أي جهاز من اجهزة الاتصال اثناء القيادة.", Extra: "جدول مخالفات السير فئة ثالثة\nالفئة 3\nالرقم 22\nالنقاط ٣"},
		"1/original-52": {Text: "- ٥٠ ٪ من التعويض المذكور اذا اشترك خمس سنوات على الاكثر .", Extra: "1963"}}
	if p := Check(tp, src); len(p) > 0 {
		t.Errorf("good reading cites flagged: %v", p)
	}
	tp.EN.Sections[0].Paras[1].Text = "Using a phone while driving is in category 3 of the annexed tables and costs 4 points."
	if p := strings.Join(Grounding(tp, src), "\n"); !strings.Contains(p, "number 4 is in no cited article") {
		t.Errorf("4 points against a row that prints ٣ passed:\n%s", p)
	}
	delete(src, "1/original-52")
	if p := strings.Join(Grounding(tp, src), "\n"); !strings.Contains(p, "original-52 of law 1 does not exist") {
		t.Errorf("a missing reading passed:\n%s", p)
	}
	tp.Articles[len(tp.Articles)-1].Article = "original-"
	tp.Articles[len(tp.Articles)-2].Article = "annex-3"
	p := strings.Join(Structure(tp), "\n")
	for _, id := range []string{"cite r1:", "cite o1:"} {
		if !strings.Contains(p, id+` "`) {
			t.Errorf("malformed reading address %s passed:\n%s", id, p)
		}
	}
}

func TestParseReadingAndDecimals(t *testing.T) {
	for in, want := range map[string]Reading{
		"annex-3-22": {Kind: "annex", Category: "3", Serial: "22"}, "annex-p-1": {Kind: "annex", Category: "p", Serial: "1"},
		"original-52": {Kind: "original", Number: "52"},
	} {
		if got, ok := ParseReading(in); !ok || got != want {
			t.Errorf("ParseReading(%q) = %+v, %v", in, got, ok)
		}
	}
	for _, in := range []string{"52", "annex-3", "annex-3-0", "original-", "annex-x-1"} {
		if _, ok := ParseReading(in); ok {
			t.Errorf("ParseReading(%q) accepted", in)
		}
	}
	if got := decimals("بنسبة تزيد عن ٠,٥ غ ل وحتى ٠,٨ غ /ل و١,٥٠٠ ليرة"); got != "0.5 0.8" {
		t.Errorf("decimals: %q", got)
	}
}
