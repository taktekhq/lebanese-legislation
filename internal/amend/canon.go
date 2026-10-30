package amend

import (
	"regexp"
	"strconv"
	"strings"
)

type Number struct {
	Article string
	Part    string
	Found   bool
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

type candidate struct{ number, part string }

type reading struct {
	cands  []candidate
	pure   string
	raw    string
	middle bool
}

func stripZeros(n string) string { return strings.TrimLeft(n, "0") }

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

func Resolve(raw string, has func(string) bool) []Number {
	if t := strings.Join(strings.Fields(raw), " "); has != nil && has(t) {
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
		if has(strings.TrimSpace(r.raw)) {
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

func readAll(raw string) []reading {
	s := Fold(raw).N
	s = strings.TrimSpace(s)
	s = canonNoun.ReplaceAllString(s, "")
	if m := canonRoman.FindStringSubmatch(s); m != nil {
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

func readOne(p string) reading {
	r := reading{raw: p}

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
		if len(segs) == 2 {
			r.pure = strings.Join(segs, "-")
		}

		for k := len(segs); k >= 1; k-- {
			r.cands = append(r.cands, candidate{strings.Join(segs[:k], "-"), strings.Join(segs[k:], "-")})
		}
		return r
	}

	if i := strings.Index(q, " مكرر "); i > 0 && canonDigits.MatchString(q[:i]) {
		if n, used := ordinal(strings.Fields(q[i+len(" مكرر "):])); used > 0 && used == len(strings.Fields(q[i+len(" مكرر "):])) {
			r.pure = stripZeros(q[:i]) + " مكرر " + strconv.Itoa(n)
			r.cands = []candidate{{r.pure, ""}, {stripZeros(q[:i]) + " مكرر", strconv.Itoa(n)}}
			return r
		}
	}

	if lead := leadDigits(p); lead != "" {
		if w := strings.Fields(p[len(lead):]); len(w) > 0 && canonOtherArt.MatchString(w[0]) {
			return r
		}
		if n := stripZeros(lead); n != "" {
			r.cands = []candidate{{n, strings.Trim(p[len(lead):], " /:-.,")}}
		}
		return r
	}

	if n, ok := ArticleNumber(p); ok {
		r.pure = n
		r.cands = []candidate{{n, ""}}
	}
	return r
}

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
