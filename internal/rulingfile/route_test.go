package rulingfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestTier(t *testing.T) {
	r := DefaultRouter
	for _, c := range []struct {
		p    TriagePage
		want string
	}{
		{TriagePage{Conf: 82, Words: 200}, TierHaiku},
		{TriagePage{Conf: 50, Words: 200}, TierSonnet},
		{TriagePage{Conf: 32, Words: 300}, TierGemini},
		{TriagePage{Conf: 20, Words: 4}, TierHaiku}, // near blank
	} {
		if got := r.Tier(1, c.p); got != c.want {
			t.Errorf("Tier(%+v) = %s, want %s", c.p, got, c.want)
		}
	}
}

func TestMerge3(t *testing.T) {
	a := "قرار رقم ٣٢٣٢ تاريخ ٢٠٠٠/٦/١٦\nوبما ان المستدعي"
	if got, v := Merge3(a, a, a); got != a || v.Disputed != 0 || v.Agreed != v.Words {
		t.Fatalf("identical readings: %q %+v", got, v)
	}
	// Haiku misreads the decree number; Opus and Sonnet agree, so no dispute at all.
	if got, _ := Merge3(a, a, strings.Replace(a, "٣٢٣٢", "٢٢٣٢", 1)); got != a {
		t.Fatalf("voter's slip leaked: %q", got)
	}
	// A and B differ on the number; the voter backs B.
	b := strings.Replace(a, "٣٢٣٢", "٣٢٢٢", 1)
	got, v := Merge3(a, b, b)
	if !strings.Contains(got, "٣٢٢٢") || strings.Contains(got, "٣٢٣٢") || v.Settled != 1 || v.Disputed != 0 {
		t.Fatalf("majority for B: %q %+v", got, v)
	}
	// Nobody backs either side: both are kept, marked.
	got, v = Merge3(a, b, strings.Replace(a, "٣٢٣٢", "٩٩٩٩", 1))
	if v.Disputed != 1 || !strings.Contains(got, markStart+"٣٢٣٢"+markSep+"٣٢٢٢"+markEnd) {
		t.Fatalf("tie: %q %+v", got, v)
	}
	// Spelling variants that don't change meaning are agreement, and A's form is kept.
	got, v = Merge3("بمـا ان إعادة", "بما ان اعادة")
	if v.Disputed != 0 || got != "بمـا ان إعادة" {
		t.Fatalf("folded agreement: %q %+v", got, v)
	}
	// Spacing inside a date and a reader's bracketed remark are not disputes.
	got, v = Merge3("تاريخ ٢١ / ٦ / ٢٠٠٢ [توقيع]", "تاريخ ٢١/٦/٢٠٠٢ [؟]")
	if v.Disputed != 0 || got != "تاريخ ٢١/٦/٢٠٠٢ [توقيع]" {
		t.Fatalf("date spacing and remarks: %q %+v", got, v)
	}
	// A two-digit year is not the same reading as a four-digit one.
	if _, v = Merge3("بتاريخ ١٠-٧-٠٢", "بتاريخ ١٠-٧-٢٠٠٢"); v.Disputed != 1 {
		t.Fatalf("02 vs 2002 must stay a dispute: %+v", v)
	}
	// A word only one reader has is a dispute settled by the voter.
	got, v = Merge3("محكمة التمييز الجزائية", "محكمة التمييز", "محكمة التمييز الجزائية")
	if got != "محكمة التمييز الجزائية" || v.Settled != 1 {
		t.Fatalf("insertion: %q %+v", got, v)
	}
}

func TestChatter(t *testing.T) {
	if !Chatter("Pages 4 to 5 are not transcribed yet. You asked me to read only these.") {
		t.Error("English talk not caught")
	}
	if Chatter("قرار رقم ٥٥ Conseil d'Etat, arrêt du 12 mai") {
		t.Error("a French citation counted as talk")
	}
	if !Chatter("I can't produce a verbatim transcription of these pages. The handwriting is dense.") {
		t.Error("a refusal not caught")
	}
	quote := "Please therfore contact Athens in this regard to change the P.&I. renewal for 1990/91, the P.&.I per trip\n" +
		"بما انه يتلخص من هذه الوقائع ان الجهة المميزة وافقت. You are kindly requested to pay the premium."
	if Chatter(quote) {
		t.Error("an English letter quoted in a judgment counted as talk")
	}
	for _, refusal := range []string{
		"I can\u2019t produce a verbatim transcription of these pages. The handwriting is dense.",
		"I\u2019m unable to provide a transcription.",
		"I am not able to transcribe this document.",
		"I cannot provide a verbatim reproduction of this copyrighted text.",
		"Sorry, the scan is illegible.",
		"I\u2019m not able to read the handwriting here. Could you provide a clearer scan?",
		"Unfortunately I do not see any text on these pages.",
	} {
		if !Chatter(refusal) {
			t.Errorf("refusal not caught: %q", refusal)
		}
	}
	for _, page := range []string{
		"Dear Sirs, we were unable to load the cargo at Piraeus. Unfortunately the vessel sailed. Could you please confirm the P.&I. cover.",
		"The Charterers shall be unable to claim any copyright in the image of the vessel. See signature on this page.",
		"Please find attached the images of the damaged cargo. Sorry for the delay.",
	} {
		if Chatter(page) {
			t.Errorf("a page that is an English letter counted as talk: %q", page)
		}
	}
	for _, refusal := range []string{"This appears to be a blank page with no text.", "The text is too blurry to read.", "No readable text.", "Sorry, I am unable to assist.", "The page is empty.", "I do not have access to the document."} {
		if !Chatter(refusal) {
			t.Errorf("refusal not caught: %q", refusal)
		}
	}
	letter := "وحيث ان الجهة المدعية ابرزت كتاباً جاء فيه ما حرفيته:\nI'm writing to inform you. Here is the invoice. Here is the cargo list.\nوبما ان"
	if Chatter(letter) {
		t.Error("an English email quoted inside an Arabic judgment counted as talk")
	}
}

func TestPipeline(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Dir(Path(dir, 9)), 0o755)
	tr := &Triage{ID: 9, Pages: []TriagePage{{N: 1, Conf: 85, Words: 100}, {N: 2, Conf: 30, Words: 200}}}
	r := DefaultRouter
	meta := Meta{Number: "55", Year: "2005"}
	head := "قرار رقم ٥٥ تاريخ ٢٠٠٥ وبما ان"
	for model, want := range map[string][]int{Haiku: {1}, Gemini: {1, 2}, Tesseract: {1}, Opus: {2}, Sonnet: {2}} {
		if got := r.Missing(tr, nil, Readings{}, model); !slices.Equal(got, want) {
			t.Fatalf("%s owes %v, want %v", model, got, want)
		}
	}
	add := func(model string, pages map[int]string) {
		t.Helper()
		if err := r.AddReadings(dir, 9, model, pages, tr, meta); err != nil {
			t.Fatal(err)
		}
	}
	add(Gemini, map[int]string{1: head, 2: "سطر بخط اليد"})
	add(Haiku, map[int]string{1: strings.Replace(head, "وبما", "ويما", 1)})
	// Before their votes, both pages show Gemini's reading alone; only the
	// handwritten one is low confidence.
	if rd, _ := LoadReading(dir, 9); rd == nil || len(rd.Pages) != 2 || Published(tr, rd) ||
		rd.Pages[0].Model != Gemini || rd.Pages[0].Text != head || rd.Pages[0].LowConfidence ||
		rd.Pages[1].Model != Gemini || rd.Pages[1].Text != "سطر بخط اليد" || !rd.Pages[1].LowConfidence {
		t.Fatalf("before Tesseract: both pages on Gemini's reading, page 2 low confidence: %+v", rd)
	}
	add(Tesseract, map[int]string{1: strings.Replace(head, "وبما", "وفما", 1)})
	rd, _ := LoadReading(dir, 9)
	if len(rd.Pages) != 2 || !slices.Equal(rd.Pages[0].Want, []string{Sonnet}) || rd.Pages[0].Disputed != 1 {
		t.Fatalf("a typed page with a dispute left should ask for Sonnet: %+v", rd.Pages)
	}
	if rd.HeaderMismatch {
		t.Fatal("a single reading must not raise a header mismatch")
	}
	rs0, _ := LoadReadings(dir, 9)
	if got := r.Missing(tr, rd, rs0, Opus); !slices.Equal(got, []int{2}) {
		t.Fatalf("the single-reading page must still be owed to Opus, got %v", got)
	}
	rs, _ := LoadReadings(dir, 9)
	if got := r.Missing(tr, rd, rs, Sonnet); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("Sonnet owes %v, want [1 2]", got)
	}
	add(Sonnet, map[int]string{1: head, 2: "سطر بخط اليد"})
	add(Opus, map[int]string{2: "سطر بخط اليد"})
	rd, _ = LoadReading(dir, 9)
	if !Published(tr, rd) || rd.Pages[0].Text != head || rd.HeaderMismatch || rd.Pages[0].LowConfidence || !rd.Pages[1].LowConfidence {
		t.Fatalf("after Sonnet and Opus: %+v", rd)
	}
	if rd.Pages[0].Model != "gemini-3.8-flash+claude-haiku-5-5+tesseract+claude-sonnet-5-5" {
		t.Fatalf("typed page readers: %q", rd.Pages[0].Model)
	}
	if rd.Pages[1].Model != "gemini-3.8-flash+claude-opus-5-5+claude-sonnet-5-5" {
		t.Fatalf("handwritten page readers: %q", rd.Pages[1].Model)
	}
}

func TestPagePlanFallbacks(t *testing.T) {
	r := DefaultRouter
	typed := TriagePage{N: 1, Conf: 85, Words: 100}
	hand := TriagePage{N: 1, Conf: 30, Words: 100}
	if pl := r.PagePlan(1, typed, map[string]string{GeminiBlocked: "2"}, nil); pl.A != Sonnet || pl.B != Haiku || slices.Contains(pl.Need, Gemini) {
		t.Errorf("typed page Gemini refused: %+v", pl)
	}
	if pl := r.PagePlan(1, hand, map[string]string{GeminiBlocked: "2"}, nil); pl.A != Opus || pl.B != Sonnet || slices.Contains(pl.Need, Gemini) {
		t.Errorf("handwritten page Gemini refused: %+v", pl)
	}
	if pl := r.PagePlan(1, typed, map[string]string{Opus: "x", Sonnet: "x", Haiku: "x"}, nil); pl.A != Opus || pl.B != Sonnet || len(pl.Need) != 3 {
		t.Errorf("page read under the first plan: %+v", pl)
	}
	r.Important = map[int]bool{1: true}
	if pl := r.PagePlan(1, typed, nil, nil); !slices.Contains(pl.Need, Opus) || !slices.Contains(pl.Need, Gemini) {
		t.Errorf("important ruling: %+v", pl)
	}
}

func TestTSVText(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t0\t0\t1\t1\t90\tمحكمة\n" +
		"5\t1\t1\t1\t1\t2\t0\t0\t1\t1\t70\tالتمييز\n" +
		"5\t1\t1\t1\t2\t1\t0\t0\t1\t1\t70\tقرار\n"
	if got := TSVText(tsv); got != "محكمة التمييز\nقرار" {
		t.Fatalf("TSVText = %q", got)
	}
}

func TestCheckHeader(t *testing.T) {
	m := Meta{Number: "368", Year: "2015", President: "شكري صادر"}
	if !CheckHeader("قرار رقم : ٢٠١٥-٢٠١٤/٣٦٨\nالرئيس : شكري صادر", m) {
		t.Error("a correct header failed")
	}
	if CheckHeader("قرار رقم : ٢٠١٥/٣٨٦\nالرئيس : شكري", m) {
		t.Error("a header with the wrong number passed")
	}
	if !CheckHeader("anything", Meta{}) {
		t.Error("no record should pass")
	}
}

func TestParseTSVConf(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t0\t0\t1\t1\t90\tمحكمة\n" +
		"5\t1\t1\t1\t1\t2\t0\t0\t1\t1\t70\tالتمييز\n" +
		"5\t1\t1\t1\t1\t3\t0\t0\t1\t1\t10\tا\n" +
		"4\t1\t1\t1\t1\t0\t0\t0\t1\t1\t-1\t\n"
	conf, n, err := ParseTSVConf(tsv)
	if err != nil || conf != 80 || n != 2 {
		t.Fatalf("ParseTSVConf = %d, %d, %v; want 80, 2", conf, n, err)
	}
}

func TestGeminiAnswerAndKeys(t *testing.T) {
	g := &GeminiBatch{Model: Gemini, PriceIn: 0.375, PriceOut: 1.875}
	line := []byte(`{"key":"85052:1,2","response":{"candidates":[{"content":{"parts":[{"text":"=== صفحة 1 ===\nأ\n=== صفحة 2 ===\nب"}]}}],"usageMetadata":{"promptTokenCount":2000000,"candidatesTokenCount":1000000}}}`)
	key, pages, usd, err := g.answer(line)
	if err != nil || key != "85052:1,2" || len(pages) != 2 || pages[2] != "ب" {
		t.Fatalf("answer = %q %v %v", key, pages, err)
	}
	if usd != 0.375*2+1.875 {
		t.Fatalf("cost = %f", usd)
	}
	id, ns := parseKey((request{id: 9, pages: []int{3, 4}}).key())
	if id != 9 || len(ns) != 2 || ns[1] != 4 {
		t.Fatalf("key round trip = %d %v", id, ns)
	}
	if s := batchState(map[string]any{"metadata": map[string]any{"state": "JOB_STATE_RUNNING"}}); s != "JOB_STATE_RUNNING" {
		t.Fatalf("batchState = %q", s)
	}
	if f := responsesFile(map[string]any{"response": map[string]any{"responsesFile": "files/x"}}); f != "files/x" {
		t.Fatalf("responsesFile = %q", f)
	}
}

func TestSubPDF(t *testing.T) {
	if _, err := exec.LookPath("pdfunite"); err != nil {
		t.Skip("needs poppler (pdfseparate, pdfunite, pdfinfo)")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.pdf")
	// Three one-page PDFs joined: build them with pdfunite from a minimal page.
	page := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj 2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj 3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 10 10]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n")
	var ones []string
	for i := range 3 {
		p := filepath.Join(dir, fmt.Sprintf("one%d.pdf", i))
		os.WriteFile(p, page, 0o644)
		ones = append(ones, p)
	}
	if out, err := exec.Command("pdfunite", append(ones, src)...).CombinedOutput(); err != nil {
		t.Skipf("pdfunite cannot build the test file: %v %s", err, out)
	}
	pages := func(b []byte) int {
		p := filepath.Join(dir, "out.pdf")
		os.WriteFile(p, b, 0o644)
		out, _ := exec.Command("pdfinfo", p).Output()
		for _, l := range strings.Split(string(out), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[0] == "Pages:" {
				n, _ := strconv.Atoi(f[1])
				return n
			}
		}
		return 0
	}
	for _, c := range []struct {
		want []int
		n    int
	}{{[]int{2, 3}, 2}, {[]int{1}, 1}, {[]int{1, 2, 3}, 3}} {
		b, err := subPDF(src, c.want)
		if err != nil {
			t.Fatal(err)
		}
		if got := pages(b); got != c.n {
			t.Errorf("subPDF(%v) has %d pages, want %d", c.want, got, c.n)
		}
	}
}

func TestAllHandwrittenNotPublishedOnOneReading(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Dir(Path(dir, 7)), 0o755)
	tr := &Triage{ID: 7, Pages: []TriagePage{{N: 1, Conf: 30, Words: 200}}}
	r := DefaultRouter
	meta := Meta{Number: "55", Year: "2005"}
	if err := r.AddReadings(dir, 7, Gemini, map[int]string{1: "نص مشوّش بلا رقم"}, tr, meta); err != nil {
		t.Fatal(err)
	}
	rd, _ := LoadReading(dir, 7)
	if len(rd.Pages) != 1 || rd.Pages[0].Voted() || !rd.Pages[0].LowConfidence || Published(tr, rd) || rd.HeaderMismatch {
		t.Fatalf("one handwritten page with Gemini only: shown, low confidence, not published, no header flag: %+v", rd)
	}
}

func TestOnePageTakesTheAnswerWhateverItsLabel(t *testing.T) {
	got := onePage(SplitPages("=== صفحة ٢ ===\nنص الصفحة"), []int{3})
	if got[3] != "نص الصفحة" || len(got) != 1 {
		t.Fatalf("one page asked, one given: %v", got)
	}
	two := SplitPages("=== صفحة 1 ===\nأ\n=== صفحة 2 ===\nب")
	if got := onePage(two, []int{1, 2}); len(got) != 2 || got[1] != "أ" {
		t.Fatalf("several pages keep their labels: %v", got)
	}
}

func TestGeminiLineTurnsSafetyOff(t *testing.T) {
	if len(geminiSafetyOff) != 5 {
		t.Fatalf("%v", geminiSafetyOff)
	}
	for _, s := range geminiSafetyOff {
		if s["threshold"] != "OFF" {
			t.Errorf("%v", s)
		}
	}
}

func TestInlinedLinesFeedAnswer(t *testing.T) {
	g := &GeminiBatch{PriceIn: 1, PriceOut: 1}
	m := map[string]any{"response": map[string]any{"inlinedResponses": map[string]any{"inlinedResponses": []any{
		map[string]any{"metadata": map[string]any{"key": "5:1"}, "response": map[string]any{
			"candidates":    []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "=== صفحة 1 ===\nنص"}}}}},
			"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5}}},
		map[string]any{"response": map[string]any{"promptFeedback": map[string]any{"blockReason": "OTHER"}}},
	}}}}
	// One answer keyed, one not: refused, so nothing lands on the wrong ruling.
	if _, _, err := inlinedLines(m, []string{"5:1", "6:2"}); err == nil {
		t.Fatal("a mix of keyed and keyless answers must be refused")
	}
	delete(m["response"].(map[string]any)["inlinedResponses"].(map[string]any)["inlinedResponses"].([]any)[0].(map[string]any), "metadata")
	lines, ok, err := inlinedLines(m, []string{"5:1", "6:2"})
	if !ok || err != nil {
		t.Fatalf("all keyless, counts match: taken in order: %v %v", ok, err)
	}
	ls := strings.Split(strings.TrimSpace(string(lines)), "\n")
	if len(ls) != 2 {
		t.Fatalf("%d lines", len(ls))
	}
	key, pages, _, err := g.answer([]byte(ls[0]))
	if err != nil || key != "5:1" || pages[1] != "نص" {
		t.Fatalf("first: %q %v %v", key, pages, err)
	}
	key, _, _, err = g.answer([]byte(ls[1]))
	if key != "6:2" || err == nil {
		t.Fatalf("second, keyless and blocked: %q %v", key, err)
	}
	if _, ok, _ := inlinedLines(map[string]any{"response": map[string]any{"responsesFile": "files/x"}}, nil); ok {
		t.Error("a file batch has no inlined responses")
	}
	if _, _, err := inlinedLines(m, []string{"5:1"}); err == nil {
		t.Error("keyless answers with a count that doesn't match the requests must be refused")
	}
	keyed := map[string]any{"response": map[string]any{"inlinedResponses": map[string]any{"inlinedResponses": []any{
		map[string]any{"metadata": map[string]any{"key": "9:9"}, "response": map[string]any{}}}}}}
	if _, _, err := inlinedLines(keyed, []string{"5:1"}); err == nil {
		t.Error("a key that was never sent must be refused")
	}
}
