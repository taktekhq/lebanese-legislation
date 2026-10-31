package site

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type queryRead struct {
	Num     int
	NumWord bool
	Date    time.Time
	Year    int
	Kind    string
	Court   []string
	Words   []string
}

func (r queryRead) lawCite() bool {
	return len(r.Court) == 0 && (!r.Date.IsZero() || (r.Num > 0 && (r.Year > 0 || r.Kind != "" || r.NumWord)))
}

func (r queryRead) rulingCite() bool {
	return len(r.Court) > 0 && (r.Num > 0 || r.Year > 0)
}

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

var kindGroups = map[string][]string{
	"law":       {"قانون", "قانون منفذ بمرسوم", "قانون نافذ حكما", "قانون عثماني", "مرسوم إشتراعى"},
	"decree":    {"مرسوم", "مرسوم نافذ حكما", "مرسوم منفذ بقانون", "قانون منفذ بمرسوم", "مرسوم إشتراعى"},
	"decreelaw": {"مرسوم إشتراعى"},
	"decision":  {"قرار", "قرار اداري", "قرار اساسي", "قرار المفوض السامي", "قرار بلدي", "قرار مبدئي", "قرار مشترك", "قرار نافذ حكما", "قرار وسيط"},
	"system":    {"نظام", "نظام داخلي"},
	"hc":        {"قرار المفوض السامي"},
}

var kindNear = map[string][]string{
	"law":       kindGroups["decree"],
	"decree":    kindGroups["law"],
	"decreelaw": append(append([]string{}, kindGroups["law"]...), kindGroups["decree"]...),
	"hc":        kindGroups["decision"],
}

var kindOwn = map[string][]string{
	"law": {"قانون", "قانون منفذ بمرسوم", "قانون نافذ حكما", "قانون عثماني"},
}

func ownKinds(kind string) []string {
	if k, ok := kindOwn[kind]; ok {
		return k
	}
	return kindGroups[kind]
}

var kindPhrases = []struct{ in, kind string }{
	{"قرار المفوض السامي", "hc"}, {"القرار المفوض السامي", "hc"}, {"المفوض السامي", "hc"},
	{"المرسوم الاشتراعي", "decreelaw"}, {"مرسوم اشتراعي", "decreelaw"}, {"decret-loi", "decreelaw"}, {"decret loi", "decreelaw"},
	{"decree-law", "decreelaw"}, {"legislative decree", "decreelaw"},
	{"القانون", "law"}, {"قانون", "law"}, {"القوانين", "law"}, {"قوانين", "law"}, {"loi", "law"}, {"law", "law"},
	{"المرسوم", "decree"}, {"مرسوم", "decree"}, {"المراسيم", "decree"}, {"مراسيم", "decree"}, {"decret", "decree"}, {"decree", "decree"},
	{"القرار", "decision"}, {"قرار", "decision"}, {"القرارات", "decision"}, {"قرارات", "decision"}, {"arrete", "decision"}, {"decision", "decision"},
}

var courtWords = map[string]string{
	"تمييز": "تمييز", "التمييز": "تمييز", "تميز": "تمييز", "التميز": "تمييز",
	"استئناف": "استئناف", "الاستئناف": "استئناف", "إستئناف": "استئناف",
	"شورى": "شورى", "الشورى": "شورى",
	"جنايات": "الجنايات", "الجنايات": "الجنايات",
	"جزائي": "جزائي", "الجزائية": "جزائي", "جزائية": "جزائي", "الجزائي": "جزائي",
	"مدني": "مدني", "المدنية": "مدني", "مدنية": "مدني", "المدني": "مدني",
	"cassation": "تمييز",
}

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
	rqNumLR     = regexp.MustCompile(`(\d{1,5})\s*/\s*[^\d\s]`)
	rqWordDay   = regexp.MustCompile(`(\d{1,2})\s+([^\s\d]+(?:\s+(?:الثاني|الاول|الأول))?)\s+((?:18|19|20)\d{2})`)
	rqShortPair = regexp.MustCompile(`(\d{1,2})\s*/\s*(\d{1,4})`)
	rqBare      = regexp.MustCompile(`\d{1,5}`)
	rqLR        = regexp.MustCompile(`(?:^|[\s/\-])(?:ل\s*\.?\s*ر|l\s*\.\s*r\.?|lr)(?:[\s.]|$)`)
)

var (
	tqGlueAD   = regexp.MustCompile(`([\x{0620}-\x{064A}\x{066E}-\x{06D3}])([0-9])`)
	tqGlueDA   = regexp.MustCompile(`([0-9])([\x{0620}-\x{064A}\x{066E}-\x{06D3}])`)
	tqGazette  = regexp.MustCompile(`(?i)[\s,،|(\-]*(?:(?:في\s+)?(?:عدد\s+)?(?:ال)?جريدة\s+(?:ال)?رسمية|تاريخ\s+النشر|(?:the\s+)?official\s+gazette|date\s+of\s+publication|(?:au\s+)?journal\s+officiel|j\.?\s*o\.?\s+n)`)
	tqLeadJunk = "•·▪◦‣*-–,>|،,.:؛ \t "
)

func tidyQuery(q string) (tidy string, cut bool) {
	q = strings.TrimLeft(arDigits.Replace(q), tqLeadJunk)
	q = tqGlueDA.ReplaceAllString(tqGlueAD.ReplaceAllString(q, "$1 $2"), "$1 $2")

	if loc := tqGazette.FindStringIndex(q); loc != nil && strings.ContainsAny(q[:loc[0]], "0123456789") {
		q, cut = q[:loc[0]], true
	}
	return strings.Join(strings.Fields(strings.TrimRight(q, tqLeadJunk)), " "), cut
}

var monthNumber = map[string]time.Month{
	"janvier": 1, "fevrier": 2, "mars": 3, "avril": 4, "mai": 5, "juin": 6, "juillet": 7, "aout": 8, "septembre": 9, "octobre": 10, "novembre": 11, "decembre": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6, "july": 7, "august": 8, "september": 9, "october": 10, "november": 11, "december": 12,

	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
	"janv": 1, "fevr": 2, "fev": 2, "avr": 4, "juil": 7,
	"كانون الثاني": 1, "شباط": 2, "اذار": 3, "آذار": 3, "نيسان": 4, "ايار": 5, "أيار": 5, "حزيران": 6, "تموز": 7, "اب": 8, "آب": 8,
	"ايلول": 9, "أيلول": 9, "تشرين الاول": 10, "تشرين الأول": 10, "تشرين الثاني": 11, "كانون الاول": 12, "كانون الأول": 12,
}

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
		w = strings.Trim(w, "()«»\"'.:؛,،")
		if c, ok := courtWords[w]; ok {
			r.Court = append(r.Court, c)
			continue
		}
		if w == "" || citeStop[w] {
			continue
		}
		if rqBare.FindString(w) == w {
			n, _ := strconv.Atoi(w)
			switch {
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
