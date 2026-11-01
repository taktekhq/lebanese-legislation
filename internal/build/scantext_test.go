package build

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func loadScan(t *testing.T, id string) [][]string {
	t.Helper()
	b, err := os.ReadFile("testdata/scan-" + id + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var tx struct {
		Pages []struct {
			Cols []string `json:"cols"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(b, &tx); err != nil {
		t.Fatal(err)
	}
	var pages [][]string
	for _, p := range tx.Pages {
		pages = append(pages, p.Cols)
	}
	return pages
}

func TestScanSoleArticle(t *testing.T) {
	// Law 41/2026: a supplement issue, "مادة وحيدة", then الأسباب الموجبة.
	s := ParseScanText(loadScan(t, "199407"), "تمديد ولاية مجلس النواب تمديداً إستثنائياً")
	if s.GazetteNumber != "11" || s.GazetteDate == nil || s.GazetteDate.Format("2006-01-02") != "2026-03-09" {
		t.Errorf("gazette = %q %v, want 11 2026-03-09", s.GazetteNumber, s.GazetteDate)
	}
	if len(s.Articles) != 1 || s.Articles[0].Number != "وحيدة" {
		t.Fatalf("articles = %+v, want one, وحيدة", s.Articles)
	}
	if !strings.Contains(s.Articles[0].Text, "تنتهي مدة ولاية مجلس النواب") || strings.Contains(s.Articles[0].Text, "بعبدا") {
		t.Errorf("article text = %q", s.Articles[0].Text)
	}
	if !strings.HasPrefix(s.Preamble, "أقر مجلس النواب") || strings.Contains(s.Preamble, "تمديد ولاية") {
		t.Errorf("preamble = %q", s.Preamble)
	}
	if !strings.HasPrefix(s.Closing, "بعبدا في") || !strings.Contains(s.Closing, "نواف سلام") {
		t.Errorf("closing = %q", s.Closing)
	}
	if !strings.HasPrefix(s.ExplanatoryNote, "الأسباب الموجبة") {
		t.Errorf("explanatory note starts %q", cutStr(s.ExplanatoryNote, 40))
	}
	if strings.Contains(s.ExplanatoryNote, "الجريدة الرسمية - ملحق") {
		t.Errorf("page header left in the text")
	}
}

func TestScanArticlesAcrossColumns(t *testing.T) {
	// Law 46/2026: ordinal articles, a page number alone atop column two.
	s := ParseScanText(loadScan(t, "200125"), "قانون تعليق المهل")
	if s.GazetteNumber != "32" {
		t.Errorf("gazette number = %q, want 32", s.GazetteNumber)
	}
	var nums []string
	for _, a := range s.Articles {
		nums = append(nums, a.Number)
	}
	if len(nums) < 5 || nums[0] != "1" || nums[1] != "2" || nums[4] != "5" {
		t.Errorf("article numbers = %v", nums)
	}
	if !strings.Contains(s.Articles[1].Text, "العقود التي نظمتها الدولة") {
		t.Errorf("article 2 lost its continuation in column two: %q", cutStr(s.Articles[1].Text, 200))
	}
	if strings.Contains(s.Articles[1].Text, "٢٧٦٩") {
		t.Errorf("page number left in article 2")
	}
	if s.PageFrom == 0 {
		t.Errorf("no page number found")
	}
}

func TestScanAnnexGoesToClosing(t *testing.T) {
	// Law 27/2026 approves an agreement printed after the signatures: its
	// own "articles" must not become the law's.
	s := ParseScanText(loadScan(t, "198949"), "طلب الاجازة لحكومة الجمهورية اللبنانية توقيع اتفاقية المقر مع المنظمة العربية للتنمية الزراعية وصندوق لبنان للتنمية والابتكار بشأن استضافة المكتب العربي لريادة الأعمال الزراعية ومنح المكتب العربي لريادة الأعمال الزراعية حصانات ومزايا")
	if len(s.Articles) != 3 {
		var nums []string
		for _, a := range s.Articles {
			nums = append(nums, a.Number)
		}
		t.Errorf("articles = %v, want 1 2 3", nums)
	}
	if !strings.Contains(s.Closing, "اتفاقية المقر") {
		t.Errorf("the annexed agreement is missing from the closing")
	}
}

func TestPlainDropsSecondReading(t *testing.T) {
	in := "المادة " + string(markStart) + "الأولى" + string(markSep) + "الاولى" + string(markEnd) + ": نص"
	if got := plain(in); got != "المادة الأولى: نص" {
		t.Errorf("plain = %q", got)
	}
}

func cutStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func TestScanDropsPrintedByFooter(t *testing.T) {
	pages := [][]string{{"أقر مجلس النواب\nالمادة الأولى: نص القانون\nPrinted by Imprimerie Officielle"}}
	s := ParseScanText(pages, "")
	if len(s.Articles) != 1 || s.Articles[0].Text != "نص القانون" {
		t.Fatalf("article text = %+v", s.Articles)
	}
	if strings.Contains(s.Preamble+s.Closing, "Printed by") {
		t.Errorf("footer survived outside the article: %q", s.Preamble+s.Closing)
	}
}

func TestScanKeepsPrintedByInMidLine(t *testing.T) {
	pages := [][]string{{"أقر المجلس\nالمادة الأولى: Printed by clause is source text\nPrinted by Imprimerie Officielle"}}
	s := ParseScanText(pages, "")
	if len(s.Articles) != 1 || !strings.Contains(s.Articles[0].Text, "Printed by clause is source text") {
		t.Fatalf("source text was removed: %+v", s.Articles)
	}
}

func TestScanKeepsOtherPublisherText(t *testing.T) {
	pages := [][]string{{"أقر المجلس\nالمادة الأولى: Published by authority as part of text"}}
	s := ParseScanText(pages, "")
	if len(s.Articles) != 1 || !strings.Contains(s.Articles[0].Text, "Published by authority") {
		t.Fatalf("source text was removed: %+v", s.Articles)
	}
}

func TestScanFootnotesOnlyIsNotLawText(t *testing.T) {
	pages := [][]string{{"أقر المجلس\nالمادة الأولى: نص القانون\nPrinted by Imprimerie Officielle"}}
	s := ParseScanText(pages, "")
	if strings.Contains(s.Articles[0].Text, "Printed by") || strings.Contains(s.Closing, "Printed by") {
		t.Errorf("footer was published: %+v", s)
	}
}

func TestScanFootersRemovedAfterReadingMarks(t *testing.T) {
	line := "Printed" + string(markStart) + " by" + string(markSep) + " bye" + string(markEnd) + " Imprimerie"
	s := ParseScanText([][]string{{"أقر المجلس\nالمادة الأولى: نص القانون\n" + line}}, "")
	if strings.Contains(s.Articles[0].Text+s.Closing, "Printed") {
		t.Errorf("marked footer survived: %+v", s)
	}
}

func TestScanSourceFaithfulLatinIdentifiersRemain(t *testing.T) {
	pages := [][]string{{"أقر المجلس\nالمادة الأولى: VIN ABC-123 in English source clause"}}
	s := ParseScanText(pages, "")
	if len(s.Articles) != 1 || !strings.Contains(s.Articles[0].Text, "VIN ABC-123") {
		t.Fatalf("source identifier was removed: %+v", s.Articles)
	}
}

func TestScanPageNumberBesideMarkedHeader(t *testing.T) {
	// Law 41's header is "… ۲۰۲٦/۳/۹ ‹|› ۱": the readers disagreed on the bar.
	s := ParseScanText(loadScan(t, "199407"), "تمديد ولاية مجلس النواب تمديداً إستثنائياً")
	if s.PageFrom != 1 {
		t.Errorf("page from = %d, want 1", s.PageFrom)
	}
}

func TestScanTitleWithDisputedDate(t *testing.T) {
	// Law 31/2026: the title's date was read two ways, and the listing writes
	// it a third way. The enacting words still end the title.
	s := ParseScanText(loadScan(t, "198953"), "يرمي الى تعديل القانون رقم 48 تاريخ 7/9/2017 المتعلق بتنظيم الشراكة بين القطاعين العام والخاص")
	if !strings.HasPrefix(s.Preamble, "أقر مجلس النواب") {
		t.Errorf("preamble = %q", cutStr(s.Preamble, 80))
	}
	if len(s.Articles) != 15 {
		t.Errorf("articles = %d, want 15", len(s.Articles))
	}
}

func TestIssueIndexDate(t *testing.T) {
	ix := issueIndex{}
	d := func(s string) time.Time { v, _ := time.Parse("2006-01-02", s); return v }
	ix["2026|31"] = d("2026-07-23")
	ix["2026|5م"] = d("2026-01-30")
	ix["2027|1"] = d("2027-01-07")
	if got, ok := ix.date("31", d("2026-07-17")); !ok || got != d("2026-07-23") {
		t.Errorf("issue 31 = %v %v", got, ok)
	}
	if got, ok := ix.date("5", d("2026-01-20")); !ok || got != d("2026-01-30") {
		t.Errorf("supplement 5م = %v %v", got, ok)
	}
	if got, ok := ix.date("1", d("2026-12-30")); !ok || got != d("2027-01-07") {
		t.Errorf("next year's issue 1 = %v %v", got, ok)
	}
	if _, ok := ix.date("31", d("2026-08-01")); ok {
		t.Errorf("an issue before the text must not match")
	}
}

func TestScanQuotedArticleStaysInBody(t *testing.T) {
	// Law 68/2026, article 2 rewrites the Penal Code's article 37 and quotes
	// it ("المادة ٣٧:"): that is not this law's article 37.
	s := ParseScanText(loadScan(t, "200346"), "الغاء عقوبة الإعدام في لبنان")
	var nums []string
	for _, a := range s.Articles {
		nums = append(nums, a.Number)
	}
	if strings.Join(nums, " ") != "1 2 3 4 5 6" {
		t.Errorf("articles = %v, want 1-6", nums)
	}
	if !strings.Contains(s.Articles[1].Text, "المادة ٣٧") {
		t.Errorf("article 2 lost the quoted article 37")
	}
}

func TestScanClosingOnOneLine(t *testing.T) {
	// Law 62/2026: place, date and signatures read onto a single line.
	s := ParseScanText(loadScan(t, "200211"), "")
	if !strings.HasPrefix(s.Closing, "بعبدا في") {
		t.Errorf("closing = %q", cutStr(s.Closing, 60))
	}
	if strings.Contains(s.Articles[len(s.Articles)-1].Text, "بعبدا") {
		t.Errorf("the closing stayed in the last article")
	}
}

func TestScanAdoptedLawAfterSignatures(t *testing.T) {
	// Law 69/2026: "مادة وحيدة: صدّق اقتراح قانون الإعلام كما عدلته اللجان",
	// then the signatures, then the media law itself: 121 articles under
	// parts, titles and chapters. Those are this text's articles.
	s := ParseScanText(loadScan(t, "200470"), "قانون الإعلام")
	if len(s.Articles) < 110 || s.Articles[0].Number != "1" {
		t.Fatalf("articles = %d, first %q; want the media law's 121", len(s.Articles), s.Articles[0].Number)
	}
	if !strings.Contains(s.Preamble, "صدّق اقتراح قانون الإعلام") {
		t.Errorf("the sole article is not in the preamble: %q", cutStr(s.Preamble, 120))
	}
	if !strings.HasPrefix(s.Closing, "بعبدا في") || strings.Contains(s.Closing, "المادة الأولى") {
		t.Errorf("closing = %q", cutStr(s.Closing, 80))
	}
	if len(s.Sections) < 3 || s.Articles[0].Section < 0 {
		t.Fatalf("sections = %d, article 1 under %d", len(s.Sections), s.Articles[0].Section)
	}
	if h := s.Sections[s.Articles[0].Section].Heading; !strings.Contains(h, "الفصل الأول") || !strings.Contains(h, "التعريفات") {
		t.Errorf("article 1's heading = %q, want الفصل الأول: التعريفات", h)
	}
}

func TestScanGapIsNoted(t *testing.T) {
	// Law 69/2026's scans lost the headings of articles 81-83: 84 still
	// counts, and article 80 says their words run on inside it.
	s := ParseScanText(loadScan(t, "200470"), "قانون الإعلام")
	var found bool
	for i, a := range s.Articles {
		if a.Number == "80" {
			found = true
			if !strings.Contains(a.Note, "81 إلى 83") || s.Articles[i+1].Number != "84" {
				t.Errorf("article 80 note %q, next %q", a.Note, s.Articles[i+1].Number)
			}
		}
	}
	if !found {
		t.Fatal("no article 80")
	}
}

func TestScanAmendedArticleQuoted(t *testing.T) {
	// Law 64/2026 adopts an amending law: "المادة الاولى: عدّل نص المادة ٢
	// … بحيث أصبحت على الشكل التالي:" then "المادة ٢: …", the new wording.
	s := ParseScanText(loadScan(t, "200213"), "")
	if len(s.Articles) < 2 || !strings.Contains(s.Articles[0].Text, "المادة ٢:") || !strings.HasPrefix(s.Articles[1].Text, "عدل نص المادة ٣") {
		t.Errorf("article 1 = %q; article 2 = %q", cutStr(s.Articles[0].Text, 80), cutStr(s.Articles[1].Text, 40))
	}
}

func TestScanBudgetLineIsNoHeading(t *testing.T) {
	// Law 43/2026 opens a credit "الباب ١١ - وزارة التربية …": a budget line.
	s := ParseScanText(loadScan(t, "200084"), "")
	if len(s.Sections) != 0 {
		t.Errorf("headings = %+v", s.Sections)
	}
	if !strings.Contains(s.Articles[0].Text, "وزارة التربية") {
		t.Errorf("article 1 lost its budget line")
	}
}

// The 30 Oct 2026 nightly kept these three forms of the credit as article text.
func TestScanDropsBareAndMarkedPrintedBy(t *testing.T) {
	for _, line := range []string{
		"Printed by",
		string(markStart) + string(markSep) + "Printed by" + string(markEnd),
		string(markStart) + "Printed by" + string(markSep) + string(markEnd),
	} {
		s := ParseScanText([][]string{{"أقر المجلس\nالمادة الأولى: نص القانون\n" + line}}, "")
		if len(s.Articles) != 1 || strings.Contains(s.Articles[0].Text+s.Closing, "Printed") {
			t.Errorf("%q survived: %+v", line, s)
		}
	}
}

func TestScanKeepsPrintedWordsThatAreNotTheCredit(t *testing.T) {
	for _, line := range []string{"Printed byline of the decree", string(markStart) + "نص" + string(markSep) + "Printed by" + string(markEnd)} {
		s := ParseScanText([][]string{{"أقر المجلس\nالمادة الأولى: نص القانون\n" + line}}, "")
		if len(s.Articles) != 1 || !strings.Contains(s.Articles[0].Text, line) {
			t.Errorf("%q was dropped: %+v", line, s.Articles)
		}
	}
}
