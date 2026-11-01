package amend

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kinds of text an amendment names, grouped the way they are cited.
const (
	KindLaw         = "law"           // القانون
	KindDecreeLaw   = "decree-law"    // المرسوم الاشتراعي
	KindDecree      = "decree"        // المرسوم
	KindDecision    = "decision"      // القرار
	KindLawByDecree = "law-by-decree" // مشروع القانون الموضوع موضع التنفيذ بالمرسوم
)

// Ref is a text as an amendment names it: "القانون رقم 328 تاريخ 2 آب 2001
// (أصول المحاكمات الجزائية)", "قانون العمل الصادر في 23/9/1946", "قانون 4
// كانون الأول سنة 1944". Any part can be missing.
type Ref struct {
	Kind   string
	Number string    // as written, folded: "328", "28/2017", "1042/1"
	Num    int       // its leading integer
	Year   int       // from the date, or the number ("28/2017", "8/70")
	Date   time.Time // zero when not given
	Name   string    // "اصول المحاكمات الجزائيه", "العمل"; folded
	Same   bool      // "القانون نفسه", "المذكور": the text named before
	Self   bool      // "هذا القانون": the amending text itself
}

// Named says whether the ref names anything at all.
func (r Ref) Named() bool { return r.Kind != "" || r.Num > 0 || r.Name != "" || !r.Date.IsZero() }

var months = []struct {
	name string
	n    time.Month
}{ // longest first, so "كانون الثاني" wins over a bare "كانون"
	{"كانون الثاني", 1}, {"كانون الاول", 12}, {"تشرين الثاني", 11}, {"تشرين الاول", 10},
	{"شباط", 2}, {"اذار", 3}, {"نيسان", 4}, {"ايار", 5}, {"حزيران", 6}, {"تموز", 7}, {"اب", 8}, {"ايلول", 9},
	{"يناير", 1}, {"فبراير", 2}, {"مارس", 3}, {"ابريل", 4}, {"مايو", 5}, {"يونيو", 6}, {"يوليو", 7},
	{"اغسطس", 8}, {"سبتمبر", 9}, {"اكتوبر", 10}, {"نوفمبر", 11}, {"ديسمبر", 12},
}

var (
	numericDate = regexp.MustCompile(`^(\d{1,4}) ?[/\-.] ?(\d{1,2}) ?[/\-.] ?(\d{1,4})`)
	wordDate    = regexp.MustCompile(`^(\d{1,2}) ([^\d]{2,14}?) (?:سنه |عام |من عام |من سنه )?(\d{4})`)
)

// date reads a date at the start of s: "2/8/2001", "2001/8/2", "2 اب 2001",
// "4 كانون الاول سنه 1944".
func date(s string) (time.Time, int) {
	if m := numericDate.FindStringSubmatch(s); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		d, mo, y := a, b, c
		if a > 31 { // year first
			d, y = c, a
		}
		if y < 100 {
			y += 1900
		}
		if t, ok := mkdate(y, mo, d); ok {
			return t, len(m[0])
		}
		return time.Time{}, 0
	}
	if m := wordDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[3])
		for _, mo := range months {
			if m[2] == mo.name {
				if t, ok := mkdate(y, int(mo.n), d); ok {
					return t, len(m[0])
				}
			}
		}
	}
	return time.Time{}, 0
}

func mkdate(y, m, d int) (time.Time, bool) {
	if y < 1850 || y > 2100 || m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t, t.Day() == d
}

var (
	kindWords = []struct {
		re   *regexp.Regexp
		kind string
	}{
		{regexp.MustCompile(`^(?:مشروع )?القانون (?:المعجل )?(?:الموضوع موضع التنفيذ|المنفذ|النافذ|الصادر|المنشور|الموضوع)[^:]{0,24}? (?:بالمرسوم|بموجب المرسوم|بمرسوم)`), KindLawByDecree},
		{regexp.MustCompile(`^مشروع القانون[^:]{0,40}? (?:بالمرسوم|بموجب المرسوم|بمرسوم)`), KindLawByDecree},
		{regexp.MustCompile(`^(?:ال)?مرسوم (?:ال)?اشتراعي`), KindDecreeLaw},
		{regexp.MustCompile(`^(?:ال)?مرسوم(?: (?:ال)?نافذ حكما| (?:ال)?تنظيمي| (?:ال)?تطبيقي)?`), KindDecree},
		{regexp.MustCompile(`^(?:ال)?قانون(?: (?:ال)?نافذ حكما| (?:ال)?معجل)?`), KindLaw},
		{regexp.MustCompile(`^(?:ال)?قرار(?: (?:ال)?اساسي| (?:ال)?وسيط| (?:ال)?مبدئي| (?:ال)?نافذ حكما| (?:ال)?تنظيمي)?`), KindDecision},
	}
	refNumber = regexp.MustCompile(`^ (?:رقم |الرقم |ذي الرقم |رقمه )?(\d{1,6})((?: ?/ ?(?:[اأ-ي]{1,3}|\d{1,4})){0,3})(?:[^\d/]|$)`)
	// "المتعلق ب…", "القاضي ب…": a text's subject, to tell apart texts
	// with the same number.
	subject    = regexp.MustCompile(`^,? ?(?:و?المتعلق|و?المتعلقه|القاضي|القاضيه|الرامي|الراميه|الخاص|الخاصه) (?:ب|بال)?`)
	dateLead   = regexp.MustCompile(`^ (?:تاريخ|بتاريخ|الصادر في|الصادر بتاريخ|الصادر تاريخ|المؤرخ في|المؤرخ|الصادر|الصادره في|الصادره بتاريخ|تاريخ صدوره|في) `)
	refSame    = regexp.MustCompile(`^(?:ذاته|نفسه|المذكور|المشار اليه|عينه)`)
	nameStop   = regexp.MustCompile(` (?:الصادر|الصادره|تاريخ|بتاريخ|المعدل|المعدله|وتعديلاته|وتعديلاتها|لتصبح|ليصبح|بحيث|علي|كما|ويستعاض|ويستبدل|وتستبدل|المنفذ|رقم|الموضوع|والمتعلق|المتعلق|المتعلقه|والمتعلقه|القاضي|وذلك|اعتبارا|بالنص|بالنصوص|فتصبح|وتصبح|ويصبح|عوضا|بدلا|من|في|الي) |[,:(.«"]`)
	parenName  = regexp.MustCompile(`^ ?\(([^()]{3,120})\)`)
	byDecree   = regexp.MustCompile(`^ (?:الموضوع موضع التنفيذ|المنفذ|الصادر|النافذ|المنشور)[^:]{0,20}? (?:بالمرسوم|بموجب المرسوم|بمرسوم) (?:رقم )?(\d{1,6})`)
	regulation = regexp.MustCompile(`^(?:ال)?نظام [^ ]{3}`)
	selfRef    = regexp.MustCompile(`^(?:هذا|هذه) (?:القانون|المرسوم|القرار|النظام)`)
)

// ref reads a text reference at the start of s and the bytes used. It reads
// kind, number and date, a name ("قانون العمل") when there is no number,
// and a name in parentheses after it.
func ref(s string) (Ref, int) {
	var r Ref
	if m := selfRef.FindStringIndex(s); m != nil {
		r.Self = true
		return r, m[1]
	}
	pos := -1
	for _, k := range kindWords {
		if m := k.re.FindStringIndex(s); m != nil {
			r.Kind, pos = k.kind, m[1]
			break
		}
	}
	if pos < 0 {
		// "نظام الموظفين", "نظام الشركات المحصور نشاطها …": known by title.
		if m := regulation.FindStringIndex(s); m != nil {
			rest := s[m[0]:]
			end := len(rest)
			if k := nameStop.FindStringIndex(rest); k != nil {
				end = k[0]
			}
			if end > 0 && end <= 90 {
				r.Name = strings.TrimSpace(strings.TrimPrefix(rest[:end], "ال"))
				return r.after(s, m[0]+end)
			}
		}
		return r, 0
	}
	// "القانون نفسه", "المرسوم المذكور"
	if m := refSame.FindStringIndex(strings.TrimPrefix(s[pos:], " ")); m != nil {
		r.Same = true
		return r, pos + 1 + m[1]
	}
	// "قانون 4 كانون الاول سنه 1944": a text known by its date.
	if strings.HasPrefix(s[pos:], " ") {
		if t, n := date(s[pos+1:]); n > 0 {
			r.Date, r.Year = t, t.Year()
			pos += 1 + n
			return r.after(s, pos)
		}
	}
	if m := refNumber.FindStringSubmatchIndex(s[pos:]); m != nil {
		r.Number = s[pos+m[2] : pos+m[3]]
		r.Num, _ = strconv.Atoi(r.Number)
		if tail := strings.ReplaceAll(s[pos+m[4]:pos+m[5]], " ", ""); tail != "" {
			r.Number += tail
			// The year is the last part: "28/2017", "8/70", "254/م/2022".
			parts := strings.Split(tail, "/")
			last := parts[len(parts)-1]
			if y, err := strconv.Atoi(last); err == nil {
				if len(last) == 4 && y > 1850 && y < 2100 {
					r.Year = y
				} else if len(last) == 2 && len(parts) == 2 {
					r.Year = 1900 + y
					if r.Year < 1920 {
						r.Year += 100
					}
				}
			}
		}
		pos += m[5]
	} else if strings.HasPrefix(s[pos:], " ") && r.Kind != KindLawByDecree {
		// "قانون العمل", "قانون موازنه 1985": a name, up to the next stop.
		rest := s[pos+1:]
		end := len(rest)
		if m := nameStop.FindStringIndex(rest); m != nil {
			end = m[0]
		}
		if end > 0 && end <= 90 {
			r.Name = strings.TrimSpace(rest[:end])
			pos += 1 + end
		}
	}
	return r.after(s, pos)
}

// after reads the date and the parenthesised name that may follow a ref.
func (r Ref) after(s string, pos int) (Ref, int) {
	// "قانون الرسوم القضائيه وتعديلاته الصادر بتاريخ …"
	for _, w := range []string{" وتعديلاته", " وتعديلاتها"} {
		if strings.HasPrefix(s[pos:], w) && dateLead.MatchString(s[pos+len(w):]) {
			pos += len(w)
		}
	}
	if r.Date.IsZero() {
		if m := dateLead.FindStringIndex(s[pos:]); m != nil {
			if t, n := date(s[pos+m[1]:]); n > 0 {
				r.Date = t
				if r.Year == 0 {
					r.Year = t.Year()
				}
				pos += m[1] + n
			}
		}
	}
	// "قانون المحاسبه العموميه الموضوع موضع التنفيذ بالمرسوم رقم 14969":
	// a named law known by the decree that brought it into force.
	if r.Num == 0 && r.Name != "" {
		if m := byDecree.FindStringSubmatch(s[pos:]); m != nil {
			r.Kind, r.Number = KindLawByDecree, m[1]
			r.Num, _ = strconv.Atoi(m[1])
			pos += len(m[0])
			if r.Date.IsZero() {
				if m := dateLead.FindStringIndex(s[pos:]); m != nil {
					if t, n := date(s[pos+m[1]:]); n > 0 {
						r.Date, r.Year = t, t.Year()
						pos += m[1] + n
					}
				}
			}
		}
	}
	if r.Name == "" {
		if m := subject.FindStringIndex(s[pos:]); m != nil {
			rest := s[pos+m[1]:]
			end := len(rest)
			if k := nameStop.FindStringIndex(rest); k != nil {
				end = k[0]
			}
			if end >= 6 && end <= 120 {
				r.Name = strings.TrimSpace(rest[:end])
			}
		}
	}
	rest := s[pos:]
	for _, skip := range []string{" وتعديلاته", " وتعديلاتها", " المعدل", ","} {
		if strings.HasPrefix(rest, skip+" (") {
			rest = rest[len(skip):]
			pos += len(skip)
		}
	}
	if m := parenName.FindStringSubmatchIndex(rest); m != nil {
		name := strings.TrimSpace(rest[m[2]:m[3]])
		name = strings.TrimPrefix(name, "قانون ")
		if r.Name == "" {
			r.Name = name
		}
		pos += m[1]
	}
	return r, pos
}

// FindDate finds the first date anywhere in a folded string ("قانون 17 شباط
// سنه 1928"), for citations that carry their date in a name.
func FindDate(s string) time.Time {
	for i := 0; i < len(s); i++ {
		if i > 0 && s[i-1] != ' ' {
			continue
		}
		if t, n := date(s[i:]); n > 0 {
			return t
		}
	}
	return time.Time{}
}
