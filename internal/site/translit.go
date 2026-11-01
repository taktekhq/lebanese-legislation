package site

import (
	"strings"
	"unicode"
)

// Arabic written in Latin letters ("ta3adi", "Shehab", "Chehab", "kenaan"):
// its consonants are read letter by letter into an Arabic skeleton, which the
// vocabulary (vocab.go) turns into a real word. Vowels are dropped, as they
// are not in the skeleton either; the digits Arabizi uses for the letters
// with no Latin match (3 for ع, 7 for ح, 2 for the hamza…) are read as such.

var latinDigraphs = []struct{ in, out string }{
	{"sh", "ش"}, {"ch", "ش"}, {"kh", "خ"}, {"gh", "غ"}, {"th", "ث"}, {"dh", "ذ"}, {"ph", "ف"},
	{"3'", "غ"}, {"7'", "خ"}, {"9'", "ظ"}, {"'3", "ع"},
}

var latinLetters = map[rune]string{
	'b': "ب", 't': "ت", 'j': "ج", 'h': "ه", 'd': "د", 'r': "ر", 'z': "ز", 's': "س", 'f': "ف", 'q': "ق",
	'k': "ك", 'l': "ل", 'm': "م", 'n': "ن", 'p': "ب", 'v': "ف", 'g': "ج", 'c': "ك", 'x': "كس",
	'2': "ء", '3': "ع", '5': "خ", '6': "ط", '7': "ح", '8': "غ", '9': "ق",
}

// arabiziSkeleton reads one Latin word as Arabic consonants ("" if it has none).
func arabiziSkeleton(w string) string {
	w = strings.ToLower(latinFold(w))
	rs := []rune(w)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		if i+1 < len(rs) {
			pair := string(rs[i : i+2])
			hit := false
			for _, d := range latinDigraphs {
				if d.in == pair {
					b.WriteString(d.out)
					i++
					hit = true
					break
				}
			}
			if hit {
				continue
			}
		}
		if ar, ok := latinLetters[rs[i]]; ok {
			b.WriteString(ar)
		}
	}
	return b.String()
}

// arabiziStop are the little words Arabizi sentences carry: the article, "in",
// "of", "and". They are left out of the search.
var arabiziStop = map[string]bool{"al": true, "el": true, "l": true, "ll": true, "lel": true, "fi": true, "fe": true, "w": true,
	"wa": true, "men": true, "min": true, "3ala": true, "ala": true, "la": true, "bi": true, "b": true, "li": true, "ma": true,
	"d": true, "de": true, "du": true, "des": true, "the": true, "of": true, "and": true, "et": true}

// latinWords reads a Latin query into Arabic words: a glossed word is
// translated, any other is read as Arabic spelt in Latin letters and matched
// to a title word. It returns "" unless every content word was read, because a
// misread word would send the search to the wrong place.
func latinWords(q string, v *vocab) string {
	if hasArabic(q) {
		return ""
	}
	var out []string
	for _, w := range latinTokens(q) {
		w = strings.TrimPrefix(strings.TrimPrefix(w, "l'"), "d'") // "l'emir" is "the emir"
		w = strings.TrimSuffix(w, "'s")                           // "Lebanon's"
		w = strings.Trim(w, "'-")
		if w == "" || arabiziStop[w] || glossStop[w] {
			continue
		}
		if ar, ok := glossWordAr[w]; ok {
			out = append(out, ar)
			continue
		}
		if strings.IndexFunc(w, unicode.IsLetter) < 0 {
			continue // a bare number is the caller's business
		}
		ar, ok := readLatinWord(w, v)
		if !ok {
			return ""
		}
		out = append(out, ar)
	}
	return strings.Join(out, " ")
}

// readLatinWord matches a Latin word to a title word by its consonants. "aa"
// is tried as the letter ع first ("kenaan" is «كنعان»), then as a long vowel;
// among words with the same consonants, one that ends the way the Latin does
// (-i is ي, -ou is و, -a is ا or ة) wins over a more frequent one that does not.
func readLatinWord(w string, v *vocab) (string, bool) {
	forms := []string{w}
	if strings.Contains(w, "aa") {
		forms = []string{strings.Replace(w, "aa", "3", 1), w}
	}
	for _, f := range forms {
		if ar, ok := v.bySkeletonExact(arabiziSkeleton(f), arabicEnding(f)); ok {
			return ar, true
		}
	}
	return "", false
}

// arabicEnding is the letters an Arabic word ends in when the Latin ends in w's last letters.
func arabicEnding(w string) string {
	switch {
	case strings.HasSuffix(w, "i"), strings.HasSuffix(w, "y"), strings.HasSuffix(w, "ee"):
		return "ي"
	case strings.HasSuffix(w, "ou"), strings.HasSuffix(w, "oo"), strings.HasSuffix(w, "u"):
		return "و"
	case strings.HasSuffix(w, "a"), strings.HasSuffix(w, "ah"), strings.HasSuffix(w, "eh"):
		return "اه"
	}
	return ""
}
