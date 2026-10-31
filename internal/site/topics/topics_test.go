package topics

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
		dsn = "host=/tmp dbname=lebleg"
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

func TestGrounding(t *testing.T) {
	db := testDB(t)
	for _, tp := range All() {
		for _, p := range Grounding(tp, db) {
			t.Error(p)
		}
	}
}

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
