package amend

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Text struct {
	Orig string
	N    string
	off  []int
}

func Fold(s string) Text {
	t := Text{Orig: s}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for i, r := range s {
		switch {
		case r >= 0x064B && r <= 0x065F, r == 0x0670, r == 0x0640,
			r == 0x00AD, r >= 0x200B && r <= 0x200F, r == 0x2066, r == 0x2067, r == 0x2068, r == 0x2069, r == 0xFEFF:
			continue
		case unicode.IsSpace(r):
			if !space && b.Len() > 0 {
				t.add(&b, ' ', i)
			}
			space = true
			continue
		}
		space = false
		switch r {
		case 'أ', 'إ', 'آ', 'ٱ':
			r = 'ا'
		case 'ى':
			r = 'ي'
		case 'ة':
			r = 'ه'
		case '–', ',', '−', '‐', '‑':
			r = '-'
		case '“', '”', '„', '″', '‟':
			r = '"'
		case '،':
			r = ','
		}
		if r >= '٠' && r <= '٩' {
			r = '0' + (r - '٠')
		} else if r >= '۰' && r <= '۹' {
			r = '0' + (r - '۰')
		}
		t.add(&b, r, i)
	}
	t.N = strings.TrimRight(b.String(), " ")
	t.off = t.off[:len(t.N)]
	return t
}

func (t *Text) add(b *strings.Builder, r rune, at int) {
	n := utf8.RuneLen(r)
	b.WriteRune(r)
	for k := 0; k < n; k++ {
		t.off = append(t.off, at)
	}
}

func (t Text) Span(i, j int) string {
	if i >= len(t.N) || i >= j {
		return ""
	}
	from := t.off[i]
	to := len(t.Orig)
	if j < len(t.N) {
		to = t.off[j]
	}
	return strings.TrimSpace(t.Orig[from:to])
}

func FoldString(s string) string { return Fold(s).N }
