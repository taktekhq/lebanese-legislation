package amend

import (
	"regexp"
	"strconv"
	"strings"
)

// Article numbers as amending texts write them: "73", "/5/", "73 مكرر",
// "12 مكرر 2", ordinals ("الثالثه والسبعون", "الحاديه عشره بعد المائه"),
// lists ("504 و505 و506", "الثانيه والرابعه") and ranges ("من 1 الي 30").
// All folded (see Fold).

var units = map[string]int{
	"اولي": 1, "اول": 1, "حاديه": 1, "حادي": 1, "واحده": 1, "واحد": 1,
	"ثانيه": 2, "ثاني": 2, "ثالثه": 3, "ثالث": 3, "رابعه": 4, "رابع": 4,
	"خامسه": 5, "خامس": 5, "سادسه": 6, "سادس": 6, "سابعه": 7, "سابع": 7,
	"ثامنه": 8, "ثامن": 8, "تاسعه": 9, "تاسع": 9, "عاشره": 10, "عاشر": 10,
}

var tens = map[string]int{
	"عشرون": 20, "عشرين": 20, "ثلاثون": 30, "ثلاثين": 30, "اربعون": 40, "اربعين": 40,
	"خمسون": 50, "خمسين": 50, "ستون": 60, "ستين": 60, "سبعون": 70, "سبعين": 70,
	"ثمانون": 80, "ثمانين": 80, "تسعون": 90, "تسعين": 90,
}

var hundreds = map[string]int{
	"مائه": 100, "مئه": 100, "مائتين": 200, "مئتين": 200, "مائتان": 200, "مئتان": 200,
	"ثلاثمائه": 300, "ثلاثمئه": 300, "اربعمائه": 400, "اربعمئه": 400, "خمسمائه": 500, "خمسمئه": 500,
	"ستمائه": 600, "ستمئه": 600, "سبعمائه": 700, "سبعمئه": 700, "ثمانمائه": 800, "ثمانمئه": 800,
	"تسعمائه": 900, "تسعمئه": 900,
}

// word strips a leading و and ال: "والعشرين" → "عشرين".
func word(tok string) (w string, and bool) {
	if strings.HasPrefix(tok, "و") && !strings.HasPrefix(tok, "واحد") {
		tok, and = strings.TrimPrefix(tok, "و"), true
	}
	return strings.TrimPrefix(tok, "ال"), and
}

// ordinal reads an ordinal number at the start of toks and says how many
// tokens it used: "الثالثه والسبعون" is 73, "الحاديه عشره" 11, "الثامنه
// عشره بعد المائه" 118, "الوحيده" 1 (a text's sole article).
func ordinal(toks []string) (n, used int) {
	if len(toks) == 0 {
		return 0, 0
	}
	w, and := word(toks[0])
	if and {
		return 0, 0
	}
	switch {
	case w == "وحيده" || w == "وحيد":
		return 1, 1
	case hundreds[w] > 0:
		return hundreds[w], 1
	case tens[w] > 0:
		n, used = tens[w], 1
	case units[w] > 0:
		n, used = units[w], 1
		if used < len(toks) && n < 10 {
			next, and := word(toks[used])
			if !and && (next == "عشره" || next == "عشر") {
				n, used = n+10, used+1
			} else if and && tens[next] > 0 {
				n, used = n+tens[next], used+1
			}
		}
	default:
		return 0, 0
	}
	// "… بعد المائه": past the hundred.
	if used+1 < len(toks) && toks[used] == "بعد" {
		if h := hundreds[strings.TrimPrefix(toks[used+1], "ال")]; h > 0 {
			n, used = n+h, used+2
		}
	}
	return n, used
}

var (
	digitsNo = regexp.MustCompile(`^(?:رقم )?/?(\d{1,4})/?(?: ?(مكرر)(?: ?(\d{1,2})(?:\D|$))?)?`)
	rangeSep = regexp.MustCompile(`^(?:الي|إلى|حتي|لغايه) `)
	// subNo is the "/1" of "12/1" or the " - 3" of "586 - 3"; dateTail
	// rules out a date ("12/9/1946"), a number with a year ("28/2017") and a
	// list marker after a heading ("المادة 23 - 1 - يحق").
	subNo    = regexp.MustCompile(`^ ?[/-] ?(?:\((\d{1,2})\)|(\d{1,2}))`)
	dateTail = regexp.MustCompile(`^(?:\d| ?[/-] ?\d| ?[-.)ـ])`)
)

// one reads a single article number at the start of s: its canonical form
// (as the database numbers articles: "73", "73 مكرر", "12 مكرر 2") and the
// bytes used.
func one(s string) (string, int) {
	if m := digitsNo.FindStringSubmatchIndex(s); m != nil {
		num := s[m[2]:m[3]]
		num = strings.TrimLeft(num, "0")
		if num == "" {
			return "", 0
		}
		end := m[3]
		if m[4] < 0 {
			// "12/1", "586 - 3": an article numbered under another one.
			if sm := subNo.FindStringSubmatchIndex(s[end:]); sm != nil {
				after := s[end+sm[1]:]
				if !dateTail.MatchString(after) {
					d := sm[2:4]
					if d[0] < 0 {
						d = sm[4:6]
					}
					return num + "-" + s[end+d[0]:end+d[1]], end + sm[1]
				}
			}
		}
		if m[4] >= 0 {
			num += " مكرر"
			end = m[5]
			if m[6] >= 0 {
				num += " " + s[m[6]:m[7]]
				end = m[7]
			}
		}
		if end < len(s) && s[end] == '/' {
			end++
		}
		return num, end
	}
	toks := strings.Split(s, " ")
	if n, used := ordinal(toks); used > 0 {
		end := len(strings.Join(toks[:used], " "))
		num := strconv.Itoa(n)
		if rest := s[end:]; strings.HasPrefix(rest, " مكرره") || strings.HasPrefix(rest, " مكرر") {
			num += " مكرر"
			end += len(" مكرر")
			if strings.HasPrefix(s[end:], "ه") {
				end += len("ه")
			}
		}
		return num, end
	}
	return "", 0
}

// numbers reads a list of article numbers at the start of s ("504 و505 و
// 506", "الثانيه والرابعه", "من 1 الي 30", "28 و 29") and the bytes used.
// A parenthesis after a number ("7 (فقرتها 2)") is skipped.
func numbers(s string) ([]string, int) {
	pos := 0
	if strings.HasPrefix(s, "من ") {
		pos = len("من ")
	}
	first, n := one(s[pos:])
	if n == 0 {
		return nil, 0
	}
	out := []string{first}
	pos += n
	for {
		rest := s[pos:]
		// A range: "1 الي 30".
		if m := rangeSep.FindStringIndex(strings.TrimPrefix(rest, " ")); m != nil {
			skip := len(rest) - len(strings.TrimPrefix(rest, " ")) + m[1]
			if last, n := one(rest[skip:]); n > 0 {
				a, errA := strconv.Atoi(out[len(out)-1])
				b, errB := strconv.Atoi(last)
				if errA == nil && errB == nil && b > a && b-a <= 300 {
					for k := a + 1; k <= b; k++ {
						out = append(out, strconv.Itoa(k))
					}
					pos += skip + n
					continue
				}
			}
		}
		// "(فقرتها 2)" after a number belongs to it.
		if strings.HasPrefix(rest, " (") {
			if j := strings.Index(rest, ")"); j > 0 && j < 40 {
				pos += j + 1
				rest = s[pos:]
			}
		}
		k := 0
		switch {
		case strings.HasPrefix(rest, " و "):
			k = len(" و ")
		case strings.HasPrefix(rest, " و"):
			k = len(" و")
		case strings.HasPrefix(rest, ", "):
			k = len(", ")
		case strings.HasPrefix(rest, " , "):
			k = len(" , ")
		default:
			return out, pos
		}
		// "والرابعه" is the next of a list; a compound ordinal ("الحاديه
		// والعشرين") was already read whole by one().
		next, n := one(rest[k:])
		if n == 0 {
			return out, pos
		}
		out = append(out, next)
		pos += k + n
	}
}

// ArticleNumber reads a printed article number ("الأولى", "١٢ مكرر", "وحيدة")
// as the database numbers articles ("1", "12 مكرر", "وحيدة"). ok is false
// when the words are not a number from start to end.
func ArticleNumber(printed string) (num string, ok bool) {
	s := FoldString(printed)
	if w := strings.TrimPrefix(s, "ال"); w == "وحيده" || w == "وحيد" { // not word(): its و is not "and" here
		return "وحيدة", true
	}
	n, used := one(s)
	if n == "" || used != len(s) {
		return "", false
	}
	return n, true
}
