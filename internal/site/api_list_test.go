package site

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDateBound(t *testing.T) {
	for _, c := range []struct {
		in, from, to string
	}{
		{"2025", "2025-01-01", "2025-12-31"},
		{"2024", "2024-01-01", "2024-12-31"},
		{"2025-02", "2025-02-01", "2025-02-28"},
		{"2024-02", "2024-02-01", "2024-02-29"},
		{"2025-05-16", "2025-05-16", "2025-05-16"},
	} {
		f, err1 := parseDateBound(c.in, false)
		e, err2 := parseDateBound(c.in, true)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: %v %v", c.in, err1, err2)
		}
		if f.Format("2006-01-02") != c.from || e.Format("2006-01-02") != c.to {
			t.Errorf("%s: %s..%s, want %s..%s", c.in, f.Format("2006-01-02"), e.Format("2006-01-02"), c.from, c.to)
		}
	}
	for _, bad := range []string{"", "25", "2025-13", "yesterday", "2025-02-30", "2025/01"} {
		if _, err := parseDateBound(bad, false); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestChangeRelation(t *testing.T) {
	plus, part := "+", "الفقرة 2"
	for _, c := range []struct {
		c    Change
		want string
	}{
		{Change{Action: "replace"}, "replace"},
		{Change{Action: "replace", Part: &part}, "amend"},
		{Change{Action: "insert"}, "insert"},
		{Change{Action: "replace", Part: &plus}, "insert"},
		{Change{Action: "repeal"}, "repeal"},
	} {
		if got := changeRelation(c.c); got != c.want {
			t.Errorf("%+v: %s, want %s", c.c, got, c.want)
		}
	}
}

func TestAPIListLaws(t *testing.T) {
	s := testServer(t)
	var all apiLawList
	if resp := getJSON(t, s, "/api/v1/laws/?since=2025-01&limit=10", &all); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if all.Order != "newest" || all.Total == 0 || len(all.Laws) != 10 || all.NextOffset == nil || *all.NextOffset != 10 {
		t.Fatalf("shape: order %q total %d laws %d next %v", all.Order, all.Total, len(all.Laws), all.NextOffset)
	}
	if all.Since == nil || *all.Since != "2025-01" || all.Until != nil {
		t.Errorf("since/until echo: %v %v", all.Since, all.Until)
	}
	prev := "9999"
	for _, l := range all.Laws {
		if l.Date == nil || *l.Date < "2025-01-01" || *l.Date > prev {
			t.Errorf("law %d date %v: not newest first from 2025-01 (prev %s)", l.ID, l.Date, prev)
		}
		prev = *l.Date
		if l.URL == "" {
			t.Errorf("law %d has no url", l.ID)
		}
	}

	var next apiLawList
	getJSON(t, s, "/api/v1/laws/?since=2025-01&limit=10&offset=10", &next)
	if next.Total != all.Total || len(next.Laws) != 10 || next.Laws[0].ID == all.Laws[9].ID {
		t.Errorf("page 2: total %d (want %d), %d laws", next.Total, all.Total, len(next.Laws))
	}

	var old apiLawList
	getJSON(t, s, "/api/v1/laws/?since=2025-05-16&until=2025-05-16&order=oldest&limit=100", &old)
	if old.Order != "oldest" || old.Total == 0 {
		t.Fatalf("one day: %+v", old.Total)
	}
	for _, l := range old.Laws {
		if l.Date == nil || *l.Date != "2025-05-16" {
			t.Errorf("law %d date %v outside the day", l.ID, l.Date)
		}
	}

	var last apiLawList
	getJSON(t, s, "/api/v1/laws/?since=2025-05-16&until=2025-05-16&limit=100&offset="+itoa(old.Total-1), &last)
	if len(last.Laws) != 1 || last.NextOffset != nil {
		t.Errorf("last page: %d laws, next %v", len(last.Laws), last.NextOffset)
	}

	var scans apiLawList
	getJSON(t, s, "/api/v1/laws/?since=2026&limit=100", &scans)
	found := false
	for _, l := range scans.Laws {
		if l.ID >= scanIDBase && l.GazetteIssueID != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("no scanned text with a gazette_issue_id among the 2026 texts")
	}

	var none, dated apiLawList
	getJSON(t, s, "/api/v1/laws/?limit=1", &none)
	getJSON(t, s, "/api/v1/laws/?since=1800&limit=1", &dated)
	if none.Total != dated.Total {
		t.Errorf("total %d with no bound, %d since 1800: undated texts are counted", none.Total, dated.Total)
	}

	for _, q := range []string{"since=yesterday", "until=2025-13", "order=sideways"} {
		if resp := getJSON(t, s, "/api/v1/laws/?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%s: status %d, want 400", q, resp.StatusCode)
		}
	}

	var big apiLawList
	getJSON(t, s, "/api/v1/laws/?limit=5000", &big)
	if len(big.Laws) != 100 {
		t.Errorf("limit 5000 gave %d laws", len(big.Laws))
	}
}

func TestAPIListGazette(t *testing.T) {
	s := testServer(t)
	var out apiIssueList
	if resp := getJSON(t, s, "/api/v1/gazette/?since=2025-07&limit=100", &out); resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if out.Total == 0 || len(out.Issues) == 0 {
		t.Fatalf("no issues: %+v", out)
	}
	prev := "9999"
	var scans, centre int
	for _, g := range out.Issues {
		if g.PublishedOn == nil || *g.PublishedOn < "2025-07-01" || *g.PublishedOn > prev {
			t.Errorf("issue %d on %v: not newest first from 2025-07", g.ID, g.PublishedOn)
		}
		prev = *g.PublishedOn
		switch g.Source {
		case "scans":
			scans++
			if g.ID < scanIDBase {
				t.Errorf("issue %d is a Centre id marked scans", g.ID)
			}
		case "centre":
			centre++
		default:
			t.Errorf("issue %d source %q", g.ID, g.Source)
		}
		if !strings.HasSuffix(g.URL, "/gazette/"+itoa(g.ID)+"/") {
			t.Errorf("issue %d url %s", g.ID, g.URL)
		}
	}
	if scans == 0 {
		t.Errorf("no scan issues since 2025-07 (centre %d)", centre)
	}
	var asc apiIssueList
	getJSON(t, s, "/api/v1/gazette/?order=oldest&limit=3", &asc)
	if len(asc.Issues) != 3 || asc.NextOffset == nil || *asc.NextOffset != 3 {
		t.Errorf("oldest: %d issues next %v", len(asc.Issues), asc.NextOffset)
	}

	var one apiGazetteDetail
	for _, g := range out.Issues {
		if g.Source == "scans" {
			if resp := getJSON(t, s, "/api/v1/gazette/"+itoa(g.ID)+"/", &one); resp.StatusCode != 200 {
				t.Fatalf("detail status %d", resp.StatusCode)
			}
			if len(one.Texts) == 0 || one.SourceURL != "" || g.TextCount != len(one.Texts) {
				t.Errorf("scan issue %d: %d texts (count %d), source %q", g.ID, len(one.Texts), g.TextCount, one.SourceURL)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest("GET", "/en/gazette/"+itoa(g.ID)+"/", nil))
			body := rec.Body.String()
			if rec.Code != 200 || !strings.Contains(body, "read from the scans") {
				t.Errorf("html page of scan issue %d: %d, no scan note", g.ID, rec.Code)
			}
			if strings.Contains(body, "OfficialJournal") {
				t.Errorf("html page of scan issue %d links to the Centre", g.ID)
			}
			break
		}
	}
	if resp := getJSON(t, s, "/api/v1/gazette/?until=2025-99", nil); resp.StatusCode != 400 {
		t.Errorf("bad until: status %d", resp.StatusCode)
	}
}

func TestAPILawAmendedBy(t *testing.T) {
	s := testServer(t)

	var law apiLawDetail
	if resp := getJSON(t, s, "/api/v1/law/333455/", &law); resp.StatusCode != 200 {
		t.Skipf("law 333455 not in this database: %d", resp.StatusCode)
	}
	var labour *apiLawChange
	for i := range law.Amended {
		if law.Amended[i].Law.ID == 190374 {
			labour = &law.Amended[i]
		}
	}
	if labour == nil || labour.Relation != "insert" || labour.Verb == "" || len(labour.Articles) == 0 || labour.EffectiveOn == nil {
		t.Fatalf("law 3/2025 amended: %+v", law.Amended)
	}
	var code apiLawDetail
	getJSON(t, s, "/api/v1/law/190374/", &code)
	var back *apiLawChange
	for i := range code.AmendedBy {
		if code.AmendedBy[i].Law.ID == 333455 && code.AmendedBy[i].Relation == "insert" {
			back = &code.AmendedBy[i]
		}
	}
	if back == nil || len(back.Articles) != len(labour.Articles) {
		t.Fatalf("Labour Code amended_by lacks law 3/2025: %+v", code.AmendedBy)
	}

	seen := map[string]bool{}
	var prev time.Time
	for _, c := range code.AmendedBy {
		k := c.Relation + "|" + itoa(c.Law.ID)
		if seen[k] {
			t.Errorf("duplicate entry %s", k)
		}
		seen[k] = true
		if c.EffectiveOn != nil {
			d, _ := time.Parse("2006-01-02", *c.EffectiveOn)
			if d.Before(prev) {
				t.Errorf("amended_by not oldest first at %s", k)
			}
			prev = d
		}
	}
}

func TestAPILawAmendedByFromGraph(t *testing.T) {
	s := testServer(t)
	var code apiLawDetail
	if resp := getJSON(t, s, "/api/v1/law/190374/", &code); resp.StatusCode != 200 {
		t.Skipf("Labour Code not in this database: %d", resp.StatusCode)
	}
	t.Logf("Labour Code amended_by: %d entries", len(code.AmendedBy))
	if len(code.AmendedBy) < 16 {
		t.Errorf("Labour Code amended_by has %d entries, want at least 16 (the MCP's count)", len(code.AmendedBy))
	}
	got := map[string]bool{}
	for _, c := range code.AmendedBy {
		if c.Law.ID == 333455 {
			for _, a := range c.Articles {
				got[a] = true
			}
			t.Logf("law 3/2025 %s: %v", c.Relation, c.Articles)
		}
	}
	for _, want := range []string{"1", "2", "12"} {
		if !got[want] {
			t.Errorf("Labour Code amended_by lacks law 3/2025 article %s: %v", want, got)
		}
	}
	if !got["12-1"] && !got["12/1"] {
		t.Errorf("law 3/2025 inserted articles missing: %v", got)
	}
}

func TestAPIPenalCodeAmendedBy(t *testing.T) {
	s := testServer(t)
	var code apiLawDetail
	if resp := getJSON(t, s, "/api/v1/law/244611/", &code); resp.StatusCode != 200 {
		t.Skipf("Penal Code not in this database: %d", resp.StatusCode)
	}
	t.Logf("Penal Code amended_by: %d entries", len(code.AmendedBy))
	if len(code.AmendedBy) < 64 {
		t.Fatalf("Penal Code amended_by has %d entries, want at least 64", len(code.AmendedBy))
	}

	start := time.Now()
	g, err := s.lawLinks(context.Background(), 244611, true, nil)
	t.Logf("graph amended_by: %d entries in %d ms", len(g), time.Since(start).Milliseconds())
	if err != nil || len(g) < 63 || len(code.AmendedBy) < len(g) {
		t.Errorf("graph amended_by of the Penal Code: %d entries (%v), want at least 63; merged %d", len(g), err, len(code.AmendedBy))
	}
	last := code.AmendedBy[len(code.AmendedBy)-1]
	if last.EffectiveOn == nil || !strings.HasPrefix(*last.EffectiveOn, "2026") {
		t.Errorf("newest amending text is not a 2026 law: %+v", last)
	}
}

func TestAPILawAmendedFromGraph(t *testing.T) {
	s := testServer(t)
	var law apiLawDetail
	const id = 2000200346
	if resp := getJSON(t, s, "/api/v1/law/"+itoa(id)+"/", &law); resp.StatusCode != 200 {
		t.Skip("Law 68/2026 not in this database")
	}
	rel := map[string]map[string]bool{}
	for _, c := range law.Amended {
		k := itoa(c.Law.ID) + " " + c.Relation
		rel[k] = map[string]bool{}
		for _, a := range c.Articles {
			rel[k][a] = true
		}
		t.Logf("amended: %s %s %v", c.Law.Title, c.Relation, c.Articles)
	}
	if !rel["244611 replace"]["37"] || !rel["244611 repeal"]["43"] {
		t.Errorf("Law 68/2026 should replace Penal article 37 and repeal 43: %v", rel)
	}
	if len(law.Amended) < 3 {
		t.Errorf("Law 68/2026 amended only %d entries", len(law.Amended))
	}
}

func TestAPILawTimingPenalCode(t *testing.T) {
	s := testServer(t)
	var code apiLawDetail
	for i := 0; i < 3; i++ {
		start := time.Now()
		if resp := getJSON(t, s, "/api/v1/law/244611/", &code); resp.StatusCode != 200 {
			t.Skip("Penal Code not in this database")
		}
		d := time.Since(start)
		t.Logf("call %d: %d ms (%d amended_by)", i+1, d.Milliseconds(), len(code.AmendedBy))
		if d > 3*time.Second {
			t.Errorf("law endpoint took %v", d)
		}
	}
}

func TestLawDetailOutlineMatchesFullLoad(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	ids := []int{244611}
	for _, q := range []string{
		`SELECT v.law_id FROM article_version v WHERE NOT EXISTS (SELECT 1 FROM legislation_article a
			WHERE a.law_id = v.law_id AND a.number = v.article_number) ORDER BY v.law_id LIMIT 1`,
		`SELECT law_id FROM amendment WHERE article_number IS NULL AND action = 'repeal' AND part IS NULL
			AND status = 'applied' AND law_id IS NOT NULL ORDER BY law_id LIMIT 1`,
	} {
		var id int
		if err := s.db.QueryRow(ctx, q).Scan(&id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		full, err := s.loadLaw(ctx, id, true)
		if errorIsNoRows(err) {
			t.Skipf("law %d not in this database", id)
		}
		if err != nil {
			t.Fatal(err)
		}
		want, err := s.lawDetailOf(ctx, full)
		if err != nil {
			t.Fatal(err)
		}
		_, got, err := s.lawDetail(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got)
		if string(w) != string(g) {
			t.Errorf("law %d: lawDetail differs from the full load:\n got %.600s\nwant %.600s", id, g, w)
		}
		t.Logf("law %d: %d articles, %d amended_by, same answer", id, len(got.Articles), len(got.AmendedBy))
	}
}

func TestNaturalLess(t *testing.T) {
	xs := []string{"10", "2", "12-1", "12", "12 مكرر", "1"}
	sortArticles(xs)
	if got := strings.Join(xs, "|"); got != "1|2|10|12|12 مكرر|12-1" && got != "1|2|10|12|12-1|12 مكرر" {
		t.Errorf("sorted %s", got)
	}
}

func TestAPIRootListsListings(t *testing.T) {
	s := testServer(t)
	var out struct {
		Endpoints []string `json:"endpoints"`
	}
	getJSON(t, s, "/api/v1/", &out)
	joined := strings.Join(out.Endpoints, "\n")
	for _, want := range []string{"/api/v1/laws/?", "/api/v1/gazette/?"} {
		if !strings.Contains(joined, want) {
			t.Errorf("apiRoot does not list %s", want)
		}
	}
}
