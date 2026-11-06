package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
)

// The Worker's tool tests, against testdata/fixture.sql (see main_test.go).

func ids(v any) []int {
	var out []int
	list, _ := v.([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		f, _ := m["id"].(float64)
		out = append(out, int(f))
	}
	return out
}

func lawIDs(r map[string]any) []int { return ids(r["laws"]) }

func list(v any) []map[string]any {
	var out []map[string]any
	l, _ := v.([]any)
	for _, x := range l {
		m, _ := x.(map[string]any)
		out = append(out, m)
	}
	return out
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func hasArticle(r map[string]any, law int, number string) bool {
	for _, a := range list(r["articles"]) {
		l, _ := a["law"].(map[string]any)
		if num(l["id"]) == law && a["article_number"] == number {
			return true
		}
	}
	return false
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSearchLegislation(t *testing.T) {
	s := dbServer(t)
	if r := call(t, s, "search_legislation", map[string]any{"query": "الاختبار", "kind": "laws"}); !containsInt(lawIDs(r.result), 100) {
		t.Errorf("a title word: %v", r.result)
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "الخامسة", "kind": "articles"}); !hasArticle(r.result, 100, "5") {
		t.Errorf("an article word: %v", r.result)
	}
	r := call(t, s, "search_legislation", map[string]any{"query": "تمييزي", "kind": "rulings"})
	rulings := list(r.result["rulings"])
	if len(rulings) == 0 || num(rulings[0]["id"]) != 9000 || rulings[0]["pdf_url"] != "https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=9000&type=list" {
		t.Errorf("a ruling: %v", r.result)
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "123", "kind": "gazette"}); !containsInt(ids(r.result["gazette"]), 500) {
		t.Errorf("a gazette number: %v", r.result)
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "x", "kind": "bogus"}); !r.isError {
		t.Error("an unknown kind is an error")
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "الاختبار"}); !strings.Contains(r.advice, "Not legal advice") {
		t.Error("the not-legal-advice line")
	}
}

func TestGetLaw(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "get_law", map[string]any{"id": 100}).result
	if num(r["id"]) != 100 || r["kind_en"] != "Law" || r["articles_truncated"] != nil {
		t.Errorf("law 100: %v", r)
	}
	if got := mustMarshal(r["articles"]); got != `[{"number":"5","position":1,"url":"https://lebaneselegislation.com/law/100/article/5/"}]` {
		t.Errorf("articles: %s", got)
	}
	if got := mustMarshal(r["table_of_contents"]); got != `[]` {
		t.Errorf("table of contents: %s", got)
	}
	r = call(t, s, "get_law", map[string]any{"id": 400}).result
	toc := `[{"depth":0,"heading":"الباب الاول: احكام عامة","id":41},{"depth":1,"heading":"الفصل الاول","id":42,"parent_id":41}]`
	if got := mustMarshal(r["table_of_contents"]); got != toc {
		t.Errorf("table of contents: %s", got)
	}
	var pairs [][]any
	for _, a := range list(r["articles"]) {
		pairs = append(pairs, []any{a["number"], num(a["section_id"])})
	}
	if got := fmt.Sprint(pairs); got != "[[6 41] [7 42] [7-1 42]]" {
		t.Errorf("sections: %s", got)
	}
	// updated_at is when the page last changed (page_state); none when unknown
	if r := call(t, s, "get_law", map[string]any{"id": 100}).result; r["updated_at"] != "2026-10-30" {
		t.Errorf("law 100 updated_at %v", r["updated_at"])
	}
	if r := call(t, s, "get_law", map[string]any{"id": 200}).result; r["updated_at"] != nil {
		t.Errorf("law 200 has no page_state row, updated_at %v", r["updated_at"])
	}
	if r := call(t, s, "get_article", map[string]any{"law_id": 100, "number": "5"}).result; r["updated_at"] != "2026-10-30" ||
		r["law"].(map[string]any)["updated_at"] != "2026-10-30" {
		t.Errorf("article 5 of law 100: updated_at %v, law %v", r["updated_at"], r["law"])
	}
	if !call(t, s, "get_law", map[string]any{"id": 999999}).isError {
		t.Error("a missing law is an error")
	}
}

func TestGetArticle(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "get_article", map[string]any{"law_id": 100, "number": "5"}).result
	changes := list(r["changes"])
	if r["text_ar"] != "نص المادة الخامسة من قانون الاختبار" || !strings.Contains(fmt.Sprint(r["text_fr"]), "Texte de l'article") || len(changes) != 1 {
		t.Fatalf("article 100/5: %v", r)
	}
	c := changes[0]
	by, _ := c["by"].(map[string]any)
	if c["relation"] != "amend" || num(by["id"]) != 200 || c["by_article"] != "1" || c["text_ar"] != "تعدل المادة الخامسة من قانون الاختبار كما يلي" {
		t.Errorf("change: %v", c)
	}
	aff := list(r["possibly_affected_by"])
	if len(aff) != 1 || num(aff[0]["by"].(map[string]any)["id"]) != 300 {
		t.Errorf("possibly affected: %v", aff)
	}
	if r["centre_text_ar"] != nil || r["version"] != "1.0" {
		t.Errorf("unamended: %v", r)
	}

	r = call(t, s, "get_article", map[string]any{"law_id": 400, "number": "6"}).result
	if r["text_ar"] != "نص المادة السادسة الجديد" || r["text_fr"] != nil || r["version"] != "2.0" || r["current_since"] != "2023-12-28" ||
		r["centre_text_ar"] != "نص المادة السادسة القديم" || !strings.Contains(fmt.Sprint(r["centre_text_fr"]), "Ancien texte") || r["repealed"] != false {
		t.Errorf("amended article: %v", r)
	}
	if got := mustMarshal(r["centre_error"]); got != `{"ar":"يكرر نص المادة 5.","en":"The Centre's text repeats article 5."}` {
		t.Errorf("centre error: %s", got)
	}
	r = call(t, s, "get_article", map[string]any{"law_id": 400, "number": "7"}).result
	if r["text_ar"] != nil || r["repealed"] != true || r["centre_text_ar"] != "نص المادة السابعة" {
		t.Errorf("repealed article: %v", r)
	}
	if !call(t, s, "get_article", map[string]any{"law_id": 100, "number": "999"}).isError {
		t.Error("a missing article is an error")
	}
	for _, asked := range []string{"7-1", "7 (1)", "7/1", "7 - 1", "٧-١", "المادة 7-1"} {
		r := call(t, s, "get_article", map[string]any{"law_id": 400, "number": asked})
		if r.isError || r.result["number"] != "7-1" || r.result["text_ar"] != "يبرم العقد خطيا" || num(r.result["section_id"]) != 42 {
			t.Errorf("%q: %v", asked, r.result)
		}
	}
}

func TestGetLawTexts(t *testing.T) {
	s := dbServer(t)
	first := call(t, s, "get_law_texts", map[string]any{"law_id": 400, "limit": 2}).result
	arts := list(first["articles"])
	next, _ := first["next_cursor"].(string)
	if num(first["total"]) != 3 || next == "" || first["offset"] != nil || len(arts) != 2 || arts[0]["number"] != "6" || arts[1]["number"] != "7" {
		t.Fatalf("first page: %v", first)
	}
	if arts[0]["text_ar"] != "نص المادة السادسة الجديد" || arts[0]["centre_text_ar"] != "نص المادة السادسة القديم" || num(arts[0]["section_id"]) != 41 ||
		!strings.Contains(fmt.Sprint(arts[0]["centre_error"]), "repeats article 5") || arts[1]["centre_error"] != nil || arts[1]["text_ar"] != nil || arts[1]["repealed"] != true {
		t.Errorf("first page texts: %v", arts)
	}
	second := call(t, s, "get_law_texts", map[string]any{"law_id": 400, "cursor": next, "limit": 2}).result
	arts = list(second["articles"])
	if len(arts) != 1 || arts[0]["number"] != "7-1" || arts[0]["text_fr"] != "Le contrat est conclu par écrit." || second["next_cursor"] != nil || num(second["total"]) != 3 {
		t.Errorf("second page: %v", second)
	}
	if !call(t, s, "get_law_texts", map[string]any{"law_id": 999999}).isError {
		t.Error("a missing law is an error")
	}
}

func TestSearchBeyondArabic(t *testing.T) {
	s := dbServer(t)
	for _, q := range []string{"contrat", "contrats", "écrit", "ecrit"} {
		if r := call(t, s, "search_legislation", map[string]any{"query": q, "kind": "articles"}); !hasArticle(r.result, 400, "7-1") {
			t.Errorf("French %q: %v", q, r.result)
		}
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "100", "kind": "laws"}); lawIDs(r.result)[0] != 100 {
		t.Errorf("by id: %v", r.result)
	}
	for _, q := range []string{"Code des essais", "essais", "test code"} {
		if r := call(t, s, "search_legislation", map[string]any{"query": q, "kind": "laws"}); !containsInt(lawIDs(r.result), 400) {
			t.Errorf("code name %q: %v", q, r.result)
		}
	}
	for _, q := range []string{"Penal Code", "penal code", "Code pénal", "code penal", "penal"} {
		if r := call(t, s, "search_legislation", map[string]any{"query": q, "kind": "laws"}); lawIDs(r.result)[0] != 430 {
			t.Errorf("closest code %q: %v", q, lawIDs(r.result))
		}
	}
	if r := call(t, s, "search_legislation", map[string]any{"query": "criminal procedure", "kind": "laws"}); lawIDs(r.result)[0] != 420 {
		t.Errorf("criminal procedure: %v", lawIDs(r.result))
	}
}

func TestGetRulingAndGazette(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "get_ruling", map[string]any{"id": 9000}).result
	if r["pdf_url"] != "https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=9000&type=list" || r["title"] != "حكم تمييزي رقم 15" || r["court_en"] != "Court of Cassation" ||
		mustMarshal(r["members"]) != `["قاض اول","قاض ثان"]` || !strings.Contains(fmt.Sprint(r["text_ar"]), "قانون الاختبار") {
		t.Errorf("ruling 9000: %v", r)
	}
	if r["pages"] != nil || r["pages_note"] != nil {
		t.Errorf("a ruling with no pages read: %v", r)
	}
	const note = "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference."
	r = call(t, s, "get_ruling", map[string]any{"id": 9001}).result
	if got := mustMarshal(r["pages"]); got != `[{"n":1,"text_ar":"نص ⟦واحد | اثنان⟧"},{"low_confidence":true,"n":2,"text_ar":"صعب"}]` {
		t.Errorf("pages: %s", got)
	}
	if r["pages_note"] != note+" Pages with low_confidence: true are hard to read (mostly handwriting): expect errors." || r["pdf_url"] == nil {
		t.Errorf("pages note: %v", r)
	}
	if !strings.Contains(fmt.Sprint(r["note"]), "scanned file only") {
		t.Errorf("a scanned-only judgment: %v", r["note"])
	}
	r = call(t, s, "get_ruling", map[string]any{"id": 9002}).result
	if mustMarshal(r["pages"]) != `[{"n":1,"text_ar":"نص"}]` || r["pages_note"] != note || r["note"] != "The Centre holds this judgment as a file only, not as typed text." {
		t.Errorf("readable pages: %v", r)
	}
	g := call(t, s, "get_gazette_issue", map[string]any{"id": 500}).result
	if g["number"] != "123" || !containsInt(ids(g["texts"]), 100) {
		t.Errorf("gazette 500: %v", g)
	}
	if r := call(t, s, "get_ruling", map[string]any{"id": 9000}).result; r["updated_at"] != "2026-10-30" {
		t.Errorf("ruling 9000 updated_at %v", r["updated_at"])
	}
	if g["updated_at"] != nil {
		t.Errorf("gazette 500: updated_at %v from a date before the site existed", g["updated_at"])
	}
}

func TestGetLinks(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "get_links", map[string]any{"key": "law/100/art/5", "direction": "to"}).result
	links := list(r["links"])
	if len(links) != 2 || links[0]["relation"] != "amend" || links[0]["from_key"] != "law/200/art/1" {
		t.Errorf("into an article: %v", r)
	}
	r = call(t, s, "get_links", map[string]any{"key": "law/200/art/1", "direction": "from"}).result
	if links := list(r["links"]); len(links) != 2 || links[0]["to_key"] != "law/100/art/5" {
		t.Errorf("out of an article: %v", r)
	}
	if !call(t, s, "get_links", map[string]any{"key": "not-a-key"}).isError {
		t.Error("a malformed key is an error")
	}

	toKeys := func(r map[string]any) string {
		var ks []string
		for _, l := range list(r["links"]) {
			ks = append(ks, fmt.Sprint(l["to_key"]))
		}
		return strings.Join(ks, " ")
	}
	r = call(t, s, "get_links", map[string]any{"key": "law/430", "direction": "to"}).result
	if r["scope"] != "all" || r["order"] != "newest" || num(r["total"]) != 4 || r["next_cursor"] != nil ||
		toKeys(r) != "law/430/art/37 law/430/art/99 law/430 law/430/art/5" || list(r["links"])[0]["date"] != "2026-09-10" {
		t.Errorf("a law's links: %v", r)
	}
	r = call(t, s, "get_links", map[string]any{"key": "law/430", "direction": "to", "scope": "law"}).result
	if num(r["total"]) != 1 || toKeys(r) != "law/430" {
		t.Errorf("scope law: %v", r)
	}
	first := call(t, s, "get_links", map[string]any{"key": "law/430", "order": "oldest", "limit": 2}).result
	if toKeys(first) != "law/430 law/430/art/99" || first["next_cursor"] == nil {
		t.Errorf("oldest first: %v", first)
	}
	second := call(t, s, "get_links", map[string]any{"key": "law/430", "order": "oldest", "limit": 2, "cursor": first["next_cursor"]}).result
	if toKeys(second) != "law/430/art/37 law/430/art/5" || second["next_cursor"] != nil {
		t.Errorf("second page: %v", second)
	}
	r = call(t, s, "get_links", map[string]any{"key": "law/333455", "direction": "from", "relation": "insert"}).result
	if num(r["total"]) != 11 || !strings.HasPrefix(fmt.Sprint(list(r["links"])[0]["to_key"]), "law/190374/art/") {
		t.Errorf("direction from: %v", r)
	}
	all := call(t, s, "get_links", map[string]any{"key": "law/430", "limit": 200})
	for _, l := range list(all.result["links"]) {
		switch l["to_key"] {
		case "law/430/art/99":
			if runeLen(fmt.Sprint(l["quote"])) != 601 || l["quote_truncated"] != true {
				t.Errorf("long quote: %d runes, truncated %v", runeLen(fmt.Sprint(l["quote"])), l["quote_truncated"])
			}
		case "law/430/art/37":
			if l["quote_truncated"] != nil {
				t.Error("a short quote is not cut")
			}
		}
	}
	r = call(t, s, "get_links", map[string]any{"key": "law/430/art/37", "scope": "all"}).result
	if num(r["total"]) != 1 || r["scope"] != nil {
		t.Errorf("an article key: %v", r)
	}
	for _, a := range []map[string]any{{"key": "law/430", "order": "sideways"}, {"key": "law/430", "scope": "everything"}} {
		if !call(t, s, "get_links", a).isError {
			t.Errorf("%v is an error", a)
		}
	}
}

func TestLawLevelLinks(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "get_law", map[string]any{"id": 190374}).result
	by := list(r["amended_by"])
	if len(by) != 1 || by[0]["relation"] != "insert" || by[0]["verb"] != "Added to" || num(by[0]["law"].(map[string]any)["id"]) != 333455 ||
		by[0]["effective_on"] != "2025-05-16" || mustMarshal(by[0]["articles"]) != `["3","4","5","6","7","8","9","10","11","12"]` {
		t.Errorf("Labour Code amended_by: %v", by)
	}
	r = call(t, s, "get_law", map[string]any{"id": 333455}).result
	am := list(r["amended"])
	if len(am) != 1 || num(am[0]["law"].(map[string]any)["id"]) != 190374 || len(am[0]["articles"].([]any)) != 10 || mustMarshal(r["amended_by"]) != "[]" {
		t.Errorf("amended: %v", r)
	}
	r = call(t, s, "get_law", map[string]any{"id": 430}).result
	var got []string
	for _, e := range list(r["amended_by"]) {
		got = append(got, fmt.Sprintf("%v %d %v", e["relation"], num(e["law"].(map[string]any)["id"]), e["articles"]))
	}
	if strings.Join(got, "; ") != "replace 200 <nil>; amend 300 [99]; amend 2000200346 [37]" {
		t.Errorf("Penal Code amended_by: %v", got)
	}
	scan := call(t, s, "get_law", map[string]any{"id": 2000200346}).result
	am = list(scan["amended"])
	if len(am) != 1 || am[0]["relation"] != "amend" || mustMarshal(am[0]["articles"]) != `["37"]` || am[0]["effective_on"] != "2026-09-10" {
		t.Errorf("scan amended: %v", am)
	}
	r = call(t, s, "get_law", map[string]any{"id": 100}).result
	by = list(r["amended_by"])
	if len(by) != 1 || by[0]["relation"] != "amend" || mustMarshal(by[0]["articles"]) != `["5"]` || num(by[0]["law"].(map[string]any)["id"]) != 200 {
		t.Errorf("law 100 amended_by: %v", by)
	}
}

func TestListLegislation(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "list_legislation", map[string]any{"since": "2025"}).result
	laws := list(r["laws"])
	if r["since"] != "2025" || r["order"] != "newest" || num(r["total"]) != 3 || fmt.Sprint(lawIDs(r)) != "[2000200347 2000200346 333455]" {
		t.Fatalf("since 2025: %v", r)
	}
	if laws[1]["date"] != "2026-09-10" || laws[1]["gazette_number"] != "36" || laws[1]["gazette_date"] != "2026-09-10" || num(laws[1]["gazette_issue_id"]) != 2002026036 || laws[2]["date"] != "2025-05-16" {
		t.Errorf("dates: %v", laws)
	}
	if got := lawIDs(call(t, s, "list_legislation", map[string]any{"since": "2025-05", "until": "2025-05"}).result); fmt.Sprint(got) != "[333455]" {
		t.Errorf("a month: %v", got)
	}
	if got := lawIDs(call(t, s, "list_legislation", map[string]any{"since": "2025", "kind": "decree"}).result); fmt.Sprint(got) != "[2000200347]" {
		t.Errorf("English kind: %v", got)
	}
	if n := num(call(t, s, "list_legislation", map[string]any{"since": "2025", "kind": "مرسوم"}).result["total"]); n != 1 {
		t.Errorf("Arabic kind: %d", n)
	}
	if got := lawIDs(call(t, s, "list_legislation", map[string]any{"since": "2025", "order": "oldest"}).result); got[0] != 333455 {
		t.Errorf("oldest: %v", got)
	}
	p1 := call(t, s, "list_legislation", map[string]any{"since": "2025", "limit": 2}).result
	p2 := call(t, s, "list_legislation", map[string]any{"since": "2025", "limit": 2, "cursor": p1["next_cursor"]}).result
	if fmt.Sprint(lawIDs(p1)) != "[2000200347 2000200346]" || p1["next_cursor"] == nil || fmt.Sprint(lawIDs(p2)) != "[333455]" || p2["next_cursor"] != nil || num(p2["total"]) != 3 {
		t.Errorf("pages: %v %v", p1, p2)
	}
	if containsInt(lawIDs(call(t, s, "list_legislation", map[string]any{"limit": 100}).result), 600600) {
		t.Error("a text with no date is left out")
	}
	for _, a := range []map[string]any{{"since": "2025-13"}, {"since": "last year"}, {"until": "2025-02-30"}, {"since": "2026", "until": "2025"}, {"order": "sideways"}} {
		r := call(t, s, "list_legislation", a)
		if _, isStr := r.result["error"].(string); !r.isError || !isStr {
			t.Errorf("%v: %v", a, r.result)
		}
	}
}

func TestListGazetteIssues(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "list_gazette_issues", map[string]any{"since": "2025"}).result
	issues := list(r["issues"])
	if num(r["total"]) != 3 || fmt.Sprint(ids(r["issues"])) != "[2002026036 601 600]" {
		t.Fatalf("since 2025: %v", r)
	}
	if got := mustMarshal(issues[0]); got != `{"id":2002026036,"number":"36","published_on":"2026-09-10","source":"scans","text_count":2,"url":"https://lebaneselegislation.com/gazette/2002026036/"}` {
		t.Errorf("an issue: %s", got)
	}
	if num(issues[1]["text_count"]) != 0 || issues[2]["source"] != "centre" || num(issues[2]["text_count"]) != 1 {
		t.Errorf("counts: %v", issues)
	}
	if got := ids(call(t, s, "list_gazette_issues", map[string]any{"since": "2025-05-20", "until": "2025-05"}).result["issues"]); fmt.Sprint(got) != "[601]" {
		t.Errorf("range: %v", got)
	}
	p := call(t, s, "list_gazette_issues", map[string]any{"order": "oldest", "limit": 2}).result
	q := call(t, s, "list_gazette_issues", map[string]any{"order": "oldest", "limit": 2, "cursor": p["next_cursor"]}).result
	if fmt.Sprint(ids(p["issues"])) != "[500 600]" || p["next_cursor"] == nil || fmt.Sprint(ids(q["issues"])) != "[601 2002026036]" || q["next_cursor"] != nil {
		t.Errorf("pages: %v %v", p, q)
	}
	for _, a := range []map[string]any{{"since": "2025/01"}, {"until": "x"}} {
		if !call(t, s, "list_gazette_issues", a).isError {
			t.Errorf("%v is an error", a)
		}
	}
}

func TestSearchGazette(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "search_legislation", map[string]any{"query": "2025", "kind": "gazette"}).result
	if fmt.Sprint(ids(r["gazette"])) != "[601 600]" || num(r["gazette_count"]) != 2 {
		t.Errorf("a year: %v", r)
	}
	if got := ids(call(t, s, "search_legislation", map[string]any{"query": "22/2025", "kind": "gazette"}).result["gazette"]); fmt.Sprint(got) != "[600]" {
		t.Errorf("22/2025: %v", got)
	}
	if n := num(call(t, s, "search_legislation", map[string]any{"query": "22/2026", "kind": "gazette"}).result["gazette_count"]); n != 0 {
		t.Errorf("22/2026: %d", n)
	}
	if got := ids(call(t, s, "search_legislation", map[string]any{"query": "36", "kind": "gazette", "year": 2026}).result["gazette"]); fmt.Sprint(got) != "[2002026036]" {
		t.Errorf("36 in 2026: %v", got)
	}
	if n := num(call(t, s, "search_legislation", map[string]any{"query": "36", "kind": "gazette", "year": 2025}).result["gazette_count"]); n != 0 {
		t.Errorf("36 in 2025: %d", n)
	}
	r = call(t, s, "search_legislation", map[string]any{"query": "١٢٣", "kind": "gazette"}).result
	if num(r["gazette_count"]) != 1 || r["q"] != "123" || r["searched_instead"] != nil {
		t.Errorf("Arabic-Indic digits: %v", r)
	}
}

func TestPossiblyAffected(t *testing.T) {
	s := dbServer(t)
	r := call(t, s, "possibly_affected", map[string]any{"law_id": 100}).result
	by := list(r["possibly_affected_by"])
	if len(by) != 1 || num(by[0]["by"].(map[string]any)["id"]) != 300 {
		t.Errorf("possibly affected: %v", r)
	}
}

func TestSearchRescue(t *testing.T) {
	s := dbServer(t)
	search := func(a map[string]any) map[string]any { return call(t, s, "search_legislation", a).result }
	if r := search(map[string]any{"query": "qqqzzz"}); num(r["law_count"]) != 0 || r["searched_instead"] != nil {
		t.Errorf("nonsense: %v", r)
	}
	for _, c := range []struct {
		q, via string
		id     int
	}{{"civil marriage", "الزواج المدني", 501}, {"banking secrecy", "السرية المصرفية", 502}} {
		r := search(map[string]any{"query": c.q})
		if r["searched_instead"] != c.via || r["searched_instead_kind"] != "translated" || !containsInt(lawIDs(r), c.id) {
			t.Errorf("%s: %v", c.q, r)
		}
	}
	r := search(map[string]any{"query": "513 هيئة إدارة السير"})
	if r["searched_instead"] != "هيئة إدارة السير 513" || r["searched_instead_kind"] != "adjusted" || fmt.Sprint(lawIDs(r)) != "[503]" {
		t.Errorf("a number: %v", r)
	}
	r = search(map[string]any{"query": "تعيين في وزارة الخارجية 2024"})
	if !strings.Contains(fmt.Sprint(r["searched_instead"]), "2024") || fmt.Sprint(lawIDs(r)) != "[504]" || r["article_count"] != nil {
		t.Errorf("a year: %v", r)
	}
	r = search(map[string]any{"query": "Chehab", "kind": "laws"})
	if fmt.Sprint(lawIDs(r)) != "[506]" || r["searched_instead"] != "shehab" {
		t.Errorf("a name: %v", r)
	}
	r = search(map[string]any{"query": "قانون الاختبار"})
	if r["searched_instead"] != nil || !containsInt(lawIDs(r), 100) {
		t.Errorf("no rescue needed: %v", r)
	}
}

func TestPinnedQuestions(t *testing.T) {
	s := dbServer(t)
	search := func(a map[string]any) map[string]any { return call(t, s, "search_legislation", a).result }
	for _, q := range []string{"cybercrime", "cyber crime", "cybercrimes", "Cyber-Crime", "information crimes", "computer crimes", "Lebanon computer crimes",
		"الجرائم المعلوماتية", "الجرائم الالكترونية", "الجرائم الإلكترونية", "جرائم المعلوماتية", "جرائم الكترونية"} {
		got := lawIDs(search(map[string]any{"query": q, "kind": "laws"}))
		n := 0
		for _, id := range got {
			if id == 278573 {
				n++
			}
		}
		if len(got) == 0 || got[0] != 278573 || n != 1 {
			t.Errorf("%s: %v", q, got)
		}
		if all := lawIDs(search(map[string]any{"query": q})); len(all) == 0 || all[0] != 278573 {
			t.Errorf("%s, every kind: %v", q, all)
		}
	}
	if got := lawIDs(search(map[string]any{"query": "cybercrime", "kind": "laws"})); fmt.Sprint(got) != "[278573 507]" {
		t.Errorf("after the pin: %v", got)
	}
	if containsInt(lawIDs(search(map[string]any{"query": "cybercrime", "kind": "laws", "year": 1999})), 278573) {
		t.Error("pinned for another year")
	}
	if containsInt(lawIDs(search(map[string]any{"query": "قانون الاختبار", "kind": "laws"})), 278573) {
		t.Error("pinned for other words")
	}
}

func TestEnglishTitles(t *testing.T) {
	s := dbServer(t)
	search := func(a map[string]any) map[string]any { return call(t, s, "search_legislation", a).result }
	for _, l := range list(search(map[string]any{"query": "قانون الاختبار", "kind": "laws"})["laws"]) {
		if num(l["id"]) == 100 && (l["title_en"] != "The test law" || l["title"] != "قانون الاختبار") {
			t.Errorf("law 100: %v", l)
		}
	}
	if other := list(search(map[string]any{"query": "قانون الزواج المدني", "kind": "laws"})["laws"]); len(other) == 0 || other[0]["title_en"] != nil {
		t.Errorf("no English title: %v", other)
	}
	r := search(map[string]any{"query": "information crimes", "kind": "laws"})
	if fmt.Sprint(lawIDs(r)) != "[278573 500]" || r["searched_instead"] != nil || list(r["laws"])[1]["title_en"] != "Law on information crimes" {
		t.Errorf("by English title: %v", r)
	}
	if got := lawIDs(search(map[string]any{"query": "test", "kind": "laws"})); len(got) == 0 || got[0] != 400 || !containsInt(got, 100) {
		t.Errorf("below Arabic matches: %v", got)
	}
	if call(t, s, "get_law", map[string]any{"id": 100}).result["title_en"] != "The test law" {
		t.Error("get_law title_en")
	}
}

func TestWordFormsAndCodes(t *testing.T) {
	s := dbServer(t)
	search := func(a map[string]any) map[string]any { return call(t, s, "search_legislation", a).result }
	r := search(map[string]any{"query": "والخامسة", "kind": "articles"})
	if !strings.Contains(fmt.Sprint(r["searched_instead"]), "any of its forms") || !hasArticle(r, 100, "5") {
		t.Errorf("another form: %v", r)
	}
	if r := search(map[string]any{"query": "والتمييزي", "kind": "rulings"}); !containsInt(ids(r["rulings"]), 9000) {
		t.Errorf("a ruling by another form: %v", r)
	}
	keys := func(r map[string]any) []string {
		var out []string
		for _, a := range list(r["articles"]) {
			out = append(out, fmt.Sprintf("%d/%v", num(a["law"].(map[string]any)["id"]), a["article_number"]))
		}
		return out
	}
	if got := keys(search(map[string]any{"query": "قانون معدل 7-1 6", "kind": "articles"})); len(got) < 2 || got[0] != "400/7-1" || got[1] != "400/6" {
		t.Errorf("numbered articles of a named code: %v", got)
	}
	if got := keys(search(map[string]any{"query": "المادة 6 قانون معدل الجديد", "kind": "articles"})); len(got) == 0 || got[0] != "400/6" {
		t.Errorf("without الجديد: %v", got)
	}
	if got := keys(search(map[string]any{"query": "المادة 6 معدل", "kind": "articles"})); len(got) == 0 || !strings.HasPrefix(got[0], "400/") {
		t.Errorf("without قانون: %v", got)
	}
	r = search(map[string]any{"query": "قانون معدل الخامسة", "kind": "articles"})
	if r["articles_not_in_code"] != "قانون معدل" || keys(r)[0] != "100/5" {
		t.Errorf("not in the code: %v", r)
	}
	if got := keys(search(map[string]any{"query": "المادة 6 من القانون المعدل", "kind": "articles"})); len(got) == 0 || got[0] != "400/6" {
		t.Errorf("المادة first: %v", got)
	}
	if r := search(map[string]any{"query": "تبليغ المدعى عليه بالذات الجلسة الأولى التبليغ في محل الإقامة انعقاد المحاكمة"}); mustMarshal(r["gazette"]) != "[]" {
		t.Errorf("a long question: %v", r["gazette"])
	}
	if r := search(map[string]any{"query": "الجرائم المعلوماتية 2018", "kind": "laws", "year": 2018}); !containsInt(lawIDs(r), 500) {
		t.Errorf("the filter year in titles: %v", r)
	}
	if r := search(map[string]any{"query": "اتفاق الجرائم المعلوماتية", "kind": "laws", "year": 2018}); !containsInt(lawIDs(r), 500) || r["searched_instead"] == nil {
		t.Errorf("rescue with a year: %v", r)
	}
	if r := search(map[string]any{"query": "قانون المعلوماتيةغائب الجرائم", "kind": "laws", "year": 2018}); !containsInt(lawIDs(r), 500) {
		t.Errorf("the kind word kept: %v", r)
	}
	r = search(map[string]any{"query": "نص الخامسة قانون غائبكلمة", "kind": "articles"})
	if len(list(r["articles"])) == 0 || strings.Contains(fmt.Sprint(r["searched_instead"]), "غائبكلمة") {
		t.Errorf("the word that keeps every text out: %v", r)
	}
}

// The calculated current amounts of the Code of Civil Procedure: the
// fixture's article 11 is the real published text, so the reviewed
// calculation applies to it.
func TestCalculatedCurrentDB(t *testing.T) {
	s := dbServer(t)
	a := call(t, s, "get_article", map[string]any{"law_id": 244565, "number": "11"}).result
	c, _ := a["calculated_current"].(map[string]any)
	if c == nil || c["kind"] != "statutory_monetary_adjustment" || num(c["multiplier"]) != 50 || c["is_published_wording"] != false || c["source_text_ar"] != a["text_ar"] {
		t.Fatalf("calculated_current: %v", a)
	}
	texts := call(t, s, "get_law_texts", map[string]any{"law_id": 244565}).result
	if !reflect.DeepEqual(list(texts["articles"])[0]["calculated_current"], a["calculated_current"]) {
		t.Errorf("get_law_texts: %v", texts)
	}
	// A word only the calculated text has finds the article, as the
	// snapshot indexed it.
	rec := calculatedcurrent.Calculate(calculatedcurrent.Input{LawID: 244565, Number: "11", Source: fmt.Sprint(a["text_ar"]), State: "in_force"})
	if rec == nil {
		t.Fatal("no calculation for article 11")
	}
	has := setOf(ftsTokens(arNorm(rec.SourceTextAr))...)
	word := ""
	for _, tok := range ftsTokens(arNorm(rec.TextAr)) {
		if !has[tok] && runeLen(tok) > 3 {
			word = tok
			break
		}
	}
	r := call(t, s, "search_legislation", map[string]any{"query": word, "kind": "articles"}).result
	hits := list(r["articles"])
	if len(hits) != 1 || !reflect.DeepEqual(hits[0]["calculated_current"], a["calculated_current"]) || hits[0]["snippet"] != a["text_ar"] {
		t.Errorf("search %q: %v", word, r)
	}
}

func TestToolsAgainstFixtureAreJSON(t *testing.T) {
	s := dbServer(t)
	// every tool answers with the not-legal-advice wrapper
	for name, a := range map[string]map[string]any{
		"search_legislation": {"query": "قانون"}, "get_law": {"id": 430}, "get_article": {"law_id": 430, "number": "37"},
		"get_law_texts": {"law_id": 430}, "get_ruling": {"id": 9000}, "get_gazette_issue": {"id": 600}, "get_links": {"key": "law/430"},
		"list_legislation": {}, "list_gazette_issues": {}, "possibly_affected": {"law_id": 430},
	} {
		r := call(t, s, name, a)
		if r.isError || r.advice != NotLegalAdvice {
			t.Errorf("%s: %v", name, r.result)
		}
	}
	_ = context.Background
	_ = json.Valid
}

func TestWarm(t *testing.T) {
	s := dbServer(t)
	if err := s.Warm(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.englishMem.corpus == nil || len(s.englishMem.corpus.docs) != 4 || s.frenchMem.corpus == nil || len(s.frenchMem.corpus.docs) != 3 {
		t.Errorf("warmed: %+v %+v", s.englishMem.corpus, s.frenchMem.corpus)
	}
}
