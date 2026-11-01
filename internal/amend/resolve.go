package amend

import (
	"strconv"
	"strings"
	"time"
)

// Law is what the resolver knows of a text in the database.
type Law struct {
	ID      int
	Kind    string // the Centre's kind, as printed
	Number  string
	Issued  time.Time // zero when unknown
	Gazette time.Time // publication in the Official Gazette; zero when unknown
	Title   string
	InForce bool
	Code    bool // one of the principal codes
}

// Group is the kind as amendments cite it.
func Group(kind string) string {
	k := FoldString(kind)
	switch {
	case k == "قانون منفذ بمرسوم":
		return KindLawByDecree
	case strings.HasPrefix(k, "مرسوم اشتراعي"), strings.HasPrefix(k, "مرسوم اشتراعى"):
		return KindDecreeLaw
	case strings.HasPrefix(k, "مرسوم"):
		return KindDecree
	case strings.HasPrefix(k, "قانون"):
		return KindLaw
	case strings.HasPrefix(k, "قرار"):
		return KindDecision
	}
	return ""
}

// Index finds the text a Ref names.
type Index struct {
	laws    map[int]*Law
	byNum   map[string][]int // group|number
	byDate  map[string][]int // group|date
	byTitle map[string][]int // folded title, without a leading "قانون"

	nameAlias map[string]int // title key of a name -> text (db/aliases.tsv)

	allowLater    bool // see AllowLater
	allowDateSlip bool // see AllowDateSlip
}

// AllowDateSlip lets a citation whose full date matches no text settle on the
// one law, decree, decree-law or law-by-decree that has its number in the
// same year (the date is a few days off: "10830 of 19/10/1962" for the law
// dated 9/10/1962). Never for decisions, whose numbers repeat within a year,
// never across years, never when two texts qualify. Off by default; the
// graph turns it on.
func (ix *Index) AllowDateSlip() { ix.allowDateSlip = true }

// AllowLater lets a number and full date that fit one text settle on it even
// when that text is dated after the citing one. Off by default: the
// amendments pass relies on a text only amending an earlier one.
func (ix *Index) AllowLater() { ix.allowLater = true }

func NewIndex(laws []Law) *Index {
	ix := &Index{laws: map[int]*Law{}, byNum: map[string][]int{}, byDate: map[string][]int{}, byTitle: map[string][]int{}}
	for i := range laws {
		l := &laws[i]
		ix.laws[l.ID] = l
		g := Group(l.Kind)
		if n := leadingInt(l.Number); n > 0 {
			ix.byNum[g+"|"+strconv.Itoa(n)] = append(ix.byNum[g+"|"+strconv.Itoa(n)], l.ID)
		}
		if !l.Issued.IsZero() {
			k := g + "|" + l.Issued.Format("2006-01-02")
			ix.byDate[k] = append(ix.byDate[k], l.ID)
		}
		t := titleKey(l.Title)
		ix.byTitle[t] = append(ix.byTitle[t], l.ID)
	}
	return ix
}

// Law returns a text by id.
func (ix *Index) Law(id int) *Law { return ix.laws[id] }

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// titleKey folds a title for matching names: no "قانون" in front, no
// parentheses, no final period.
func titleKey(s string) string {
	s = FoldString(s)
	if i := strings.Index(s, "("); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimRight(s, ". "))
	s = strings.TrimPrefix(s, "قانون ")
	return strings.TrimPrefix(s, "ال")
}

// Resolve finds the text r names, before the date the amendment was made
// (a text can only amend an earlier one). It says how it found it, or why
// not ("" id).
func (ix *Index) Resolve(r Ref, before time.Time) (int, string) {
	earlier := func(ids []int) []int {
		var out []int
		for _, id := range ids {
			l := ix.laws[id]
			if before.IsZero() || l.Issued.IsZero() || !l.Issued.After(before) {
				out = append(out, id)
			}
		}
		return out
	}
	groups := []string{r.Kind}
	switch r.Kind {
	case "":
		groups = []string{KindLaw, KindDecreeLaw, KindLawByDecree}
	case KindLaw:
		groups = []string{KindLaw, KindLawByDecree}
	case KindLawByDecree:
		groups = []string{KindLawByDecree, KindLaw, KindDecree}
	}
	if r.Num > 0 {
		var cands []int
		for _, g := range groups {
			cands = append(cands, ix.byNum[g+"|"+strconv.Itoa(r.Num)]...)
		}
		cands = earlier(cands)
		if !r.Date.IsZero() {
			if ids := ix.filter(cands, func(l *Law) bool { return l.Issued.Equal(r.Date) || l.Gazette.Equal(r.Date) }); len(ids) == 1 {
				return ids[0], "number and date"
			}
		}
		if r.Year > 0 {
			if ids := ix.filter(cands, func(l *Law) bool { return l.Issued.Year() == r.Year }); len(ids) == 1 {
				return ids[0], "number and year"
			}
		}
		if r.Name != "" {
			if ids := ix.filter(cands, func(l *Law) bool { return nameMatch(l.Title, r.Name) }); len(ids) == 1 {
				return ids[0], "number and name"
			}
		}
		// Older texts cite a decree-law as "القانون": any kind, same number
		// and date.
		if !r.Date.IsZero() {
			var any []int
			for _, g := range []string{KindLaw, KindDecreeLaw, KindLawByDecree, KindDecree, KindDecision, ""} {
				any = append(any, ix.byNum[g+"|"+strconv.Itoa(r.Num)]...)
			}
			same := func(l *Law) bool { return l.Issued.Equal(r.Date) }
			if ids := ix.filter(earlier(any), same); len(ids) == 1 {
				return ids[0], "number and date, other kind"
			}
			// A full number and date that fit one text, dated after the
			// citing one: the Centre's republished texts carry later
			// citations, or a date is off. Only where asked (the graph).
			if ix.allowLater && !before.IsZero() {
				if ids := ix.filter(any, same); len(ids) == 1 {
					return ids[0], "number and date, the text is later than the citing one"
				}
			}
		}
		if ix.allowDateSlip && !r.Date.IsZero() {
			if id, how := ix.dateSlip(r); id > 0 {
				return id, how
			}
		}
		if len(cands) == 0 {
			return 0, "no text with that number"
		}
		return 0, "several texts with that number"
	}
	if !r.Date.IsZero() {
		var cands []int
		for _, g := range groups {
			cands = append(cands, ix.byDate[g+"|"+r.Date.Format("2006-01-02")]...)
		}
		if r.Name != "" {
			if ids := ix.filter(cands, func(l *Law) bool { return nameMatch(l.Title, r.Name) }); len(ids) == 1 {
				return ids[0], "date and name"
			}
		}
		if len(cands) == 1 {
			return cands[0], "date"
		}
	}
	if r.Name != "" {
		// A name a person has checked (db/aliases.tsv), unless the text it
		// names came after the citing one.
		if id, ok := ix.nameAlias[titleKey(r.Name)]; ok {
			if l := ix.laws[id]; l != nil && (before.IsZero() || l.Issued.IsZero() || !l.Issued.After(before)) {
				return id, "alias"
			}
		}
		if a, ok := aliases[titleKey(r.Name)]; ok {
			r.Name = a
		}
		cands := earlier(ix.byTitle[titleKey(r.Name)])
		if len(cands) > 1 { // the code, then the one in force
			if c := ix.filter(cands, func(l *Law) bool { return l.Code }); len(c) == 1 {
				cands = c
			} else if c := ix.filter(cands, func(l *Law) bool { return l.InForce }); len(c) == 1 {
				cands = c
			}
		}
		if len(cands) == 1 {
			return cands[0], "name"
		}
		// A short name for a code: "قانون التجاره" for "قانون التجاره البريه".
		var codes []int
		for _, l := range ix.laws {
			if l.Code && strings.HasPrefix(titleKey(l.Title), titleKey(r.Name)) {
				codes = append(codes, l.ID)
			}
		}
		if len(codes) == 1 {
			return codes[0], "code name"
		}
		return 0, "name not found"
	}
	return 0, "names no text"
}

// dateSlip finds the text a citation names when its full date is off: no
// text with that number has the date, and exactly one law, decree,
// decree-law or law-by-decree has the number in the date's year.
func (ix *Index) dateSlip(r Ref) (int, string) {
	if r.Kind == "" || r.Kind == KindDecision {
		return 0, ""
	}
	n := strconv.Itoa(r.Num)
	for _, g := range []string{KindLaw, KindDecreeLaw, KindDecree, KindDecision, KindLawByDecree, ""} {
		for _, id := range ix.byNum[g+"|"+n] {
			if l := ix.laws[id]; l.Issued.Equal(r.Date) || l.Gazette.Equal(r.Date) {
				return 0, "" // the date fits a text: not a slip
			}
		}
	}
	var cands []int
	for _, g := range []string{KindLaw, KindDecreeLaw, KindDecree, KindLawByDecree} {
		for _, id := range ix.byNum[g+"|"+n] {
			if ix.laws[id].Issued.Year() == r.Date.Year() {
				cands = append(cands, id)
			}
		}
	}
	if len(cands) != 1 {
		return 0, ""
	}
	l := ix.laws[cands[0]]
	if !slipKinds(r.Kind, Group(l.Kind)) {
		return 0, ""
	}
	return l.ID, "date in the citation differs: cited " + r.Date.Format("2006-01-02") + ", text dated " + l.Issued.Format("2006-01-02")
}

// slipKinds says whether a citation of kind cited can mean a text of kind
// found: the same kind, a decree and a law-by-decree ("القانون المنشور
// بالمرسوم" is cited as either), and a law and a law-by-decree.
func slipKinds(cited, found string) bool {
	if cited == found {
		return true
	}
	pair := func(a, b string) bool { return (cited == a && found == b) || (cited == b && found == a) }
	return pair(KindDecree, KindLawByDecree) || pair(KindLaw, KindLawByDecree)
}

// Names amendments use for texts titled otherwise.
var aliases = map[string]string{
	"محاسبه العامه":   "المحاسبه العموميه",
	"محاسبه العموميه": "المحاسبه العموميه",
	"تجاره":           "التجاره البريه",
	"موجبات":          "الموجبات والعقود",
	"ضريبه علي الدخل": "ضريبه الدخل",
}

func (ix *Index) filter(ids []int, keep func(*Law) bool) []int {
	var out []int
	for _, id := range ids {
		if keep(ix.laws[id]) {
			out = append(out, id)
		}
	}
	return out
}

func nameMatch(title, name string) bool {
	t, n := titleKey(title), titleKey(name)
	return t == n || strings.Contains(t, n) || strings.Contains(n, t)
}

// TitleRef reads the text an amending text names in its own title
// ("تعديل الماده 73 من قانون اصول المحاكمات الجزائيه رقم 328 …"): the first
// reference the index can resolve. It is the fallback for articles that
// name no text ("تعدل الماده 47 لتصبح …").
func (ix *Index) TitleRef(title string, before time.Time) (int, string) {
	x := FoldString(title)
	for i := 0; i < len(x); i++ {
		if i > 0 && x[i-1] != ' ' && x[i-1] != '(' {
			continue
		}
		r, n := ref(x[i:])
		if n == 0 || r.Self || r.Same || !r.Named() {
			continue
		}
		if id, how := ix.Resolve(r, before); id > 0 {
			return id, "title: " + how
		}
	}
	return 0, ""
}
