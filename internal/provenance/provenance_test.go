package provenance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/build"
)

// fakeSources serves texts by "kind/id" and counts reads.
type fakeSources struct {
	texts map[string]string
	reads map[string]int
}

func (f *fakeSources) Text(kind, id string) (string, error) {
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[kind+"/"+id]++
	switch kind {
	case build.SourceUL, build.SourceSaderlex, build.SourceAmendment:
	default:
		return "", ErrUnknownKind
	}
	t, ok := f.texts[kind+"/"+id]
	if !ok {
		return "", ErrNotInArchive
	}
	return t, nil
}

const (
	pageText = "المادة الأولى: يُحدد عدد محامي الدولة بسبعة وعشرين محامياً.\nالمادة 2: ينشر هذا المرسوم ويبلغ حيث تدعو الحاجة."
	amending = "تعدل المادة الأولى من المرسوم رقم 11148 بحيث يصبح نصها كما يلي: «يحدد عدد محامي الدولة بثلاثين محاميا»."
)

func sources() *fakeSources {
	return &fakeSources{texts: map[string]string{
		"ul/lawtext/1":     pageText,
		"amendment/2/1":    amending,
		"saderlex/197135":  "المادة 1: يتوجب رسم الطابع.",
		"ul/lawtext/empty": "",
	}}
}

func ul(id string) Source { return Source{build.SourceUL, "lawtext/" + id} }

func TestCheckSpanPasses(t *testing.T) {
	texts := []ArticleText{
		{LawID: 1, Article: "1", Text: "يحدد عدد محامي الدولة بسبعة وعشرين محاميا.", Source: ul("1")}, // diacritics dropped, still a span
		{LawID: 1, Article: "2", Text: "ينشر هذا   المرسوم\nويبلغ حيث تدعو الحاجة.", Source: ul("1")},
		{LawID: 1, Article: "3", Text: "", Source: ul("1")},
	}
	if vs := Check(texts, sources(), ApprovedEdits{}, Known{}); len(vs) != 0 {
		t.Fatalf("want no violation, got %v", vs)
	}
}

// A span starts and ends on the source's word boundaries: a piece of a
// word is not the source's wording. A number glued to the next word is two
// words, and so is a word glued to a quotation mark. Whole words that change
// the meaning by what they leave out (a negation, an exception) are still a
// span: telling those apart needs the article's own text, which the plan's
// contract does not ask (README).
func TestCheckSpanOnWordBoundaries(t *testing.T) {
	src := &fakeSources{texts: map[string]string{
		"ul/lawtext/1": pageText,
		"ul/lawtext/9": "المادة 12يعاقب كل من خالف «أحكام»هذا القانون.\nالمادة 13: لا يجوز للمحكمة أن تحكم بالإعدام إلا في الجرائم الخطيرة.",
	}}
	for _, c := range []struct {
		name, law, text string
		blocked         bool
	}{
		{"whole words", "1", "عدد محامي الدولة", false},
		{"starts inside a word", "1", "حدد عدد محامي الدولة", true},
		{"ends inside a word", "1", "يحدد عدد محامي الدو", true},
		{"inside one word", "1", "حامي", true},
		{"a number glued to the word after it", "9", "يعاقب كل من خالف", false},
		{"a word glued to a quotation mark", "9", "أحكام", false},
		{"the word after the quotation mark", "9", "هذا القانون.", false},
		{"a piece of a quoted word", "9", "حكام", true},
		{"pieces of two words", "9", "جوز للمحك", true},
		{"a dropped negation, in whole words", "9", "يجوز للمحكمة أن تحكم بالإعدام", false},
		{"punctuation only", "9", ".", false},
	} {
		vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: c.text, Source: ul(c.law)}}, src, ApprovedEdits{}, Known{})
		if got := len(vs) == 1 && vs[0].Reason == "not in source ul/lawtext/"+c.law; got != c.blocked {
			t.Errorf("%s (%q): blocked %v, want %v (%+v)", c.name, c.text, got, c.blocked, vs)
		}
	}
}

func TestCheckOneRewrittenWord(t *testing.T) {
	texts := []ArticleText{
		{LawID: 1, Article: "1", Text: "يحدد عدد محامي الدولة بثمانية وعشرين محاميا.", Source: ul("1")},
		{LawID: 1, Article: "2", Text: "ينشر هذا المرسوم ويبلغ حيث تدعو الحاجة.", Source: ul("1")},
	}
	vs := Check(texts, sources(), ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].LawID != 1 || vs[0].Article != "1" || vs[0].Reason != "not in source ul/lawtext/1" {
		t.Fatalf("want one violation for law 1 article 1, got %+v", vs)
	}
	msg := (&BlockedError{Violations: vs}).Error()
	if !strings.Contains(msg, "law 1 article 1") {
		t.Errorf("message does not name the article: %q", msg)
	}
}

// Words hidden in markup in a stored text are still its words: a comment's
// body and a tag's attribute values must be in the source too. Markup that
// hides nothing (a <br>, a bare <span>) passes.
func TestCheckWordsHiddenInMarkup(t *testing.T) {
	for _, c := range []struct {
		name, text string
		blocked    bool
	}{
		{"a comment's body", "يحدد عدد محامي الدولة <!-- بثلاثين محاميا ويستثنى القضاة --> بسبعة وعشرين محاميا.", true},
		{"an attribute's value", `يحدد عدد محامي الدولة <span title="ويستثنى القضاة">بسبعة</span> وعشرين محاميا.`, true},
		{"an unquoted attribute", `يحدد عدد محامي الدولة <span title=القضاة>بسبعة</span> وعشرين محاميا.`, true},
		{"a tag inside a comment", `يحدد عدد <!-- <b title="القضاة">x</b> --> محامي الدولة`, true},
		{"an encoded comment", "يحدد عدد &lt;!-- القضاة --&gt; محامي الدولة", true},
		{"a bare tag", "يحدد عدد محامي الدولة<br/> بسبعة <span>وعشرين</span> محاميا.", false},
		{"an attribute that reads on in the source", `يحدد عدد <span title="محامي">الدولة</span> بسبعة`, false},
	} {
		vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: c.text, Source: ul("1")}}, sources(), ApprovedEdits{}, Known{})
		if got := len(vs) == 1 && vs[0].Reason == "not in source ul/lawtext/1"; got != c.blocked {
			t.Errorf("%s: blocked %v, want %v (%+v)", c.name, got, c.blocked, vs)
		}
	}
}

func TestCheckApprovedEditPasses(t *testing.T) {
	text := "يحدد عدد محامي الدولة بثمانية وعشرين محاميا."
	p := writeFile(t, "approved_edits.tsv", "edit_id\tlaw_id\tarticle\ttext_sha256\tapprover\tdate\n"+
		"e1\t1\t1\t"+sha(text)+"\treviewer\t2026-10-24\n")
	edits, err := LoadApprovedEdits(p)
	if err != nil {
		t.Fatal(err)
	}
	texts := []ArticleText{
		{LawID: 1, Article: "1", Text: text, Source: ul("1")},
		{LawID: 1, Article: "1", VersionID: 1001, Text: text, Source: Source{build.SourceEdit, "e1"}},
	}
	if vs := Check(texts, sources(), edits, Known{}); len(vs) != 0 {
		t.Fatalf("an approved edit should pass, got %v", vs)
	}
	// The same hash for another article does not pass.
	vs := Check([]ArticleText{{LawID: 1, Article: "2", Text: text, Source: ul("1")}}, sources(), edits, Known{})
	if len(vs) != 1 {
		t.Fatalf("an edit approved for article 1 passed article 2: %v", vs)
	}
	// An edit source with no approved hash is a violation.
	vs = Check([]ArticleText{{LawID: 1, Article: "1", Text: text + " ", Source: Source{build.SourceEdit, "e1"}}}, sources(), edits, Known{})
	if len(vs) != 1 || vs[0].Reason != "not in source edit/e1" {
		t.Fatalf("want not in source edit/e1, got %+v", vs)
	}
}

func TestCheckKnownRowExactTextOnly(t *testing.T) {
	text := "نص لا يوجد في المصدر"
	p := writeFile(t, "provenance_known.tsv", "law_id\tarticle\ttext_sha256\ttask\n1\t1\t"+sha(text)+"\tkarim-0042\n")
	known, err := LoadKnown(p)
	if err != nil {
		t.Fatal(err)
	}
	if vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: text, Source: ul("1")}}, sources(), ApprovedEdits{}, known); len(vs) != 0 {
		t.Fatalf("a known text should pass, got %v", vs)
	}
	// A later rewrite of the same article still blocks.
	vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: text + " معدل", Source: ul("1")}}, sources(), ApprovedEdits{}, known)
	if len(vs) != 1 {
		t.Fatalf("a rewrite of a known article passed: %v", vs)
	}
}

func TestCheckAmendment(t *testing.T) {
	src := Source{build.SourceAmendment, "2/1"}
	if vs := Check([]ArticleText{{LawID: 1, Article: "1", VersionID: 2000, Text: "يحدد عدد محامي الدولة بثلاثين محاميا", Source: src}},
		sources(), ApprovedEdits{}, Known{}); len(vs) != 0 {
		t.Fatalf("an amendment's span should pass, got %v", vs)
	}
	vs := Check([]ArticleText{{LawID: 1, Article: "1", VersionID: 2000, Text: "يحدد عدد محامي الدولة بأربعين محاميا", Source: src}},
		sources(), ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].Reason != "not in source amendment/2/1" {
		t.Fatalf("want not in source amendment/2/1, got %+v", vs)
	}
}

func TestCheckReasons(t *testing.T) {
	texts := []ArticleText{
		{LawID: 5, Article: "1", Text: "نص", Source: ul("404")},
		{LawID: 6, Article: "1", Text: "نص", Source: Source{"model", "x"}},
		{LawID: 7, Article: "1", Text: "نص", Source: Source{}},
	}
	vs := Check(texts, sources(), ApprovedEdits{}, Known{})
	want := []string{"source ul/lawtext/404 not in archive", `unknown source kind "model"`, "unknown source kind"}
	if len(vs) != len(want) {
		t.Fatalf("got %+v", vs)
	}
	for i, v := range vs {
		if v.Reason != want[i] {
			t.Errorf("law %d: reason %q, want %q", v.LawID, v.Reason, want[i])
		}
	}
}

func TestCheckReadsEachSourceOnce(t *testing.T) {
	src := sources()
	var texts []ArticleText
	for i := 0; i < 5; i++ {
		texts = append(texts, ArticleText{LawID: 1, Article: fmt.Sprint(i), Text: "ينشر هذا المرسوم", Source: ul("1")},
			ArticleText{LawID: 3, Article: fmt.Sprint(i), Text: "يتوجب رسم الطابع", Source: Source{build.SourceSaderlex, "197135"}})
	}
	if vs := Check(texts, src, ApprovedEdits{}, Known{}); len(vs) != 0 {
		t.Fatal(vs)
	}
	for k, n := range src.reads {
		if n != 1 {
			t.Errorf("%s read %d times", k, n)
		}
	}
}

// Every kind a writer stores is one the guard knows.
func TestWriterKindsKnown(t *testing.T) {
	for _, k := range build.SourceKinds {
		vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: "x", Source: Source{k, "nothing"}}}, sources(), ApprovedEdits{}, Known{})
		for _, v := range vs {
			if strings.HasPrefix(v.Reason, "unknown source kind") {
				t.Errorf("writer kind %q unknown to the guard", k)
			}
		}
	}
}

// The model-writer case: a build-path writer stores model text with
// source_kind ul, a real source id, and a hash it computed itself. The
// guard reads the words, so it blocks it, and the writer cannot vouch for
// the text by adding a row of its own to the edit or known files.
func TestCheckBlocksModelWriter(t *testing.T) {
	src := &ArchiveSources{Root: "testdata/archive", Bundle: "testdata/bundle"}
	page, err := src.Text(build.SourceUL, "lawtext/8225")
	if err != nil {
		t.Fatal(err)
	}
	stored, ownHash := modelWriter(page)
	if !strings.Contains(stored.Text, "بموجب مرسوم") {
		t.Fatalf("the fixture's model did not rewrite: %q", stored.Text)
	}
	edits := writeFile(t, "approved_edits.tsv", "edit_id\tlaw_id\tarticle\ttext_sha256\tapprover\tdate\n"+
		"m1\t8225\t1\t"+ownHash+"\t\t2026-10-24\n")
	if _, err := LoadApprovedEdits(edits); err == nil || !strings.Contains(err.Error(), "no approver") {
		t.Fatalf("an edit row with no approver was not refused: %v", err)
	}
	known := writeFile(t, "provenance_known.tsv", "law_id\tarticle\ttext_sha256\ttask\n8225\t1\t"+ownHash+"\t\n")
	if _, err := LoadKnown(known); err == nil || !strings.Contains(err.Error(), "no task") {
		t.Fatalf("a known row with no task was not refused: %v", err)
	}
	vs := Check([]ArticleText{stored}, src, ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].LawID != 8225 || vs[0].Reason != "not in source ul/lawtext/8225" {
		t.Fatalf("model text under a real source passed: %+v", vs)
	}
}

// modelWriter is the fixture writer: it takes the first article of the
// page, has "a model" reword it, and stores the result as law 8225 article
// 1 with the page as its source, and its own hash of the text.
func modelWriter(page string) (ArticleText, string) {
	first, _, _ := strings.Cut(page, "\n")
	words := strings.Fields(first)
	words[len(words)/2] = "بموجب مرسوم"
	text := strings.Join(words, " ")
	return ArticleText{LawID: 8225, Article: "1", Text: text, Source: Source{build.SourceUL, "lawtext/8225"}}, sha(text)
}

func TestLoadApprovedEditsFiles(t *testing.T) {
	// The repository's own files load, and the correction file's rows
	// approve exactly their wording.
	edits, err := LoadApprovedEdits("../../db/approved_edits.tsv", "../../db/article_corrections.tsv", "../../db/centre_errors.tsv")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ln := range strings.Split(db.ArticleCorrections, "\n") {
		f := strings.Split(ln, "\t")
		if len(f) != 4 || f[0] == "law_id" || strings.HasPrefix(ln, "#") {
			continue
		}
		n++
		var law int64
		fmt.Sscan(f[0], &law)
		if !edits.Has(law, f[1], sha(build.CorrectionText(f[2]))) {
			t.Errorf("correction %s/%s not approved", f[0], f[1])
		}
	}
	if n == 0 || edits.Len() != n {
		t.Errorf("approved %d wordings, want the %d corrections (centre_errors approves none)", edits.Len(), n)
	}
	if _, err := LoadKnown("../../db/provenance_known.tsv"); err != nil {
		t.Fatal(err)
	}
}

func TestBlockedError(t *testing.T) {
	var vs []Violation
	for i := 1; i <= 25; i++ {
		vs = append(vs, Violation{LawID: int64(i), Article: "1", Reason: "not in source ul/lawtext/1"},
			Violation{LawID: int64(i), Article: "1", VersionID: 2000, Reason: "not in source amendment/9/1"})
	}
	var err error = &BlockedError{Violations: vs}
	if !errors.Is(err, ErrBlocked) {
		t.Error("not ErrBlocked")
	}
	var coded interface{ ExitCode() int }
	if !errors.As(err, &coded) || coded.ExitCode() != 4 {
		t.Error("exit code is not 4")
	}
	if c, ok := ErrBlocked.(interface{ ExitCode() int }); !ok || c.ExitCode() != 4 {
		t.Error("ErrBlocked's exit code is not 4")
	}
	lines := strings.Split(err.Error(), "\n")
	if lines[0] != "wording guard: 25 articles without source" {
		t.Errorf("first line %q", lines[0])
	}
	if len(lines) != 22 || lines[1] != "law 1 article 1: not in source ul/lawtext/1" || !strings.Contains(lines[21], "5 more") {
		t.Errorf("lines: %q", lines)
	}
}

func TestStripInternal(t *testing.T) {
	s := StripInternal(db.Schema + "\n" + db.AmendmentSchema)
	if strings.Contains(s, "source_kind") || strings.Contains(s, Marker) {
		t.Error("the published schema has the provenance columns")
	}
	if !strings.Contains(s, "source_id   integer NOT NULL") {
		t.Error("legislation_scan's own source_id was dropped")
	}
	if !strings.Contains(db.Schema, "source_kind") || !strings.Contains(db.AmendmentSchema, "source_kind") {
		t.Error("the schema lacks the provenance columns")
	}
	for _, c := range [][2]string{{"legislation_article", "source_kind"}, {"article_version", "source_id"}} {
		if !Internal(c[0], c[1]) {
			t.Errorf("%s.%s not internal", c[0], c[1])
		}
	}
	if Internal("legislation_scan", "source_id") {
		t.Error("legislation_scan.source_id is public")
	}
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The static check sees the real writers write, so finding nothing in the
// repository means something: each writer file writes law text, and the
// files that read model output for the build (internal/modelout) write none.
func TestScanSeesRealWriters(t *testing.T) {
	for _, w := range WriterFiles {
		writes, reads := scanGo(filepath.Join("../..", w))
		if len(writes) == 0 {
			t.Errorf("%s: no law-text write seen", w)
		}
		if len(reads) != 0 {
			t.Errorf("%s reads model output: %v", w, reads)
		}
	}
	for _, f := range []string{"internal/modelout/judgements.go", "internal/modelout/graph.go", "internal/modelout/rulings.go",
		"internal/modelout/titles.go"} {
		writes, reads := scanGo(filepath.Join("../..", f))
		if len(reads) == 0 || len(writes) != 0 {
			t.Errorf("%s: reads %v, writes %v; want model reads and no law-text write", f, reads, writes)
		}
	}
}
