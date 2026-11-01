package provenance

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Tags and comments only: a "<" in running text ("x < y") is not a tag.
var htmlTag = regexp.MustCompile(`(?s)<!--.*?-->|</?[A-Za-z][^<>]*>`)

// Normalize folds a text to the form the guard compares, on both sides: the
// stored article and its archived source. NFC; HTML tags removed and
// entities decoded; tatweel, Arabic diacritics and invisible format marks
// (ZWNJ, direction marks, BOM) removed; quotation marks (« » " “ ” „ ‹ ›)
// removed; Arabic-Indic digits as ASCII; whitespace collapsed to single
// spaces. It never folds letters (ة stays ة): a changed letter is a changed
// word.
//
// Quotation marks go because the build trims an amendment's wording out of
// its quotes and drops an unpaired « inside it (internal/amend's unquote):
// on the full corpus, 24 Oct 2026, every amendment that failed the span
// check failed at a quote mark, and none at a word.
func Normalize(s string) string { return normalize(s, nil) }

// normalize is Normalize. When breaks is not nil, it gets the offset in the
// result of each quotation mark dropped: a word ends there even though no
// space is left (a source's «النص» glued to the word before it).
func normalize(s string, breaks *[]int) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = norm.NFC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case quoteMark(r):
			if breaks != nil {
				*breaks = append(*breaks, b.Len())
			}
			continue
		case r == 'ـ' || arabicMark(r) || unicode.Is(unicode.Cf, r):
			continue
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// attrValue is a tag's attribute value: quoted, or bare.
var attrValue = regexp.MustCompile("=\\s*(\"[^\"]*\"|'[^']*'|[^\\s\"'=<>`]+)")

// NormalizeStored is Normalize for a stored text, the side a writer chose.
// Markup there is not dropped with what it holds: a comment's body and a
// tag's attribute values stay as words (a tag inside a comment too), and
// only the tag's name and its syntax go. Words a writer hid in markup must
// then be in the source like any others. The source side keeps Normalize:
// a page's markup is the fetcher's, not a writer's.
func NormalizeStored(s string) string {
	for htmlTag.MatchString(s) {
		s = htmlTag.ReplaceAllStringFunc(s, markupWords)
	}
	return Normalize(s)
}

// markupWords is what a comment or a tag holds in words.
func markupWords(m string) string {
	if body, ok := strings.CutPrefix(m, "<!--"); ok {
		return " " + strings.TrimSuffix(body, "-->") + " "
	}
	var b strings.Builder
	b.WriteByte(' ')
	for _, v := range attrValue.FindAllStringSubmatch(m, -1) {
		b.WriteString(strings.Trim(v[1], `"'`))
		b.WriteByte(' ')
	}
	return b.String()
}

// arabicMark: harakat, tanwin, shadda, sukun, dagger alef and the Quranic
// annotation signs. The same class as the schema's ar_norm, plus the
// annotation ranges.
func arabicMark(r rune) bool {
	switch {
	case r >= 0x0610 && r <= 0x061A,
		r >= 0x064B && r <= 0x065F,
		r == 0x0670,
		r >= 0x06D6 && r <= 0x06DC,
		r >= 0x06DF && r <= 0x06E4,
		r == 0x06E7 || r == 0x06E8,
		r >= 0x06EA && r <= 0x06ED:
		return true
	}
	return false
}

func quoteMark(r rune) bool {
	switch r {
	case '«', '»', '"', '“', '”', '„', '‹', '›':
		return true
	}
	return false
}

// normSource is a source as the guard compares it: Normalize's text, and
// where in it a quotation mark was dropped.
type normSource struct {
	text   string
	breaks map[int]bool
}

func normalizeSource(s string) normSource {
	var br []int
	t := normalize(s, &br)
	m := make(map[int]bool, len(br))
	for _, i := range br {
		m[i] = true
	}
	return normSource{t, m}
}

// spans says whether text, normalized, is a contiguous span of the source
// that starts and ends on a word boundary: never inside one of the
// source's words ("جوز للمحك" is not a span of "يجوز للمحكمة"). An empty
// text passes: the Centre's pages hold empty articles (1,639 on 24 Oct
// 2026), and which article of the source a text is, the span check does
// not know.
func (s normSource) spans(text string) bool {
	if text == "" {
		return true
	}
	for from := 0; from < len(s.text); {
		i := strings.Index(s.text[from:], text)
		if i < 0 {
			return false
		}
		i += from
		first, _ := utf8.DecodeRuneInString(text)
		last, _ := utf8.DecodeLastRuneInString(text)
		if s.boundary(i, first, true) && s.boundary(i+len(text), last, false) {
			return true
		}
		_, n := utf8.DecodeRuneInString(s.text[i:])
		from = i + n
	}
	return false
}

// boundary says whether the source has a word boundary at offset at, next
// to inner, the text's first rune (before is true) or its last.
func (s normSource) boundary(at int, inner rune, before bool) bool {
	if s.breaks[at] {
		return true
	}
	var outer rune
	if before {
		if at == 0 {
			return true
		}
		outer, _ = utf8.DecodeLastRuneInString(s.text[:at])
	} else {
		if at == len(s.text) {
			return true
		}
		outer, _ = utf8.DecodeRuneInString(s.text[at:])
	}
	c := wordClass(inner)
	return c == 0 || c != wordClass(outer)
}

// wordClass is what a rune can be part of a word with: digits with digits,
// letters with letters of their script. 0: not a word's rune. A number
// glued to a word ("12يعاقب") is two words.
func wordClass(r rune) int {
	switch {
	case unicode.IsDigit(r):
		return 1
	case !unicode.IsLetter(r) && !unicode.IsMark(r):
		return 0
	case unicode.Is(unicode.Arabic, r):
		return 2
	case unicode.Is(unicode.Latin, r):
		return 3
	}
	return 4
}
