package build

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// A text read from scans of the Gazette's own pages (lebgazette
// sader-bundle): two machine readings merged word by word. Where they differ,
// both are kept in a marked span: U+E000 reading one, U+E001 reading two,
// U+E002. The marks stay in the stored text; the site shows them.
const (
	markStart = ''
	markSep   = ''
	markEnd   = ''
)

// ScanText is a scanned text cut into the parts the database keeps.
type ScanText struct {
	GazetteNumber   string // digits, as UL keeps them ("11" for "ملحق العدد ١١")
	GazetteDate     *time.Time
	PageFrom        int // the Gazette's own page numbers, 0 when not printed
	PageTo          int
	Preamble        string // enacting words and recitals, before the first article
	Articles        []ScanArticle
	Sections        []ScanSection
	Closing         string // place, date and signatures, and any annex after them
	ExplanatoryNote string // الأسباب الموجبة
}

type ScanArticle struct {
	Number  string // as the database numbers articles: "1", "12 مكرر", "وحيدة"
	Note    string // ours, when the next articles' headings were not read and their words run on here
	Text    string // the article's words after "المادة …:", paragraphs split by blank lines
	Section int    // index in Sections of the heading it falls under, -1 for none
}

// ScanSection is a heading (القسم، الباب، الفصل …) between articles.
type ScanSection struct {
	Heading string
	Depth   int // 0 الكتاب/القسم, 1 الباب, 2 الفصل, 3 الفرع, 4 النبذة
}

var (
	digitsAr   = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9", "۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9")
	scanHeader = regexp.MustCompile(`الجريدة الرسمية\s*[-–—ـ]+\s*(?:ملحق\s+)?العدد\s+(\d+)\s*م?\s*[-–—ـ]+\s*(\d{4})/(\d{1,2})/(\d{1,2})`)
	pageNoRe   = regexp.MustCompile(`\d{1,5}`)
	invisible  = strings.NewReplacer("\u200e", "", "\u200f", "", "\u200b", "", "\u061c", "", "\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "", "\ufeff", "")
	bareNumber = regexp.MustCompile(`^\s*\d{1,5}\s*$`)
	// A standalone English printer credit is production furniture, not law text.
	printedBy = regexp.MustCompile(`(?i)^\s*printed\s+by(?:\s.*)?$`)
	kindLine  = regexp.MustCompile(`^\s*(?:قانون|مرسوم|قرار|تعميم|مذكرة|بيان|علم وخبر)[^:]{0,30}\s+رقم\s+\S+\s*$`)
	enacting  = regexp.MustCompile(`^\s*(?:أقر|اقر|إن |ان |بناء|بناءً|يقرر|يرسم)`)
	articleRe = regexp.MustCompile(`^\s*(?:ال)?مادة\s+(.{1,40}?)\s*[:：\-–]\s*(.*)$`)
	closingRe = regexp.MustCompile(`^\s*\S+(?:\s\S+)?\s+في\s+\d{1,2}\s+\S+(?:\s\S+)?\s+\d{4}(?:\s|$)`)
	signRe    = regexp.MustCompile(`^\s*(?:الإمضاء|الامضاء)\s*:`)
	reasonsRe = regexp.MustCompile(`^\s*(?:الأسباب|الاسباب) الموجبة\s*:?\s*$`)
)

// second is a line as reading two has it: the other side of each mark.
func second(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch r {
		case markStart:
			skip = true
		case markSep:
			skip = false
		case markEnd:
			skip = false
		default:
			if !skip {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// footer reports whether a line is the printer's credit: in reading one, or
// in reading two when reading one is empty. "Printed by" may stand alone
// (30 Oct 2026, 11 lines).
func footer(one, two string) bool {
	return printedBy.MatchString(one) || (strings.TrimSpace(one) == "" && printedBy.MatchString(two))
}

// plain is a line as reading one has it, without the marks, for finding
// headings; the stored text keeps the marks.
func plain(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch r {
		case markStart:
			skip = false
		case markSep:
			skip = true
		case markEnd:
			skip = false
		default:
			if !skip {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// ParseScanText cuts a scanned text into its parts. pages holds each page's
// columns in reading order; title is the text's title as listed (its words
// are skipped where the page prints them under the heading).
func ParseScanText(pages [][]string, title string) ScanText {
	var t ScanText
	var lines []string
	pageNos := map[int]bool{}
	for _, cols := range pages {
		for _, col := range cols {
			ls := strings.Split(col, "\n")
			for i, ln := range ls {
				p := invisible.Replace(digitsAr.Replace(plain(ln)))
				if footer(p, invisible.Replace(digitsAr.Replace(second(ln)))) {
					continue
				}
				if m := scanHeader.FindStringSubmatch(p); m != nil {
					if t.GazetteNumber == "" {
						t.GazetteNumber = strings.TrimLeft(m[1], "0")
						y, _ := strconv.Atoi(m[2])
						mo, _ := strconv.Atoi(m[3])
						d, _ := strconv.Atoi(m[4])
						if y > 1900 && mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
							at := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
							t.GazetteDate = &at
						}
					}
					// The page number sits before or after the header.
					if n := pageNoRe.FindString(strings.Replace(p, m[0], " ", 1)); n != "" {
						v, _ := strconv.Atoi(n)
						pageNos[v] = true
					}
					continue
				}
				// A page number alone at the top or foot of a column.
				if (i == 0 || i == len(ls)-1) && bareNumber.MatchString(p) {
					v, _ := strconv.Atoi(strings.TrimSpace(p))
					pageNos[v] = true
					continue
				}
				if strings.TrimSpace(ln) != "" {
					lines = append(lines, strings.TrimSpace(ln))
				}
			}
		}
	}
	if len(pageNos) > 0 {
		var ns []int
		for n := range pageNos {
			ns = append(ns, n)
		}
		sort.Ints(ns)
		t.PageFrom, t.PageTo = ns[0], ns[len(ns)-1]
	}

	// The heading ("قانون رقم ٢٧") and the title under it are already in
	// the record: skip them, up to the enacting words.
	start := 0
	if len(lines) > 0 && kindLine.MatchString(digitsAr.Replace(plain(lines[0]))) {
		start = 1
		enact := -1 // the enacting words ("أقر مجلس النواب", "إن رئيس الجمهورية") end the title
		for i := 1; i < len(lines) && i < 20; i++ {
			if enacting.MatchString(plain(lines[i])) {
				enact = i
				break
			}
			if articleRe.MatchString(plain(lines[i])) {
				break
			}
		}
		if enact > 0 {
			start = enact
		} else if ft := amend.FoldString(title); ft != "" {
			// Title lines: each one a piece of the listed title.
			for start < len(lines) {
				fl := amend.FoldString(plain(lines[start]))
				if fl == "" || !strings.Contains(ft, fl) || enacting.MatchString(plain(lines[start])) {
					break
				}
				start++
			}
		} else {
			for start < len(lines) && !enacting.MatchString(plain(lines[start])) &&
				!articleRe.MatchString(plain(lines[start])) && start < 8 {
				start++
			}
			if start == len(lines) || start == 8 { // no enacting words: keep everything after the heading
				start = 1
			}
		}
	}

	b := cutBody(lines[start:])
	// A law that adopts a law printed after its signatures ("مادة وحيدة:
	// صدّق اقتراح قانون الإعلام كما عدلته اللجان"): the adopted law's articles
	// and headings are this text's; its own sole article joins the preamble.
	if len(b.articles) == 1 && b.articles[0].Number == "وحيدة" && adopts.MatchString(plain(b.articles[0].Text)) {
		if cut := annexStart(b.closing); cut > 0 {
			if a := cutBody(b.closing[cut:]); len(a.articles) >= 3 {
				b.pre = append(append(b.pre, "مادة وحيدة: "+b.articles[0].Text), a.pre...)
				b.articles, b.sections = a.articles, a.sections
				b.closing = b.closing[:cut]
				b.reasons = append(a.closing, append(a.reasons, b.reasons...)...)
			}
		}
	}
	t.Articles, t.Sections = b.articles, b.sections
	t.Preamble = strings.Join(b.pre, "\n\n")
	t.Closing = strings.Join(b.closing, "\n\n")
	t.ExplanatoryNote = strings.Join(b.reasons, "\n\n")
	return t
}

var (
	adopts    = regexp.MustCompile(`صد[ّ]?ق\s+(?:اقتراح|مشروع)\s+(?:ال)?قانون`)
	sectionRe = regexp.MustCompile(`^\s*(الكتاب|القسم|الباب|الفصل|الفرع|النبذة)\s+\S+`)
)

// sectionWords counts heading words on a line: a budget line names two
// ("الباب ١١ - وزارة … الفصل ٣ - …"), an outline heading one.
var sectionWords = regexp.MustCompile(`(?:^|\s)(?:الكتاب|القسم|الباب|الفصل|الفرع|النبذة)\s`)

// budgetLine is a budget's classification ("الباب ١١ - وزارة التربية"), not a heading.
var budgetLine = regexp.MustCompile(`^\s*(?:الجزء|الباب|الفصل|البند|الفقرة)\s+\d+\s*[-–]`)

var mentioned = regexp.MustCompile(`المادة\s+/?([^\s/:،,]+(?:\s+(?:عشرة|عشر|مكرر))?)/?`)

// quotedArticle says whether an article heading repeats an article the line before
// names and introduces ("عدّل نص المادة ٢ … بحيث أصبحت على الشكل التالي:"):
// the amended article's new wording, not the next article of this text.
func quotedArticle(prev, num string) bool {
	if !strings.HasSuffix(strings.TrimSpace(prev), ":") {
		return false
	}
	for _, m := range mentioned.FindAllStringSubmatch(prev, -1) {
		if n, ok := amend.ArticleNumber(m[1]); ok && n == num {
			return true
		}
	}
	return false
}

var sectionDepth = map[string]int{"الكتاب": 0, "القسم": 0, "الباب": 1, "الفصل": 2, "الفرع": 3, "النبذة": 4}

type bodyParts struct {
	pre, closing, reasons []string
	articles              []ScanArticle
	sections              []ScanSection
}

// annexStart finds where an adopted law begins after the signatures: the
// line after the last signature before its first article.
func annexStart(lines []string) int {
	last := -1
	for i, ln := range lines {
		p := plain(ln)
		if signRe.MatchString(p) {
			last = i
			continue
		}
		if m := articleRe.FindStringSubmatch(p); m != nil {
			if num, ok := amend.ArticleNumber(m[1]); ok && articleInt(num) == 1 {
				break
			}
		}
	}
	return last + 1
}

// cutBody cuts lines into preamble, headings, articles, closing and the
// explanatory note.
func cutBody(lines []string) bodyParts {
	var b bodyParts
	var cur *ScanArticle
	var body []string
	flush := func() {
		if cur != nil {
			cur.Text = strings.Join(body, "\n\n")
			b.articles = append(b.articles, *cur)
		}
		cur, body = nil, nil
	}
	state := 0 // 0 preamble, 1 articles, 2 closing, 3 reasons
	lastN := 0 // the last article's number: a quoted article of another text ("المادة ٣٧:" inside article 2) is not the next one
	section := -1
	// The number of the next article heading after each line, to tell a
	// jump in the numbering (headings the readers missed) from a quote.
	following := make([]int, len(lines)+1)
	following[len(lines)] = -1
	for i := len(lines) - 1; i >= 0; i-- {
		following[i] = following[i+1]
		if m := articleRe.FindStringSubmatch(plain(lines[i])); m != nil {
			if num, ok := amend.ArticleNumber(m[1]); ok {
				following[i] = articleInt(num)
			}
		}
	}
	joinNext := false // a heading cut after its colon ("الفصل الأول:" / "التعريفات")
	prev := ""        // the line before, to tell an amended article quoted after "… بحيث أصبحت:" from the next article
	for i, ln := range lines {
		p := plain(ln)
		pd := digitsAr.Replace(p)
		if i > 0 {
			prev = plain(lines[i-1])
		}
		if joinNext {
			joinNext = false
			if len([]rune(p)) <= 60 && !articleRe.MatchString(p) && !sectionRe.MatchString(p) {
				h := &b.sections[len(b.sections)-1]
				h.Heading = strings.TrimSpace(h.Heading) + " " + strings.TrimSpace(ln)
				continue
			}
		}
		if state < 3 && reasonsRe.MatchString(p) {
			flush()
			state = 3
			b.reasons = append(b.reasons, ln)
			continue
		}
		if state <= 1 && (closingRe.MatchString(pd) || (state == 1 && signRe.MatchString(p))) {
			flush()
			state = 2
		}
		if state <= 1 {
			if m := sectionRe.FindStringSubmatch(p); m != nil && len([]rune(p)) <= 90 && headingAhead(lines, i) && len(sectionWords.FindAllString(p, -1)) == 1 && !budgetLine.MatchString(pd) {
				flush()
				state = 1
				b.sections = append(b.sections, ScanSection{Heading: strings.TrimSpace(ln), Depth: sectionDepth[m[1]]})
				section = len(b.sections) - 1
				joinNext = strings.HasSuffix(strings.TrimSpace(p), ":") || len(strings.Fields(p)) <= 2
				continue
			}
			if m := articleRe.FindStringSubmatch(p); m != nil {
				if num, ok := amend.ArticleNumber(m[1]); ok && !quotedArticle(prev, num) && (nextArticle(lastN, num) || articleInt(num) > lastN && following[i+1] == articleInt(num)+1) {
					n := articleInt(num)
					flush()
					if lastN > 0 && n > lastN+1 && len(b.articles) > 0 {
						prev := &b.articles[len(b.articles)-1]
						prev.Note = fmt.Sprintf("لم تُقرأ آليًا عناوين المواد %d إلى %d، فجاء نصّها ضمن هذه المادة: راجع صور الصفحات.", lastN+1, n-1)
						if n-1 == lastN+1 {
							prev.Note = fmt.Sprintf("لم يُقرأ آليًا عنوان المادة %d، فجاء نصّها ضمن هذه المادة: راجع صور الصفحات.", lastN+1)
						}
					}
					lastN = n
					state = 1
					cur = &ScanArticle{Number: num, Section: section}
					// The words after the colon, taken from the marked line.
					if rest := afterArticleHead(ln); rest != "" {
						body = append(body, rest)
					}
					continue
				}
			}
		}
		switch state {
		case 0:
			b.pre = append(b.pre, ln)
		case 1:
			if cur == nil { // after a heading, before its first article: never drop words
				if len(b.sections) > 0 {
					h := &b.sections[len(b.sections)-1]
					h.Heading = strings.TrimSpace(h.Heading) + " " + strings.TrimSpace(ln)
				} else {
					b.pre = append(b.pre, ln)
				}
				continue
			}
			body = append(body, ln)
		case 2:
			b.closing = append(b.closing, ln)
		case 3:
			b.reasons = append(b.reasons, ln)
		}
	}
	flush()
	return b
}

// afterArticleHead returns what follows "المادة …:" on a line, marks kept.
func afterArticleHead(ln string) string {
	for i, r := range ln {
		if r == ':' || r == '：' || r == '-' || r == '–' {
			if articleRe.MatchString(plain(ln[:i+len(string(r))])) {
				return strings.TrimSpace(ln[i+len(string(r)):])
			}
		}
	}
	return ""
}

func articleInt(num string) int {
	if num == "وحيدة" {
		return 1
	}
	n, _ := strconv.Atoi(strings.Fields(num)[0])
	return n
}

// nextArticle says whether num can follow article last: the first article,
// one to three past the last (a garbled heading must not swallow the rest),
// or a "مكرر" of the last. Anything else is another text's article quoted
// in this one.
func nextArticle(last int, num string) bool {
	n := articleInt(num)
	switch {
	case last == 0:
		return true
	case n > last && n <= last+3:
		return true
	case n == last && strings.Contains(num, "مكرر"):
		return true
	}
	return false
}

// headingAhead says whether line i is a heading in the text's outline: an
// article or another heading follows within three lines. "الباب" in a budget
// table is not.
func headingAhead(lines []string, i int) bool {
	for j := i + 1; j < len(lines) && j <= i+3; j++ {
		p := plain(lines[j])
		if articleRe.MatchString(p) || sectionRe.MatchString(p) {
			return true
		}
	}
	return false
}
