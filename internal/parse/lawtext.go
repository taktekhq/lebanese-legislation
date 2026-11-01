package parse

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/taktekhq/lebanese-legislation/internal/page"
	"github.com/taktekhq/lebanese-legislation/internal/text"
)

// LawText is LawView.aspx: a whole text, consolidated, on one page.
//
// Inside #divTreeDetails the page is a flat run of <h2> headings and
// <div class="text-1"> bodies. A heading holding <a name="Section_N"> is a
// chapter (N is the Centre's LawTreeSectionID); its indentation gives its
// depth (centred = top, then padding 20px, 30px … 70px). A heading reading
// "المادة N" is an article, and the text-1 body after it is that article's
// text: Arabic paragraphs, then the French in direction:ltr blocks.
type LawText struct {
	Title        string
	PrintedCount *int // the page's own "عدد المواد", to check against
	Sections     []Section
	Articles     []Article
	Closing      *string
	Repeats      int // verbatim repeats of an article the page printed, dropped
}

// Counted is how many articles the page's own count covers: it leaves out
// the promulgation articles (مواد اصدار) some codes carry.
func (t *LawText) Counted() int {
	n := 0
	for _, a := range t.Articles {
		if !strings.Contains(a.Number, "اصدار") {
			n++
		}
	}
	return n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type Section struct {
	ID       int
	ParentID *int
	Position int
	Depth    int
	Heading  string
	Note     *string // the Centre's editorial note on it, printed as an article "N *" (see editorNote)
}

type Article struct {
	Position  int
	CentreID  *int // LawArticleID, when the heading links to its own page
	SectionID *int
	Number    string
	NumberInt *int
	Note      *string
	TextAr    string
	TextFr    *string
	Images    []string // absolute URLs of pictures of the text (some laws are scans)

	before []string // headings of the chapters that open between the previous article and this one
}

var (
	articleHead = regexp.MustCompile(`^\s*ال?ماد[ةه]\s*`)
	sectionName = regexp.MustCompile(`^Section_(\d+)$`)
	articleHref = regexp.MustCompile(`(?i)LawArticleID=(\d+)`)
	padding     = regexp.MustCompile(`padding-right\s*:\s*(\d+)px`)
	// After the number come the Centre's annotations: "(عدلت بموجب …)",
	// "( اضيفت بموجب: …)", "مادة ملغاة (الغيت بموجب …)".
	noteStart = regexp.MustCompile(`\(|مادة ملغاة|ملغاة`)
	dashBis   = regexp.MustCompile(`\s*-\s*`)
	bisNumber = regexp.MustCompile(`مكرر\s*(\d+)`)
	countRe   = regexp.MustCompile(`\d+`)

	// subNumber is an article inserted after another one and numbered under
	// it: "12 - 1", "586 - (1)". Its number is written "12-1".
	subNumber = regexp.MustCompile(`^\s*(\d+)\s*-\s*\(?(\d{1,2})\)?(?:\s|$)`)
	// brokenRepeal is a heading whose repeal note lost its start in the
	// Centre's markup ("<a>المادة 173 </a>/1948</span>)"): the article page
	// reads "(مادة ملغاة) (تشريع الالغاء 0 /1948)".
	brokenRepeal = regexp.MustCompile(`^\s*(\d+)\s*/\s*(\d{4})\s*\)\s*(.*)$`)
)

// ParseLawText reads a LawView.aspx page.
func ParseLawText(raw []byte) (*LawText, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, err
	}
	t := &LawText{Title: d.Text("litLaw")}
	if m := countRe.FindString(d.Text("litArticlesCount")); m != "" {
		n, _ := strconv.Atoi(m)
		t.PrintedCount = &n
	}
	if n := d.ID("signatureLaw"); n != nil {
		var lines []string
		for _, p := range page.Paragraphs(n, nil) {
			lines = append(lines, p.Text)
		}
		t.Closing = text.Opt(strings.Join(lines, "\n"))
	}
	body := d.ID("divTreeDetails")
	if body == nil {
		return t, nil
	}

	type rawSection struct {
		Section
		level int // raw indentation: 0 for centred, else the padding in px
	}
	var secs []rawSection
	var arts []Article
	var pending *Article // an article heading waiting for its body
	var current *int     // the chapter articles currently fall under
	var opened []string  // chapter headings since the last article heading

	flush := func() {
		if pending != nil {
			arts = append(arts, *pending)
			pending = nil
		}
	}

	// One walk in document order. Loose markup can nest a body inside an
	// unclosed heading, or a heading inside a body, so the walk always goes
	// on into children; each heading's own text and each body's paragraphs
	// leave out whatever is nested in them.
	page.Walk(body, func(c *html.Node) bool {
		switch {
		case page.Is(c, atom.H2):
			flush()
			label := page.Squeeze(strings.Join(ownText(c), " "))
			if sid, ok := sectionID(c); ok {
				secs = append(secs, rawSection{Section: Section{ID: sid, Heading: label}, level: level(c)})
				id := sid
				current = &id
				opened = append(opened, label)
			} else if articleHead.MatchString(label) {
				a := Article{SectionID: current, before: opened}
				opened = nil
				a.Number, a.Note = articleNumber(articleHead.ReplaceAllString(label, ""))
				a.NumberInt = text.LeadingInt(a.Number)
				if m := articleHref.FindStringSubmatch(hrefs(c)); m != nil {
					v, _ := strconv.Atoi(m[1])
					a.CentreID = &v
				}
				pending = &a
			}
		case isBody(c):
			ar, fr := split(c)
			if pending != nil {
				pending.TextAr, pending.TextFr, pending.Images = ar, fr, images(c)
				flush()
			}
		}
		return true
	})
	flush()

	// The Centre's body of an article sometimes runs on into the heading of
	// the chapter that follows it (Code of Civil Procedure 173 ends with
	// "الفرع الاول - في انار الخط …", the next chapter's heading with a
	// typo): cut it.
	for i := 1; i < len(arts); i++ {
		for k := len(arts[i].before) - 1; k >= 0; k-- {
			t, ok := cutHeading(arts[i-1].TextAr, arts[i].before[k])
			if !ok {
				break
			}
			arts[i-1].TextAr = t
		}
	}
	// An "article" numbered "143 *" whose text is the editors' note on a
	// part added later ("اضيف هذا الجزء … بموجب …") is a note on its chapter.
	notes := map[int]string{}
	kept := arts[:0]
	for _, a := range arts {
		if a.SectionID != nil && editorNote(a) {
			notes[*a.SectionID] = strings.TrimSpace(notes[*a.SectionID] + "\n" + a.TextAr)
			continue
		}
		kept = append(kept, a)
	}
	arts = kept

	// Depth: rank of the heading's indentation among those this text uses.
	levels := map[int]bool{}
	for _, s := range secs {
		levels[s.level] = true
	}
	var order []int
	for l := range levels {
		order = append(order, l)
	}
	sort.Ints(order)
	rank := map[int]int{}
	for i, l := range order {
		rank[l] = i
	}
	type open struct{ depth, id int }
	var stack []open
	for i, s := range secs {
		s.Depth = rank[s.level]
		s.Position = i + 1
		for len(stack) > 0 && stack[len(stack)-1].depth >= s.Depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			p := stack[len(stack)-1].id
			s.ParentID = &p
		}
		stack = append(stack, open{s.Depth, s.ID})
		if n, ok := notes[s.ID]; ok {
			s.Note = &n
		}
		t.Sections = append(t.Sections, s.Section)
	}
	// The Centre's page sometimes repeats an article verbatim (one law shows
	// its article 14 five times); keep the first copy.
	seen := map[string]bool{}
	for _, a := range arts {
		key := a.Number + "\x00" + a.TextAr + "\x00" + deref(a.TextFr) + "\x00" + strings.Join(a.Images, " ")
		if seen[key] {
			t.Repeats++
			continue
		}
		seen[key] = true
		a.Position = len(t.Articles) + 1
		t.Articles = append(t.Articles, a)
	}
	return t, nil
}

// articleNumber splits a heading's tail into the number as cited ("9 مكرر",
// "14 مكرر 2", "1 اصدار") and the annotations that follow it.
func articleNumber(tail string) (string, *string) {
	if m := brokenRepeal.FindStringSubmatch(tail); m != nil {
		return m[1], text.Opt(page.Squeeze("مادة ملغاة (تشريع الالغاء 0 /" + m[2] + ") " + m[3]))
	}
	if m := subNumber.FindStringSubmatchIndex(tail); m != nil {
		num := tail[m[2]:m[3]] + "-" + tail[m[4]:m[5]]
		return num, text.Opt(page.Squeeze(tail[m[1]:]))
	}
	num, note := tail, ""
	if loc := noteStart.FindStringIndex(tail); loc != nil && loc[0] > 0 {
		num, note = tail[:loc[0]], tail[loc[0]:]
	}
	num = dashBis.ReplaceAllString(strings.TrimSpace(num), " ")
	num = bisNumber.ReplaceAllString(num, "مكرر $1")
	num = page.Squeeze(num)
	if num == "" {
		num, note = page.Squeeze(tail), ""
	}
	return num, text.Opt(page.Squeeze(note))
}

func isBody(n *html.Node) bool {
	return page.Is(n, atom.Div) && strings.HasPrefix(page.Attr(n, "class"), "text-1")
}

// nestedIn reports whether n is a heading or body inside another one.
func nestedIn(outer *html.Node) func(*html.Node) bool {
	return func(n *html.Node) bool { return n != outer && (page.Is(n, atom.H2) || isBody(n)) }
}

// ownText is a heading's text without any heading or body nested in it.
func ownText(h *html.Node) []string {
	var out []string
	skip := nestedIn(h)
	page.Walk(h, func(n *html.Node) bool {
		if skip(n) {
			return false
		}
		if n.Type == html.TextNode {
			out = append(out, n.Data)
		}
		return true
	})
	return out
}

// split separates an article body into its Arabic and French texts by the
// script of each paragraph's letters. The Centre marks the French with
// direction:ltr, but its editors sometimes put the Arabic in those blocks too
// (Code of Obligations art. 487, 719, 759), so the marker only decides
// paragraphs with no letters at all. Nested headings and bodies are left out.
func split(body *html.Node) (string, *string) {
	var ar, fr []string
	for _, p := range page.Paragraphs(body, nestedIn(body)) {
		if french(p) {
			fr = append(fr, p.Text)
		} else {
			ar = append(ar, p.Text)
		}
	}
	return page.Join(ar), text.Opt(page.Join(fr))
}

// images lists the pictures in a body, as absolute URLs on the Centre's site.
func images(body *html.Node) []string {
	var out []string
	skip := nestedIn(body)
	page.Walk(body, func(n *html.Node) bool {
		if skip(n) {
			return false
		}
		if page.Is(n, atom.Img) {
			if src := strings.TrimSpace(page.Attr(n, "src")); src != "" {
				if !strings.HasPrefix(src, "http") {
					src = "https://legallaw.ul.edu.lb/" + strings.TrimLeft(src, "/")
				}
				out = append(out, src)
			}
		}
		return true
	})
	return out
}

func french(p page.Para) bool {
	switch page.Script(p.Text) {
	case page.LatinScript:
		return true
	case page.ArabicScript:
		return false
	}
	return p.LTR
}

func sectionID(h *html.Node) (int, bool) {
	id, ok := 0, false
	skip := nestedIn(h)
	page.Walk(h, func(n *html.Node) bool {
		if skip(n) {
			return false
		}
		if page.Is(n, atom.A) {
			if m := sectionName.FindStringSubmatch(page.Attr(n, "name")); m != nil {
				id, _ = strconv.Atoi(m[1])
				ok = true
			}
		}
		return !ok
	})
	return id, ok
}

func level(h *html.Node) int {
	if m := padding.FindStringSubmatch(page.Attr(h, "style")); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0 // centred, or no indentation: top level
}

func hrefs(n *html.Node) string {
	var b strings.Builder
	skip := nestedIn(n)
	page.Walk(n, func(c *html.Node) bool {
		if skip(c) {
			return false
		}
		if page.Is(c, atom.A) {
			b.WriteString(page.Attr(c, "href"))
			b.WriteByte(' ')
		}
		return true
	})
	return b.String()
}

// chapterLabel is how a chapter heading opens: a chapter word, an ordinal,
// or a number.
var chapterLabel = regexp.MustCompile(`^\s*\(?(?:ال)?(?:باب|فصل|فرع|قسم|جزء|كتاب|نبذة|نبذه|مبحث|بند|اولا|أولا|ثانيا|ثالثا|رابعا|خامسا|سادسا|سابعا|ثامنا|تاسعا|عاشرا)|^\s*\(?[0-9٠-٩]`)

// editorStar is the number the Centre gives its editors' notes printed as
// articles ("143 *"); addedPart is how such a note opens.
var (
	editorStar = regexp.MustCompile(`^\d+ \*$`)
	addedPart  = regexp.MustCompile(`^\s*(?:ا|أ)ضيف(?:ت)? (?:هذا الجزء|هذا القسم|هذا الفصل|هذا الباب|الباب|الفصل|القسم|الجزء)`)
)

// editorNote says whether a is the editors' note on a chapter, not an
// article: a starred number, no French, and a short text saying the part was
// added by a later text.
func editorNote(a Article) bool {
	return editorStar.MatchString(strings.TrimSpace(a.Number)) && a.TextFr == nil && len(a.Images) == 0 &&
		len([]rune(a.TextAr)) < 300 && addedPart.MatchString(a.TextAr)
}

// letters is s's Arabic letters and digits alone, folded (أ إ آ → ا, ة → ه,
// ى → ي), with each one's byte offset in s.
func letters(s string) ([]rune, []int) {
	var rs []rune
	var at []int
	for i, r := range s {
		switch r {
		case 'أ', 'إ', 'آ':
			r = 'ا'
		case 'ة':
			r = 'ه'
		case 'ى':
			r = 'ي'
		}
		if r >= 0x0621 && r <= 0x064A || r >= '0' && r <= '9' || r >= 0x0660 && r <= 0x0669 {
			rs = append(rs, r)
			at = append(at, i)
		}
	}
	return rs, at
}

// cutHeading takes heading off the end of body when body ends with it,
// spelled the same up to punctuation, spacing and a typo or two (one edit in
// 12 letters). Short headings, or a body that would be left with almost
// nothing, are never cut.
func cutHeading(body, heading string) (string, bool) {
	h, _ := letters(heading)
	b, at := letters(body)
	if len(h) < 10 || len(b) < len(h)+20 {
		return body, false
	}
	allowed := max(1, len(h)/12)
	best, bestN := allowed+1, 0
	for n := len(h) - allowed; n <= len(h)+allowed; n++ {
		if n <= 0 || n > len(b)-20 {
			continue
		}
		// The closest match, and the longest of equally close ones.
		if d := editDistance(b[len(b)-n:], h, allowed); d <= allowed && d <= best {
			best, bestN = d, n
		}
	}
	if bestN == 0 {
		return body, false
	}
	// A heading that is only a name ("مجلس المندوبين") can also close a
	// sentence ("… عبر مندوبيها في مجلس المندوبين."): cut it only after a
	// full stop or a line break. One that opens with a chapter word or a
	// number is a heading wherever it falls.
	cut := strings.TrimRight(body[:at[len(b)-bestN]], " \t\u00a0")
	if !chapterLabel.MatchString(heading) && cut != "" && !strings.ContainsAny(cut[len(cut)-1:], ".:\n") &&
		!strings.HasSuffix(cut, "؛") {
		return body, false
	}
	return strings.TrimRight(cut, " \t\n\u00a0-–ـ:"), true
}

// editDistance is the Levenshtein distance between a and b, or limit+1 once
// it is sure to be over limit.
func editDistance(a, b []rune, limit int) int {
	if d := len(a) - len(b); d > limit || -d > limit {
		return limit + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(b); j++ {
			c := prev[j-1]
			if a[i-1] != b[j-1] {
				c++
			}
			c = min(c, prev[j]+1, cur[j-1]+1)
			cur[j] = c
			best = min(best, c)
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
