package mcpserver

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// The cheap parts of the website's zero-result rescue: the glossary of
// English and French legal terms, Arabic-Indic digit folding, a number and a
// year pulled out of the question, and spelling variants of Latin names. It
// reads the question into other words to search for; it never writes
// anything shown as law.

//go:embed gloss.json
var glossJSON []byte

var gloss = func() (g struct {
	PhraseKeys []string          `json:"phrase_keys"` // longest first
	Phrases    map[string]string `json:"phrases"`
	Words      map[string]string `json:"words"`
	Stop       []string          `json:"stop"`
	PinStop    []string          `json:"pin_stop"`
	Pins       map[string]int    `json:"pins"`
	stop       map[string]bool
	pinStop    map[string]bool
}) {
	if err := json.Unmarshal(glossJSON, &g); err != nil {
		panic("mcpserver: gloss.json: " + err.Error())
	}
	g.stop = setOf(g.Stop...)
	g.pinStop = setOf(g.PinStop...)
	return g
}()

// foldDigits turns Arabic-Indic and Persian digits into 0-9: the indexed texts use 0-9.
func foldDigits(s string) string { return arabicToASCII(s) }

func hasArabic(s string) bool { return arabicChar.MatchString(s) }

// stripMarks drops the combining marks U+0300-U+036F after NFD, as
// s.normalize("NFD").replace(/[̀-ͯ]/g, "") does.
func stripMarks(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x0300 && r <= 0x036f {
			return -1
		}
		return r
	}, norm.NFD.String(s))
}

// latinFold lower-cases a Latin query, straightens curly apostrophes and
// strips accents ("nationalité" is "nationalite").
func latinFold(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'").Replace(s)
	return strings.ToLower(stripMarks(s))
}

var latinSplit = regexp.MustCompile(`[^\p{L}\p{N}'-]+`)

func latinTokens(q string) []string {
	var out []string
	for _, t := range latinSplit.Split(latinFold(q), -1) {
		if t != "" {
			out = append(out, strings.TrimSuffix(t, "'s"))
		}
	}
	return out
}

// glossQueryWhole returns the Arabic search for a Latin query ("" when it is
// Arabic or has no word the glossary knows) and whether every content word was
// translated. Unknown words are left out when others are known.
func glossQueryWhole(q string) (ar string, whole bool) {
	if jsTrim(q) == "" || hasArabic(q) {
		return "", false
	}
	padded := " " + strings.Join(latinTokens(q), " ") + " "
	var out []string
	for _, p := range gloss.PhraseKeys {
		if strings.Contains(padded, " "+p+" ") {
			out = append(out, gloss.Phrases[p])
			padded = strings.Join(strings.Split(padded, " "+p+" "), " ")
		}
	}
	whole = true
	for _, w := range strings.Split(padded, " ") {
		if w == "" || gloss.stop[w] {
			continue
		}
		if a, ok := gloss.Words[w]; ok {
			out = append(out, a)
		} else {
			whole = false
		}
	}
	return strings.Join(out, " "), whole && len(out) > 0
}

func glossQuery(q string) string {
	ar, _ := glossQueryWhole(q)
	return ar
}

var (
	searchYear = regexp.MustCompile(`^(?:19|20)\d\d$`)
	searchNum  = regexp.MustCompile(`^\d{1,5}$`)
	// little Arabic words that no title search should be held up by
	splitStop = setOf("في", "من", "على", "عن", "الى", "إلى", "او", "أو", "و", "ان", "أن", "هل", "ما", "رقم", "تاريخ")
)

// splitNumbers pulls a text number and a year out of a query, leaving the words.
func splitNumbers(q string) (num string, year int, words []string) {
	for _, w := range jsFields(foldDigits(q)) {
		switch {
		case searchYear.MatchString(w) && year == 0:
			year, _ = strconv.Atoi(w)
		case searchNum.MatchString(w) && num == "":
			num = w
		case splitStop[w]:
		default:
			words = append(words, w)
		}
	}
	return num, year, words
}

type nameSwap struct {
	re   *regexp.Regexp
	subs []string
}

var nameSwaps = []nameSwap{
	{regexp.MustCompile(`^ch`), []string{"sh"}}, {regexp.MustCompile(`^sh`), []string{"ch"}},
	{regexp.MustCompile(`kh`), []string{"h", "ch"}}, {regexp.MustCompile(`ou`), []string{"u", "o"}},
	{regexp.MustCompile(`aa`), []string{"a"}}, {regexp.MustCompile(`ee`), []string{"i"}},
	{regexp.MustCompile(`ie`), []string{"i", "e"}}, {regexp.MustCompile(`ei`), []string{"i", "e"}},
	{regexp.MustCompile(`e`), []string{"i", "a"}}, {regexp.MustCompile(`dj|j`), []string{"g"}},
	{regexp.MustCompile(`^k`), []string{"c", "q"}}, {regexp.MustCompile(`ae`), []string{"a", "e"}},
}

var notLatinLower = regexp.MustCompile(`[^a-z]`)

// replaceFirst is String.prototype.replace with a non-global regexp.
func replaceFirst(re *regexp.Regexp, s, to string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + to + s[loc[1]:]
}

// nameVariants gives the other ways a Latin name is spelled in English titles
// ("Chehab", "Shehab", "Shihab"; "Khalil", "Halil"), the original first. Each
// variant is tried against the English titles and kept only if it finds
// something.
func nameVariants(word string) []string {
	w := notLatinLower.ReplaceAllString(latinFold(word), "")
	if len(w) < 3 {
		return nil
	}
	out := []string{w}
	seen := map[string]bool{w: true}
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, s := range nameSwaps {
		if !s.re.MatchString(w) {
			continue
		}
		for _, sub := range s.subs {
			add(replaceFirst(s.re, w, sub))
		}
	}
	// one spelling change on top of a digraph change: Chehab to Shihab
	for _, v := range append([]string(nil), out...) {
		if v != w && strings.Contains(v, "e") {
			add(strings.Replace(v, "e", "i", 1))
		}
	}
	return out
}

var pinArStop = setOf("قانون", "القانون", "لبنان", "في")

// pinKey folds a question to the form the pinned terms are kept in: Latin
// lower case, accents and hyphens dropped, the little words left out;
// Arabic with spelling folded, the article ال of each word and the words
// قانون and لبنان left out.
func pinKey(q string) string {
	if hasArabic(q) {
		var out []string
		for _, w := range jsFields(arNorm(q)) {
			if pinArStop[w] {
				continue
			}
			for runeLen(w) > 4 && strings.HasPrefix(w, "ال") {
				w = strings.TrimPrefix(w, "ال") // "الالكترونية" and "الكترونية" are one word
			}
			out = append(out, w)
		}
		return strings.Join(out, " ")
	}
	var out []string
	for _, t := range latinTokens(strings.ReplaceAll(q, "-", " ")) {
		if !gloss.pinStop[t] {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// pinnedLaw returns the id of the text a question names, or 0 (pins in gloss.json).
func pinnedLaw(q string) int {
	k := pinKey(q)
	if k == "" {
		return 0
	}
	return gloss.Pins[k]
}

// hasLatin is /[A-Za-zÀ-ÿ]/.
func hasLatin(s string) bool {
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 0xC0 && r <= 0xFF) {
			return true
		}
	}
	return false
}
