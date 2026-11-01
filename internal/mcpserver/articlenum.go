package mcpserver

import (
	"regexp"
	"strconv"
	"strings"
)

// Article numbers have one spelling, the Centre's: "73", "12-1", "12 مكرر",
// "14 مكرر 2", "وحيدة", "1 اصدار". Callers (and citations) write "12/1",
// "19.1", "٥٨٦ (1)", "12 bis", "الوحيدة", "3,8", "90 وما يليها", "103 جديدة".
// numberForms folds them, the same way amend.Canon and amend.Resolve do;
// the cases in testdata/article_numbers.json hold both to one table.

var (
	anDrop    = regexp.MustCompile(`[\x{064B}-\x{065F}\x{0670}\x{0640}\x{00AD}\x{200B}-\x{200F}\x{2066}-\x{2069}\x{FEFF}]`)
	anAlef    = regexp.MustCompile(`[أإآٱ]`)
	anDashes  = regexp.MustCompile(`[\x{2013}\x{2014}\x{2212}\x{2010}\x{2011}]`)
	anSpaces  = regexp.MustCompile(`[` + jsSpace + `]+`)
	anDotGap  = regexp.MustCompile(`(\d) ?[./] ?(\d)`)
	anParGap  = regexp.MustCompile(`(\d) ?\( ?(\d+) ?\)`)
	anDashGap = regexp.MustCompile(`(\d) ?- ?(\d)`)
	anSpGap   = regexp.MustCompile(`(\d) (\d)`)
	anSole    = regexp.MustCompile(`^(?:ال)?وحيده?$`)
	anNew     = regexp.MustCompile(`^(\d+) ?جديده$`)
	anDigits  = regexp.MustCompile(`^(\d+)$`)
	anBis     = regexp.MustCompile(`^(\d+) مكرر(?: (\d+))?$`)
	anIssue   = regexp.MustCompile(`^(\d+) اصدار$`)
	anSub     = regexp.MustCompile(`^\d+(?:-\d+)+$`)
	anLeadNum = regexp.MustCompile(`^\d+`)
	anOther   = regexp.MustCompile(`^(?:مكرر|ثان|ثاني|ثالث|ثلاث|رابع|رباع|خامس|خماس|سادس|سداس|سابع|سباع|ثامن|ثمان|تاسع|تساع|عاشر|عشار)[اهة]?$`)
	anArticle = regexp.MustCompile(`^(?:رقم )?(?:ال)?ماد[هة] ?`)
	anLeadPar = regexp.MustCompile(`^\((\d+)\)`)
	anFrom    = regexp.MustCompile(`^من `)
	anFollow  = regexp.MustCompile(` ?و?ما (?:يليها|يليهما|بعدها|بعدهما)$`)
	anBisWord = regexp.MustCompile(`(?i)\bbis\b|مكرر[هةا]`)
	anDashBis = regexp.MustCompile(`(\d) ?- ?مكرر`)
	anBisDash = regexp.MustCompile(`مكرر ?- ?(\d)`)
	anBisNum  = regexp.MustCompile(`مكرر ?(\d)`)
	anRange   = regexp.MustCompile(`^(\d+) (?:الي|حتي|لغايه) (\d+)$`)
	anAnd     = regexp.MustCompile(`(\d) ?و ?(\d)`)
)

// arabicToASCII turns Arabic-Indic and Persian digits into 0-9.
func arabicToASCII(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x0660 && r <= 0x0669:
			return '0' + (r - 0x0660)
		case r >= 0x06F0 && r <= 0x06F9:
			return '0' + (r - 0x06F0)
		}
		return r
	}, s)
}

// foldAr folds Arabic the way amend.Fold does: no tashkeel or tatweel,
// أ إ آ ٱ to ا, ى to ي, ة to ه, digits to ASCII, dashes to "-", "،" to ",".
func foldAr(s string) string {
	s = arabicToASCII(s)
	s = anDrop.ReplaceAllString(s, "")
	s = anAlef.ReplaceAllString(s, "ا")
	s = strings.ReplaceAll(s, "ى", "ي")
	s = strings.ReplaceAll(s, "ة", "ه")
	s = anDashes.ReplaceAllString(s, "-")
	s = strings.ReplaceAll(s, "،", ",")
	s = anSpaces.ReplaceAllString(s, " ")
	return jsTrim(s)
}

// repeatReplace applies re until nothing changes.
func repeatReplace(re *regexp.Regexp, s, to string) string {
	for {
		t := re.ReplaceAllString(s, to)
		if t == s {
			return s
		}
		s = t
	}
}

func stripZeros(n string) string { return strings.TrimLeft(n, "0") }

// readOne is the numbers one folded number may be, best first: the
// Centre's own spelling, then the others it falls back to (a sub-article
// the text lacks is its article).
func readOne(p string) []string {
	out := []string{p}
	// "19.1", "12/1", "586 (1)", "6 - 2", "54 6" are all "N-M".
	gap := repeatReplace(anDotGap, p, "${1}-${2}")
	gap = repeatReplace(anParGap, gap, "${1}-${2}")
	gap = repeatReplace(anDashGap, gap, "${1}-${2}")
	gap = repeatReplace(anSpGap, gap, "${1}-${2}")
	if anSole.MatchString(gap) || gap == "الوحيد" {
		return append(out, "وحيدة")
	}
	if m := anNew.FindStringSubmatch(gap); m != nil {
		if n := stripZeros(m[1]); n != "" {
			out = append(out, n+" جديدة", n)
		}
		return out
	}
	if m := anDigits.FindStringSubmatch(gap); m != nil {
		if n := stripZeros(m[1]); n != "" {
			out = append(out, n)
		}
		return out
	}
	if m := anBis.FindStringSubmatch(gap); m != nil {
		if n := stripZeros(m[1]); n != "" {
			if m[2] != "" {
				out = append(out, n+" مكرر "+stripZeros(m[2]), n+" مكرر")
			} else {
				out = append(out, n+" مكرر")
			}
		}
		return out
	}
	if m := anIssue.FindStringSubmatch(gap); m != nil {
		return append(out, stripZeros(m[1])+" اصدار")
	}
	if anSub.MatchString(gap) {
		// "42.02" is a tariff heading, not article 42-2: the Centre never
		// writes a sub-number with a leading zero.
		segs := strings.Split(gap, "-")
		for _, s := range segs[1:] {
			if len(s) > 1 && s[0] == '0' {
				return out
			}
		}
		for i := range segs {
			if segs[i] = stripZeros(segs[i]); segs[i] == "" {
				segs[i] = "0"
			}
		}
		for k := len(segs); k >= 1; k-- {
			out = append(out, strings.Join(segs[:k], "-"))
		}
		return out
	}
	if m := anLeadNum.FindString(p); m != "" {
		// a number then words we do not read ("4 E", "2م"): the article, unless
		// the first word makes it another article ("267 رباعا", "6 مكرر ثان")
		rest := jsTrim(p[len(m):])
		if anOther.MatchString(strings.Split(rest, " ")[0]) {
			return out
		}
		if n := stripZeros(m); n != "" {
			out = append(out, n)
		}
		return out
	}
	if n := ordinal(p); n != 0 {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

var ordUnits = map[string]int{
	"اولي": 1, "اول": 1, "حاديه": 1, "حادي": 1, "واحده": 1, "واحد": 1, "ثانيه": 2, "ثاني": 2, "ثالثه": 3, "ثالث": 3, "رابعه": 4, "رابع": 4,
	"خامسه": 5, "خامس": 5, "سادسه": 6, "سادس": 6, "سابعه": 7, "سابع": 7, "ثامنه": 8, "ثامن": 8, "تاسعه": 9, "تاسع": 9, "عاشره": 10, "عاشر": 10,
}

var ordTens = map[string]int{
	"عشرون": 20, "عشرين": 20, "ثلاثون": 30, "ثلاثين": 30, "اربعون": 40, "اربعين": 40, "خمسون": 50, "خمسين": 50,
	"ستون": 60, "ستين": 60, "سبعون": 70, "سبعين": 70, "ثمانون": 80, "ثمانين": 80, "تسعون": 90, "تسعين": 90,
}

// ordinal reads the words of an ordinal article number ("الثالثه", "الحاديه
// عشره", "الثالثه والسبعون") as amend.ArticleNumber does, up to 99; 0 when
// the words are not one from start to end.
func ordinal(s string) int {
	type tok struct {
		and  bool
		word string
	}
	w := func(t string) tok {
		and := strings.HasPrefix(t, "و") && !strings.HasPrefix(t, "واحد")
		word := t
		if and {
			word = strings.TrimPrefix(t, "و")
		}
		return tok{and, strings.TrimPrefix(word, "ال")}
	}
	toks := strings.Split(s, " ")
	first := w(toks[0])
	if first.and {
		return 0
	}
	if len(toks) == 1 && (first.word == "وحيده" || first.word == "وحيد") {
		return 0 // the sole article is "وحيدة", not 1
	}
	if ordTens[first.word] != 0 && len(toks) == 1 {
		return ordTens[first.word]
	}
	u := ordUnits[first.word]
	if u == 0 {
		return 0
	}
	if len(toks) == 1 {
		return u
	}
	next := w(toks[1])
	if len(toks) == 2 && !next.and && (next.word == "عشره" || next.word == "عشر") && u < 10 {
		return u + 10
	}
	if len(toks) == 2 && next.and && ordTens[next.word] != 0 && u < 10 {
		return u + ordTens[next.word]
	}
	return 0
}

func readAll(raw string) [][]string {
	s := foldAr(raw)
	s = anArticle.ReplaceAllString(s, "")
	if m := anLeadPar.FindStringSubmatch(s); m != nil {
		s = m[1] + s[len(m[0]):]
	}
	if jsLen(s) > 2 && strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = s[1 : len(s)-1]
	}
	s = anFrom.ReplaceAllString(s, "")
	s = anFollow.ReplaceAllString(s, "")
	s = anBisWord.ReplaceAllString(s, "مكرر")
	s = anDashBis.ReplaceAllString(s, "${1} مكرر")
	s = anBisDash.ReplaceAllString(s, "مكرر ${1}")
	s = anBisNum.ReplaceAllString(s, "مكرر ${1}")
	s = jsTrim(s)
	if m := anRange.FindStringSubmatch(s); m != nil {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		if b > a && b-a <= 300 {
			return [][]string{readOne(m[1]), readOne(m[2])} // the ends; a lookup takes the first that exists
		}
	}
	s = repeatReplace(anAnd, s, "${1},${2}")
	var out [][]string
	for _, x := range strings.Split(s, ",") {
		if x = jsTrim(x); x != "" {
			out = append(out, readOne(x))
		}
	}
	return out
}

// numberForms is what a caller may type for an article number, in the
// order to try: as given, then each number it names (a list names several)
// as the database writes it, with the fall-backs of amend.Resolve: "19.2"
// is "19-2", then "19"; "103 جديدة" is "103 جديدة" if the text has it, else
// "103"; "586 (1)", "586/1", "586 - 1", "٥٨٦-١" and "586 1" are "586-1";
// "12 bis" is "12 مكرر".
func numberForms(input string) []string {
	given := jsTrim(input)
	all := []string{given}
	for _, forms := range readAll(given) {
		all = append(all, forms...)
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range all {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}
