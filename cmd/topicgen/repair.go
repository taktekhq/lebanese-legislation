package main

import (
	"strings"
	"unicode"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// foldRune reduces a character to the one a reader treats as the same
// letter: no diacritics or tatweel, one alef, one yeh, one heh. Spaces and
// punctuation are dropped. Used only to find where in the article a
// near-copy was taken from.
func foldRune(r rune) (rune, bool) {
	switch {
	case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
		return 'ا', true
	case r == 'ى':
		return 'ي', true
	case r == 'ة':
		return 'ه', true
	case r == 'ـ' || (r >= 0x064B && r <= 0x065F) || r == 0x0670:
		return 0, false
	case unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) || unicode.IsPunct(r) || r == '‏' || r == '‎':
		return 0, false
	}
	return unicode.ToLower(r), true
}

// repairQuote finds the passage of text that the model's quote was copied
// from, when it differs only in spelling marks, spacing or punctuation, and
// returns the article's own words for it. It never changes the article: the
// result is always an exact piece of text.
func repairQuote(text, quote string) (string, bool) {
	if strings.Contains(text, quote) {
		return quote, true
	}
	var folded []rune
	var at []int // folded index -> byte offset in text
	for i, r := range text {
		if f, ok := foldRune(r); ok {
			folded = append(folded, f)
			at = append(at, i)
		}
	}
	var q []rune
	for _, r := range quote {
		if f, ok := foldRune(r); ok {
			q = append(q, f)
		}
	}
	if len(q) < 8 {
		return "", false
	}
	idx := indexRunes(folded, q)
	if idx < 0 {
		return "", false
	}
	start := at[idx]
	end := len(text)
	if last := idx + len(q); last < len(at) {
		end = at[last]
	}
	// drop trailing spaces the fold skipped over
	return strings.TrimRight(text[start:end], " \t\r\n"), true
}

func indexRunes(h, n []rune) int {
outer:
	for i := 0; i+len(n) <= len(h); i++ {
		for j := range n {
			if h[i+j] != n[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

// stripDashes removes em dashes from every text of a topic: a comma where
// the dash stood between words, a hyphen between digits.
func stripDashes(t *topics.Topic) {
	fix := func(s *string) {
		*s = strings.ReplaceAll(*s, " \u2014 ", ", ")
		*s = strings.ReplaceAll(*s, "\u2014", "-")
	}
	for _, c := range []*topics.Content{&t.EN, &t.AR} {
		fix(&c.Title)
		fix(&c.Question)
		fix(&c.Description)
		fix(&c.Answer.Text)
		for i := range c.Sections {
			s := &c.Sections[i]
			fix(&s.Heading)
			for j := range s.Paras {
				fix(&s.Paras[j].Text)
			}
			if s.Table != nil {
				fix(&s.Table.Caption)
				for j := range s.Table.Head {
					fix(&s.Table.Head[j])
				}
				for _, r := range s.Table.Rows {
					for j := range r.Cells {
						fix(&r.Cells[j])
					}
				}
			}
		}
	}
}
