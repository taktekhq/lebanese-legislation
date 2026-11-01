package mcpserver

import (
	"regexp"
	"strings"
)

// arNorm is Postgres's ar_norm (db/schema.sql): it drops tashkeel and
// tatweel and folds أ إ آ ٱ to ا, ى to ي, ة to ه, so one spelling of a word
// finds every other. Queries are normalized with it before they are matched
// against ar_norm'd text.
func arNorm(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case (r >= 0x064B && r <= 0x065F) || r == 0x0670 || r == 0x0640:
			continue
		case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
			b.WriteRune('ا')
		case r == 'ى':
			b.WriteRune('ي')
		case r == 'ة':
			b.WriteRune('ه')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fts5Quote wraps a search term the way an FTS5 MATCH expression quotes a
// phrase, doubling embedded double quotes. The search code builds FTS5
// expressions exactly as the Worker did and then translates them to
// Postgres full-text queries (see fts.go), so both sides read a question
// the same way.
func fts5Quote(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}

// fts5MatchAll requires every word of q (each normalized and quoted).
func fts5MatchAll(q string) string {
	words := jsFields(jsTrim(arNorm(q)))
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = fts5Quote(w)
	}
	return strings.Join(out, " AND ")
}

// arStop holds the little words that no search should be held up by, after arNorm.
var arStop = setOf("في", "من", "على", "عن", "الى", "او", "ان", "التي", "الذي", "هل", "ما", "لدى", "رقم", "تاريخ", "بتاريخ", "و")

var (
	stemPrefixes = []string{"وال", "بال", "كال", "فال", "لل", "ال"}
	stemSuffixes = []string{"ات", "ين", "ون", "ه"}
)

// lightStem is a word (after arNorm) less a leading article ("ال", "وال",
// "بال", "لل"...) and a feminine or plural ending, when three letters are
// left: "الصوريه" is "صوري", "المحاكمات" is "محاكم", "والعقود" is "عقود".
func lightStem(w string) string {
	s := w
	for _, p := range stemPrefixes {
		if strings.HasPrefix(s, p) && runeLen(s)-runeLen(p) >= 3 {
			s = s[len(p):]
			break
		}
	}
	for _, x := range stemSuffixes {
		if strings.HasSuffix(s, x) && runeLen(s)-runeLen(x) >= 3 {
			s = s[:len(s)-len(x)]
			break
		}
	}
	return s
}

var formPrefixes = []string{"", "ال", "وال", "بال", "لل", "و", "ب", "ل", "ف"}

var (
	edgePunct  = regexp.MustCompile(`^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$`)
	arabicChar = regexp.MustCompile(`[\x{0600}-\x{06FF}]`)
)

// trimEdges drops anything but letters and digits from both ends of a word.
func trimEdges(w string) string { return edgePunct.ReplaceAllString(w, "") }

// fts5Forms requires every word in one of its usual forms: the stem as a
// prefix, bare or after an article or a conjunction. The texts write
// "صورية", "والصورية", "الصوري"; a search for "الصورية سند الضد" found
// nothing while every word had to be that exact token. Little words are
// left out; numbers stay exact.
func fts5Forms(q string) string {
	var groups []string
	for _, raw := range jsFields(jsTrim(arNorm(q))) {
		w := trimEdges(raw)
		if w == "" || arStop[w] {
			continue
		}
		stem := lightStem(w)
		if !arabicChar.MatchString(w) || runeLen(stem) < 3 {
			groups = append(groups, fts5Quote(w))
			continue
		}
		// the word as typed stays a form: "وكاله" is not "و" + "كاله"
		var forms []string
		seen := map[string]bool{}
		add := func(f string) {
			if !seen[f] {
				seen[f] = true
				forms = append(forms, f)
			}
		}
		for _, p := range formPrefixes {
			add(fts5Quote(p+stem) + "*")
		}
		add(fts5Quote(w))
		groups = append(groups, "("+strings.Join(forms, " OR ")+")")
	}
	return strings.Join(groups, " AND ")
}

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
