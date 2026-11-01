package site

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reading a search for the parts that name a text rather than describe it.
// "القانون رقم 175 تاريخ 8/5/2020", "décret 11614 de 1969", "قانون الجنسية
// 31/1/1946", "قرار رقم 50/2002 تمييز جزائي": a number, a full date or a year,
// a kind of text and a court are matched exactly; the words left only rank.
// Before this (21 Oct 2026) the date was cut to its year, so "رقم 28 تاريخ
// 10/2/2017" listed seven texts numbered 28 of 2017 with the right one sixth,
// and a date with no number was dropped altogether.

type queryRead struct {
	Num     int       // the text's or ruling's number (0: none)
	NumWord bool      // the number came after "رقم", "n°", "no."
	Date    time.Time // a full date (zero: none)
	Year    int       // from the date, "513/2005", "de 1969" or a court search's year
	Kind    string    // law, decree, decreelaw or decision ("": none named)
	Court   []string  // words naming a court: تمييز, جزائي… (rulings)
	Words   []string  // the rest, which only rank
}

// lawCite says whether the query names a text: a full date, or a number with
// a year, a kind or "رقم".
func (r queryRead) lawCite() bool {
	return len(r.Court) == 0 && (!r.Date.IsZero() || (r.Num > 0 && (r.Year > 0 || r.Kind != "" || r.NumWord)))
}

// rulingCite says whether the query names a ruling: a court with a number or a year.
func (r queryRead) rulingCite() bool {
	return len(r.Court) > 0 && (r.Num > 0 || r.Year > 0)
}

// label is the citation as a reader would write it, for "nothing numbered … here".
func (r queryRead) label() string {
	var p []string
	if r.Num > 0 {
		p = append(p, strconv.Itoa(r.Num))
	}
	switch {
	case !r.Date.IsZero():
		p = append(p, r.Date.Format("02/01/2006"))
	case r.Year > 0 && r.Num > 0:
		p[len(p)-1] += "/" + strconv.Itoa(r.Year)
	case r.Year > 0:
		p = append(p, strconv.Itoa(r.Year))
	}
	return strings.Join(p, " ")
}

// kindGroups are the kinds a kind word covers. A decree-law is a law in
// everyday speech ("قانون الملكية المشتركة" is decree-law 88/1983), and a law
// put in force by decree is both.
var kindGroups = map[string][]string{
	"law":       {"قانون", "قانون منفذ بمرسوم", "قانون نافذ حكما", "قانون عثماني", "مرسوم إشتراعى"},
	"decree":    {"مرسوم", "مرسوم نافذ حكما", "مرسوم منفذ بقانون", "قانون منفذ بمرسوم", "مرسوم إشتراعى"},
	"decreelaw": {"مرسوم إشتراعى"},
	"decision":  {"قرار", "قرار اداري", "قرار اساسي", "قرار المفوض السامي", "قرار بلدي", "قرار مبدئي", "قرار مشترك", "قرار نافذ حكما", "قرار وسيط"},
	"system":    {"نظام", "نظام داخلي"},
	"hc":        {"قرار المفوض السامي"}, // "122/ل.ر": L.R. marks the High Commissioner's decisions
}

// kindNear are the kinds people confuse with a kind word, ranked right after
// the exact kinds: "مرسوم اشتراعي 1306" means law-by-decree 1306/1971 (the
// Foreign Ministry's rules), and there is no decree-law 1306. A kind in both
// lists (a decree-law is a law) ranks as exact only.
var kindNear = map[string][]string{
	"law":       kindGroups["decree"],
	"decree":    kindGroups["law"],
	"decreelaw": append(append([]string{}, kindGroups["law"]...), kindGroups["decree"]...),
	"hc":        kindGroups["decision"],
}

// kindOwn are the kinds a kind word names in its own words, ranked before the
// rest of its group: "قانون 146" is one of the four laws numbered 146, not the
// decree-laws 146 of 1959 that had ranked first by their length (23 Oct 2026).
// A kind word not listed here ranks its whole group as its own. Not "مرسوم":
// "المرسوم 112" is decree-law 112/1959 (the civil servants' rules) and
// "décret 11614" law-by-decree 11614/1969, as people cite them.
var kindOwn = map[string][]string{
	"law": {"قانون", "قانون منفذ بمرسوم", "قانون نافذ حكما", "قانون عثماني"},
}

func ownKinds(kind string) []string {
	if k, ok := kindOwn[kind]; ok {
		return k
	}
	return kindGroups[kind]
}

// kindPhrases, longest first, as they are written after ar_norm-like folding
// (see foldKindText).
var kindPhrases = []struct{ in, kind string }{
	{"قرار المفوض السامي", "hc"}, {"القرار المفوض السامي", "hc"}, {"المفوض السامي", "hc"},
	{"المرسوم الاشتراعي", "decreelaw"}, {"مرسوم اشتراعي", "decreelaw"}, {"decret-loi", "decreelaw"}, {"decret loi", "decreelaw"},
	{"decree-law", "decreelaw"}, {"legislative decree", "decreelaw"},
	{"القانون", "law"}, {"قانون", "law"}, {"القوانين", "law"}, {"قوانين", "law"}, {"loi", "law"}, {"law", "law"},
	{"المرسوم", "decree"}, {"مرسوم", "decree"}, {"المراسيم", "decree"}, {"مراسيم", "decree"}, {"decret", "decree"}, {"decree", "decree"},
	{"القرار", "decision"}, {"قرار", "decision"}, {"القرارات", "decision"}, {"قرارات", "decision"}, {"arrete", "decision"}, {"decision", "decision"},
}

// courtWords fold the ways a court is written to the words of ruling.court.
var courtWords = map[string]string{
	"تمييز": "تمييز", "التمييز": "تمييز", "تميز": "تمييز", "التميز": "تمييز",
	"استئناف": "استئناف", "الاستئناف": "استئناف", "إستئناف": "استئناف",
	"شورى": "شورى", "الشورى": "شورى",
	"جنايات": "الجنايات", "الجنايات": "الجنايات",
	"جزائي": "جزائي", "الجزائية": "جزائي", "جزائية": "جزائي", "الجزائي": "جزائي",
	"مدني": "مدني", "المدنية": "مدني", "مدنية": "مدني", "المدني": "مدني",
	"cassation": "تمييز",
}

// citeStop are words a citation carries that say nothing about the text.
var citeStop = map[string]bool{"رقم": true, "الرقم": true, "عدد": true, "تاريخ": true, "تاريح": true, "بتاريخ": true, "التاريخ": true,
	"الصادر": true, "الصادرة": true, "صادر": true, "صادرة": true, "المؤرخ": true, "المؤرخة": true, "مؤرخ": true, "وتعديلاته": true, "تعديلاته": true,
	"وتعديلاتها": true, "تعديلاتها": true, "المتعلق": true, "المتعلقة": true, "issued": true, "on": true,
	"محكمة": true, "غرفة": true, "الغرفة": true, "حكم": true, "الحكم": true, "سنة": true, "عام": true, "لعام": true, "لسنة": true,
	"no": true, "n°": true, "n": true, "number": true, "num": true, "du": true, "de": true, "of": true, "dated": true, "en": true, "the": true,
	"في": true, "من": true, "على": true, "الى": true, "إلى": true, "و": true, "،": true, ",": true, "-": true}

var (
	rqDate      = regexp.MustCompile(`(\d{1,2})\s*[/.\-]\s*(\d{1,2})\s*[/.\-]\s*((?:18|19|20)\d{2})`)
	rqNumYear   = regexp.MustCompile(`(\d{1,5})\s*/\s*((?:18|19|20)\d{2})\b`)
	rqNumWord   = regexp.MustCompile(`(?:رقم|الرقم|عدد|n°|no\.?|n\.|number)\s*:?\s*(\d{1,5})`)
	rqNumLR     = regexp.MustCompile(`(\d{1,5})\s*/\s*[^\d\s]`) // "122/ل.ر", "15-s" is read as 15 by the bare-number rule
	rqWordDay   = regexp.MustCompile(`(\d{1,2})\s+([^\s\d]+(?:\s+(?:الثاني|الاول|الأول))?)\s+((?:18|19|20)\d{2})`)
	rqShortPair = regexp.MustCompile(`(\d{1,2})\s*/\s*(\d{1,4})`)
	rqBare      = regexp.MustCompile(`\d{1,5}`)
	rqLR        = regexp.MustCompile(`(?:^|[\s/\-])(?:ل\s*\.?\s*ر|l\s*\.\s*r\.?|lr)(?:[\s.]|$)`)
)

// Pasted citations carry more than the search needs. tidyQuery mends what
// people paste: a list bullet ("•\tالقرار رقم 45"), a number glued to a word
// ("القانون30", "11244تاريخ"), and a gazette reference after the citation
// ("... عدد الجريدة الرسمية 3 تاريخ النشر 19/1/1949 الصفحة 30-45", "Official
// Gazette issue: 3 | Date of publication: ..."), whose second date and page
// numbers were read as the text's own and found nothing (22 Oct 2026).
// cut says the gazette part was set aside, so the page can say what it searched.
var (
	tqGlueAD   = regexp.MustCompile(`([\x{0620}-\x{064A}\x{066E}-\x{06D3}])([0-9])`)
	tqGlueDA   = regexp.MustCompile(`([0-9])([\x{0620}-\x{064A}\x{066E}-\x{06D3}])`)
	tqGazette  = regexp.MustCompile(`(?i)[\s,،|(\-]*(?:(?:في\s+)?(?:عدد\s+)?(?:ال)?جريدة\s+(?:ال)?رسمية|تاريخ\s+النشر|(?:the\s+)?official\s+gazette|date\s+of\s+publication|(?:au\s+)?journal\s+officiel|j\.?\s*o\.?\s+n)`)
	tqLeadJunk = "•·▪◦‣*-–\u2014>|،,.:؛ \t "
)

func tidyQuery(q string) (tidy string, cut bool) {
	q = strings.TrimLeft(arDigits.Replace(q), tqLeadJunk)
	q = tqGlueDA.ReplaceAllString(tqGlueAD.ReplaceAllString(q, "$1 $2"), "$1 $2")
	// Only after a citation: "قانون الجريدة الرسمية" is a search for that law.
	if loc := tqGazette.FindStringIndex(q); loc != nil && strings.ContainsAny(q[:loc[0]], "0123456789") {
		q, cut = q[:loc[0]], true
	}
	return strings.Join(strings.Fields(strings.TrimRight(q, tqLeadJunk)), " "), cut
}

var monthNumber = map[string]time.Month{
	"janvier": 1, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6, "juillet": 7, "aout": 8, "septembre": 9, "octobre": 10, "novembre": 11, "decembre": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6, "july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,
	// short forms ("decree 9341 17 nov 2012", 23 Oct 2026); "mars", "mai", "juin" are whole above
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
	"janv": 1, "fevr": 2, "fev": 2, "avr": 4, "juil": 7,
	"كانون الثاني": 1, "شباط": 2, "اذار": 3, "آذار": 3, "نيسان": 4, "ايار": 5, "أيار": 5, "حزيران": 6, "تموز": 7, "اب": 8, "آب": 8,
	"ايلول": 9, "أيلول": 9, "تشرين الاول": 10, "تشرين الأول": 10, "تشرين الثاني": 11, "كانون الاول": 12, "كانون الأول": 12,
}

// foldKindText lower-cases, folds accents, Arabic-Indic digits, backslashes and
// the hamza of «إشتراعي» so the patterns above can be plain strings.
func foldKindText(q string) string {
	q = latinFold(arDigits.Replace(q))
	q = strings.NewReplacer(`\`, "/", "إشتراعي", "اشتراعي", "إشتراعى", "اشتراعي", "اشتراعى", "اشتراعي", "ـ", "").Replace(q)
	return q
}

func validDate(d, m, y int) (time.Time, bool) {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d
}

// readQuery takes a search apart. It never fails: what it cannot place stays in Words.
func readQuery(q string) queryRead {
	var r queryRead
	s := " " + foldKindText(q) + " "
	cut := func(m string) { s = strings.Replace(s, m, " ", 1) }

	if m := rqDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if t, ok := validDate(d, mo, y); ok {
			r.Date, r.Year = t, y
		}
		cut(m[0])
	} else if m := rqWordDay.FindStringSubmatch(s); m != nil {
		if mo, ok := monthNumber[strings.TrimSuffix(m[2], ".")]; ok {
			d, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[3])
			if t, ok := validDate(d, int(mo), y); ok {
				r.Date, r.Year = t, y
				cut(m[0])
			}
		}
	}
	// Rulings are cited "year/number" too: «قرار رقم 93/33 تاريخ 19/1/1993» is
	// number 33 of 1993. When one side is the dated year's last two digits,
	// the other side is the number.
	if r.Year > 0 {
		if m := rqShortPair.FindStringSubmatch(s); m != nil {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			switch yy := r.Year % 100; {
			case a == yy && b != yy:
				r.Num = b
				cut(m[0])
			case b == yy && a != yy:
				r.Num = a
				cut(m[0])
			}
		}
	}
	if m := rqNumYear.FindStringSubmatch(s); m != nil && r.Year == 0 {
		r.Num, _ = strconv.Atoi(m[1])
		r.Year, _ = strconv.Atoi(m[2])
		cut(m[0])
	}
	if m := rqNumWord.FindStringSubmatch(s); m != nil && r.Num == 0 {
		r.Num, _ = strconv.Atoi(m[1])
		r.NumWord = true
		cut(m[0])
	}
	if m := rqNumLR.FindStringSubmatch(s); m != nil && r.Num == 0 {
		r.Num, _ = strconv.Atoi(m[1])
		cut(m[1])
	}
	lr := false
	if loc := rqLR.FindStringIndex(s); loc != nil {
		lr, s = true, s[:loc[0]]+" "+s[loc[1]:]
	}
	for _, k := range kindPhrases {
		if i := indexWord(s, k.in); i >= 0 {
			r.Kind = k.kind
			s = s[:i] + " " + s[i+len(k.in):]
			break
		}
	}
	if lr && (r.Kind == "" || r.Kind == "decision") {
		r.Kind = "hc"
	}
	var words []string
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, "()«»\"'.:؛,،") // "Decree 10897, ...": the comma is not part of the number
		if c, ok := courtWords[w]; ok {
			r.Court = append(r.Court, c)
			continue
		}
		if w == "" || citeStop[w] {
			continue
		}
		if rqBare.FindString(w) == w { // a bare number
			n, _ := strconv.Atoi(w)
			switch { // a year-like number is the year unless "رقم" said otherwise (cut above)
			case n >= 1800 && n <= 2100 && r.Year == 0:
				r.Year = n
			case r.Num == 0:
				r.Num = n
			default:
				words = append(words, w)
			}
			continue
		}
		words = append(words, w)
	}
	r.Words = words
	return r
}

// indexWord finds phrase in s as whole words (s is padded with spaces).
func indexWord(s, phrase string) int {
	for from := 0; ; {
		i := strings.Index(s[from:], phrase)
		if i < 0 {
			return -1
		}
		i += from
		before, after := i == 0 || s[i-1] == ' ', i+len(phrase) >= len(s) || s[i+len(phrase)] == ' '
		if before && after {
			return i
		}
		from = i + len(phrase)
	}
}
