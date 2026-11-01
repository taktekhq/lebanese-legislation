package amend

import (
	"regexp"
	"strconv"
	"strings"
)

// One spelling for article numbers: the Centre's. Western digits; "73";
// a sub-article "12-1" (dash, no spaces); "12 مكرر", "14 مكرر 2"; a text's
// sole article "وحيدة"; a promulgation article "1 اصدار". Citations,
// printed numbers and what people type come in many others ("12/1",
// "19.1", "٥٨٦ (1)", "12 bis", "الوحيدة", "3,8", "90 وما يليها", "103 جديدة");
// Canon and Resolve fold them to this one.

// Number is one article number read from a spelling.
type Number struct {
	Article string // canonical; "" when the spelling names no article
	Part    string // what the target does not number: the "2" of "19.2" when the target has article 19 but no 19-2, or the words that came with it
	Found   bool   // the target has Article (always false without a target)
}

var (
	canonDigits  = regexp.MustCompile(`^(\d+)$`)
	canonBis     = regexp.MustCompile(`^(\d+) مكرر(?: (\d+))?$`)
	canonSub     = regexp.MustCompile(`^\d+(?:-\d+)+$`)
	canonIssue   = regexp.MustCompile(`^(\d+) اصدار$`)
	canonNew     = regexp.MustCompile(`^(\d+) ?جديده$`)
	canonRange   = regexp.MustCompile(`^(\d+) (?:الي|حتي|لغايه) (\d+)$`)
	canonNoun    = regexp.MustCompile(`^(?:رقم )?(?:ال)?ماد[هة] ?`)
	canonRoman   = regexp.MustCompile(`^\((\d+)\)`)
	canonMore    = regexp.MustCompile(` ?و?ما (?:يليها|يليهما|بعدها|بعدهما)$`)
	canonBisWord = regexp.MustCompile(`(?i)\bbis\b|مكرر[هةا]`)
	// The word right after a number that makes it another article: bis we
	// read above; ter, quater and the rest ("267 رباعا", "304 ثلاثا", "6
	// مكرر ثان") we don't, so such a number is never taken for plain N.
	canonOtherArt = regexp.MustCompile(`^(?:مكرر|ثان|ثاني|ثالث|ثلاث|رابع|رباع|خامس|خماس|سادس|سداس|سابع|سباع|ثامن|ثمان|تاسع|تساع|عاشر|عشار)[اهة]?$`)
	canonAnd      = regexp.MustCompile(`(\d) ?و ?(\d)`)
	canonSlash    = regexp.MustCompile(`(\d) ?[./] ?(\d)`)
	canonDash     = regexp.MustCompile(`(\d) ?- ?(\d)`)
	canonParen    = regexp.MustCompile(`(\d) ?\( ?(\d+) ?\)`)
	canonGap      = regexp.MustCompile(`(\d) (\d)`)
	canonBisDash  = regexp.MustCompile(`(\d) ?- ?مكرر`)
	canonDashBis  = regexp.MustCompile(`مكرر ?- ?(\d)`)
	canonBisGap   = regexp.MustCompile(`مكرر ?(\d)`)
)

// candidate is a number to try against a target, and what to keep as the
// part when it is not the whole spelling.
type candidate struct{ number, part string }

// reading is one number as written: the candidates in the order to try them
// against the target (the last of them is where a lookup without a target
// stops), and the middle of a range (kept only where the target has it).
type reading struct {
	cands  []candidate
	pure   string // the folded spelling alone, "" if none could be read
	raw    string
	middle bool
}

func stripZeros(n string) string { return strings.TrimLeft(n, "0") }

// Canon reads a spelling of article numbers (one, a list, a range) as the
// Centre writes them, and where target (a text's article numbers) is given,
// as that text numbers them. Articles the spelling names that the text has
// come first, in order; with a target, an article that is not in it is
// still returned in its folded spelling, never another one. Spellings that
// name no article at all return nothing. See Resolve for the parts.
func Canon(raw string, target []string) []string {
	var has func(string) bool
	if target != nil {
		set := make(map[string]bool, len(target))
		for _, t := range target {
			set[t] = true
		}
		has = func(n string) bool { return set[n] }
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range Resolve(raw, has) {
		if n.Article != "" && !seen[n.Article] {
			seen[n.Article] = true
			out = append(out, n.Article)
		}
	}
	return out
}

// Resolve reads raw into the article numbers it names. has says whether the
// target text numbers an article that way; nil reads without a target (every
// folded number stands, a sub-article stays "N-M").
//
// With a target: the spelling is tried as the Centre writes it first ("103
// جديدة" if the text has that article, as some do, else "103"); a sub-article
// "N-M" the text does not have falls back to article N with M as the part
// ("19.2" is article 19, paragraph 2, when the text has no article 19-2);
// the same for words left after a number ("4 E"). Nothing is invented: a
// number the text lacks keeps its folded spelling with Found false, and one
// that names no article (Article "") keeps its words as the part.
func Resolve(raw string, has func(string) bool) []Number {
	if t := strings.Join(strings.Fields(raw), " "); has != nil && has(t) { // the Centre's own, as it prints it ("40 مكررة")
		return []Number{{Article: t, Found: true}}
	}
	var out []Number
	seen := map[string]bool{}
	for _, r := range readAll(raw) {
		n := r.resolve(has)
		if r.middle && has != nil && !n.Found {
			continue
		}
		key := n.Article + "|" + n.Part
		if n.Article == "" {
			key = "|" + r.raw
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, n)
	}
	return out
}

func (r reading) resolve(has func(string) bool) Number {
	if has != nil {
		if has(strings.TrimSpace(r.raw)) { // the Centre's own spelling, whatever it is ("1 *", "12 جديدة")
			return Number{Article: strings.TrimSpace(r.raw), Found: true}
		}
		for _, c := range r.cands {
			if has(c.number) {
				return Number{Article: c.number, Part: c.part, Found: true}
			}
		}
	}
	if r.pure == "" {
		return Number{Part: strings.TrimSpace(r.raw)}
	}
	return Number{Article: r.pure}
}

// readAll folds raw to its readings.
func readAll(raw string) []reading {
	s := Fold(raw).N
	s = strings.TrimSpace(s)
	s = canonNoun.ReplaceAllString(s, "")
	if m := canonRoman.FindStringSubmatch(s); m != nil { // "(5) E"
		s = m[1] + s[len(m[0]):]
	}
	if len(s) > 2 && s[0] == '(' && s[len(s)-1] == ')' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimPrefix(s, "من ")
	s = canonMore.ReplaceAllString(s, "")
	s = canonBisWord.ReplaceAllString(s, "مكرر")
	s = canonBisDash.ReplaceAllString(s, "$1 مكرر")
	s = canonDashBis.ReplaceAllString(s, "مكرر $1")
	s = canonBisGap.ReplaceAllString(s, "مكرر $1")
	s = strings.TrimSpace(s)
	if m := canonRange.FindStringSubmatch(s); m != nil {
		a, errA := strconv.Atoi(m[1])
		b, errB := strconv.Atoi(m[2])
		if errA == nil && errB == nil && b > a && b-a <= 300 {
			var out []reading
			for k := a; k <= b; k++ {
				r := readOne(strconv.Itoa(k))
				r.middle = k != a && k != b
				out = append(out, r)
			}
			return out
		}
	}
	s = repeat(canonAnd, s, "$1,$2")
	var out []reading
	for _, piece := range strings.Split(s, ",") {
		if p := strings.TrimSpace(piece); p != "" {
			out = append(out, readOne(p))
		}
	}
	return out
}

func repeat(re *regexp.Regexp, s, to string) string {
	for {
		t := re.ReplaceAllString(s, to)
		if t == s {
			return s
		}
		s = t
	}
}

// readOne reads one folded number.
func readOne(p string) reading {
	r := reading{raw: p}
	// "19.1", "12/1", "586 (1)", "6 - 2", "54 6" are all "N-M".
	q := repeat(canonSlash, p, "$1-$2")
	q = repeat(canonParen, q, "$1-$2")
	q = repeat(canonDash, q, "$1-$2")
	q = repeat(canonGap, q, "$1-$2")
	switch {
	case q == "وحيده" || q == "الوحيده" || q == "وحيد" || q == "الوحيد":
		r.pure = "وحيدة"
		r.cands = []candidate{{r.pure, ""}}
		return r
	}
	if m := canonNew.FindStringSubmatch(q); m != nil {
		// Some texts number "12 جديدة" as an article of their own.
		n := stripZeros(m[1])
		if n == "" {
			return r
		}
		r.pure = n
		r.cands = []candidate{{n + " جديدة", ""}, {n, ""}}
		return r
	}
	if m := canonDigits.FindStringSubmatch(q); m != nil {
		if n := stripZeros(m[1]); n != "" {
			r.pure = n
			r.cands = []candidate{{n, ""}}
		}
		return r
	}
	if m := canonBis.FindStringSubmatch(q); m != nil {
		if n := stripZeros(m[1]); n != "" {
			r.pure = n + " مكرر"
			if m[2] != "" {
				r.pure += " " + stripZeros(m[2])
			}
			r.cands = []candidate{{r.pure, ""}}
			if m[2] != "" {
				r.cands = append(r.cands, candidate{n + " مكرر", stripZeros(m[2])})
			}
		}
		return r
	}
	if m := canonIssue.FindStringSubmatch(q); m != nil {
		r.pure = stripZeros(m[1]) + " اصدار"
		r.cands = []candidate{{r.pure, ""}}
		return r
	}
	if canonSub.MatchString(q) {
		segs := strings.Split(q, "-")
		for i := range segs {
			// "42.02" is a tariff heading, not article 42-2: the Centre never
			// writes a sub-number with a leading zero.
			if i > 0 && len(segs[i]) > 1 && segs[i][0] == '0' {
				return reading{raw: p}
			}
			segs[i] = stripZeros(segs[i])
			if segs[i] == "" && i == 0 {
				return r
			}
			if segs[i] == "" {
				segs[i] = "0"
			}
		}
		if len(segs) == 2 { // deeper numbers are the Centre's to have; we never keep one it lacks
			r.pure = strings.Join(segs, "-")
		}
		// N-M-K, then N-M with K as the part, then N with M-K.
		for k := len(segs); k >= 1; k-- {
			r.cands = append(r.cands, candidate{strings.Join(segs[:k], "-"), strings.Join(segs[k:], "-")})
		}
		return r
	}
	// "N مكرر ثان": an ordinal after the bis.
	if i := strings.Index(q, " مكرر "); i > 0 && canonDigits.MatchString(q[:i]) {
		if n, used := ordinal(strings.Fields(q[i+len(" مكرر "):])); used > 0 && used == len(strings.Fields(q[i+len(" مكرر "):])) {
			r.pure = stripZeros(q[:i]) + " مكرر " + strconv.Itoa(n)
			r.cands = []candidate{{r.pure, ""}, {stripZeros(q[:i]) + " مكرر", strconv.Itoa(n)}}
			return r
		}
	}
	// A number then words we do not read ("4 E", "7 (فقرتها 2)", "2م"): the
	// article, if the target has it, and the words as the part.
	if lead := leadDigits(p); lead != "" {
		if w := strings.Fields(p[len(lead):]); len(w) > 0 && canonOtherArt.MatchString(w[0]) {
			return r
		}
		if n := stripZeros(lead); n != "" {
			r.cands = []candidate{{n, strings.Trim(p[len(lead):], " /:-.,")}}
		}
		return r
	}
	// Words: "الثالثة", "الاولى" (an ordinal).
	if n, ok := ArticleNumber(p); ok {
		r.pure = n
		r.cands = []candidate{{n, ""}}
	}
	return r
}

// IsCanonical reports whether s is written the Centre's way: "73", "12-1",
// "12 مكرر", "14 مكرر 2", "وحيدة", "1 اصدار". (A few texts of the Centre have
// numbers of their own beyond these, such as "12 جديدة" and "1 *"; a check
// against the data allows those where the Centre has them.)
func IsCanonical(s string) bool {
	if s == "وحيدة" {
		return true
	}
	if m := canonDigits.FindStringSubmatch(s); m != nil {
		return stripZeros(m[1]) == m[1]
	}
	if m := canonBis.FindStringSubmatch(s); m != nil {
		return stripZeros(m[1]) == m[1] && (m[2] == "" || stripZeros(m[2]) == m[2])
	}
	if canonIssue.MatchString(s) {
		return true
	}
	return canonSub.MatchString(s) && strings.Count(s, "-") == 1
}

func leadDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
