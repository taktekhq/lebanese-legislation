package site

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Searches people made on 20-22 Oct 2026 (PostHog) that showed the wrong text
// first, or nothing, and the text each should open with. The ids are the
// Centre's, stable across nightly loads.
func TestPostHogSearchesFindTheirText(t *testing.T) {
	dsn := os.Getenv("LEBLEG_DSN")
	if dsn == "" {
		t.Skip("LEBLEG_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("no database: %v", err)
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		t.Skip("database not reachable")
	}
	s := &Server{db: pool}
	v, err := loadVocab(ctx, pool)
	if err != nil {
		t.Skipf("no legislation table: %v", err)
	}
	s.vocab.Store(v)

	for _, c := range []struct {
		q    string
		want int
		near bool   // the text is among the close titles shown above the articles
		via  bool   // the page says it read the citation differently
		then int    // a text that must be in the first three
		kind string // the first text's kind, when no one text is the answer
	}{
		{q: "337", want: 183490},                           // notaries' law 337/1994, not customs amendment no. 337
		{q: "11244", want: 198100},                         // decree 11244/2003 on the traffic authority
		{q: "نظام كتاب العدل", want: 183490},               // in force, not decree-law 76/1940 it replaced
		{q: "القانون30 14/9/1982", want: 8281},             // a number glued to the kind word
		{q: "المرسوم 11244تاريخ 25/10/2003", want: 198100}, // ... and to «تاريخ»
		{q: "القرار رقم 123/ل.ر", want: 0},                 // checked below: a High Commissioner's decision
		{q: "قوانين الجنسية", want: 202037},                // plural kind word
		{q: "قانون الغابات اللبناني 7/12/1949", want: 181678},
		{q: "قانون الغاباتت الصادر بتاريخ 7/1/1949 عدد الجريدة الرسمية 3 تاريخ النشر  19/1/1949 الصفحة 30-45", want: 181678},
		{q: "The Forest Law issued on 07/01/1949, Official Gazette issue: 3 | Date of publication: 19/01/1949 | Pages: 30–54", want: 181678},
		{q: "access information", want: 269965}, // English title of law 28/2017
		{q: "قانون الغابات اللبناني", want: 181678, near: true},
		// 22-23 Oct 2026
		{q: "القانون 617 تاريخ 8/8/2012 المعدّل للقانون 15874", want: 217637, via: true},              // decree 617 of 8/8/2007, amending decree 15874
		{q: "تعديل المرسوم رقم 15874 تاريخ 5/12/2005 (المرسوم التطبيقي لقانون البناء)", want: 217637}, // its title, pasted
		{q: "قانون البلديات", want: 244078, then: 244635},                                             // the laws of that name before an amendment of 1961
		{q: "نظام الخارجية", want: 196323, then: 244658},
		{q: "المرسوم 112", want: 179571}, // decree-law 112/1959, the civil servants' rules
		{q: "قانون 146", kind: "قانون"},  // one of the four laws 146, not a decree-law 146
		// 23 Oct 2026
		{q: "(2012) (القرار رقم 1/1 - الشروط الواجب مراعاتها في المصانع والمستودعات الغذائية)", want: 239653, via: true}, // decision 1 of 2015
	} {
		res, _, err := s.searchFor(ctx, c.q)
		if err != nil {
			t.Fatal(err)
		}
		list := res.Laws
		if c.near {
			list = res.Near
		}
		if len(list) == 0 {
			t.Errorf("%q: no texts (near %v)", c.q, c.near)
			continue
		}
		if c.kind != "" {
			if list[0].Kind != c.kind {
				t.Errorf("%q: first text is a %s (%d), want a %s", c.q, list[0].Kind, list[0].ID, c.kind)
			}
			continue
		}
		if c.via && res.Via == "" {
			t.Errorf("%q: no line saying what was found instead", c.q)
		}
		if c.then != 0 && !slices.ContainsFunc(list[:min(3, len(list))], func(l LawRow) bool { return l.ID == c.then }) {
			t.Errorf("%q: %d not in the first three", c.q, c.then)
		}
		if c.want == 0 {
			if list[0].Kind != "قرار المفوض السامي" {
				t.Errorf("%q: first text is a %s, want a High Commissioner's decision", c.q, list[0].Kind)
			}
			continue
		}
		if list[0].ID != c.want {
			t.Errorf("%q: first text %d (%s), want %d", c.q, list[0].ID, list[0].Title, c.want)
		}
	}

	// The article of a cited text, the date a little off (decree 2868 is of 16/12/1959).
	for _, q := range []string{"المادة 67 من المرسوم 2868 تاريخ 27/12/1959", "المادة 67 من المرسوم 2868"} {
		if to := s.articleRef(ctx, q); !strings.HasSuffix(to, "/203704/article/67/") {
			t.Errorf("articleRef(%q) = %q, want decree 2868/1959 article 67", q, to)
		}
	}

	// Close titles only when a word was dropped with a kind word to anchor it.
	if res, _, err := s.searchFor(ctx, "عدم الصفة"); err != nil || len(res.Near) > 0 {
		t.Errorf("عدم الصفة: close titles %d (err %v), want none: every title with «الصفة» is noise", len(res.Near), err)
	}
}

func TestInForceFirst(t *testing.T) {
	num := func(s string) *string { return &s }
	row := func(id int, status string) LawRow { return LawRow{ID: id, Status: status, Number: num("1")} }
	ids := func(ls []LawRow) []int {
		var out []int
		for _, l := range ls {
			out = append(out, l.ID)
		}
		return out
	}
	for _, c := range []struct {
		in   []LawRow
		want []int
	}{
		// a repealed text leads: the closest in force is lifted, the rest keep their order
		{[]LawRow{row(1, "repealed"), row(2, "repealed"), row(3, "in_force"), row(4, "in_force")}, []int{3, 1, 2, 4}},
		// one in force leads already: nothing moves, repealed namesakes stay above amendments
		{[]LawRow{row(1, "in_force"), row(2, "repealed"), row(3, "in_force")}, []int{1, 2, 3}},
		// a code outranks a text that is not one: never lifted above it
		{[]LawRow{{ID: 1, Status: "repealed", Code: num("Penal Code")}, row(2, "in_force")}, []int{1, 2}},
	} {
		if got := ids(inForceFirst(c.in, nil, false)); !slices.Equal(got, c.want) {
			t.Errorf("inForceFirst %v = %v, want %v", ids(c.in), got, c.want)
		}
	}
}
