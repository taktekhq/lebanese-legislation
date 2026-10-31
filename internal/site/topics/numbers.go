package topics

import (
	"regexp"
	"strconv"
	"strings"
)

func Numbers(s string, strict bool) map[string]bool {
	out := map[string]bool{}
	s = digitsASCII.Replace(s)
	for _, tok := range digitRun.FindAllString(s, -1) {
		addDigits(out, tok)
	}
	for _, v := range englishNumbers(s) {
		out[strconv.Itoa(v)] = true
	}
	if !strict && strings.Contains(arFold.Replace(s), "ونصف") {
		for i := 0; i <= 12; i++ {
			out[strconv.Itoa(i)+".5"] = true
		}
	}
	for _, v := range arabicNumbers(s, strict) {
		out[strconv.Itoa(v)] = true
	}
	return out
}

var digitsASCII = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
	"٫", ".", "٬", ",", "،", ",")

var digitRun = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

func canon(d string) string {
	d = strings.TrimLeft(d, "0")
	if d == "" {
		return "0"
	}
	return d
}

func addDigits(out map[string]bool, tok string) {
	parts := strings.FieldsFunc(tok, func(r rune) bool { return r == '.' || r == ',' })
	if len(parts) == 1 {
		out[canon(parts[0])] = true
		return
	}
	grouped := true
	for _, p := range parts[1:] {
		grouped = grouped && len(p) == 3
	}
	if !grouped {
		for _, p := range parts {
			out[canon(p)] = true
		}
	}
	out[canon(strings.Join(parts, ""))] = true
	if frac := strings.TrimRight(parts[len(parts)-1], "0"); len(parts) == 2 && frac != "" && strings.Contains(tok, ".") {
		out[canon(parts[0])+"."+frac] = true
	}
}

var enUnits = map[string]int{"two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
	"one": 1}
var enScales = map[string]int{"hundred": 100, "thousand": 1000, "million": 1000000}
var wordRun = regexp.MustCompile(`[A-Za-z]+`)

func englishNumbers(s string) []int {
	var out []int
	toks := wordRun.FindAllString(strings.ToLower(s), -1)
	for i := 0; i < len(toks); {
		if !isNumWord(toks[i]) {
			i++
			continue
		}
		total, cur, words, onlyOne := 0, 0, 0, true
		j := i
	phrase:
		for j < len(toks) {
			w := toks[j]
			if u, ok := enUnits[w]; ok {
				last := cur % 100
				if !(last == 0 || (last >= 20 && u < 10)) {
					break phrase
				}
				cur += u
				if w != "one" {
					onlyOne = false
				}
			} else if sc, ok := enScales[w]; ok {
				if cur == 0 && total == 0 {
					break phrase
				}
				if cur == 0 {
					cur = 1
				}
				if sc == 100 {
					cur *= 100
				} else {
					total += cur * sc
					cur = 0
				}
				onlyOne = false
			} else if w == "and" && words > 0 && j+1 < len(toks) && isNumWord(toks[j+1]) {
				j++
				continue
			} else {
				break phrase
			}
			words++
			j++
		}
		if v := total + cur; v > 0 && !(onlyOne && v == 1) {
			out = append(out, v)
		}
		i = j
		if words == 0 {
			i++
		}
	}
	return out
}

func isNumWord(w string) bool {
	_, a := enUnits[w]
	_, b := enScales[w]
	return a || b
}

var arFold = strings.NewReplacer("أ", "ا", "إ", "ا", "آ", "ا", "ى", "ي", "ة", "ه", "ـ", "", "ً", "", "ٌ", "", "ٍ", "", "َ", "", "ُ", "", "ِ", "", "ّ", "", "ْ", "", "ٰ", "")

var arWord = regexp.MustCompile(`[\p{Arabic}]+`)

type arNum struct {
	v    int
	kind byte
}

var arNums = func() map[string]arNum {
	m := map[string]arNum{}
	add := func(kind byte, v int, words ...string) {
		for _, w := range words {
			m[w] = arNum{v, kind}
		}
	}
	add('u', 1, "واحد", "واحده", "احد", "احدي")
	add('u', 2, "اثنان", "اثنين", "اثنا", "اثني", "اثنتان", "اثنتين", "اثنتا", "اثنتي", "اثنه")
	add('u', 3, "ثلاث", "ثلاثه")
	add('u', 4, "اربع", "اربعه")
	add('u', 5, "خمس", "خمسه")
	add('u', 6, "ست", "سته")
	add('u', 7, "سبع", "سبعه")
	add('u', 8, "ثمان", "ثماني", "ثمانيه", "ثمانية")
	add('u', 9, "تسع", "تسعه")
	add('u', 10, "عشره", "عشر")
	add('t', 20, "عشرون", "عشرين")
	add('t', 30, "ثلاثون", "ثلاثين")
	add('t', 40, "اربعون", "اربعين")
	add('t', 50, "خمسون", "خمسين")
	add('t', 60, "ستون", "ستين")
	add('t', 70, "سبعون", "سبعين")
	add('t', 80, "ثمانون", "ثمانين")
	add('t', 90, "تسعون", "تسعين")
	add('h', 100, "مئه", "مائه", "ميه")
	add('h', 200, "مئتان", "مئتين", "مائتان", "مائتين", "مئتي", "مائتي")
	add('k', 1000, "الف")
	add('k', 2000, "الفان", "الفين")
	add('k', 1000, "الاف", "آلاف")
	add('m', 1000000, "مليون", "ملايين", "مليونا")
	add('m', 2000000, "مليونان", "مليونين", "مليوني")

	add('h', 100, "مايه")
	add('h', 200, "مايتي", "مايتين", "مايتان")
	for pre, v := range map[string]int{"ثلاث": 3, "اربع": 4, "خمس": 5, "ست": 6, "سبع": 7, "ثمان": 8, "تسع": 9} {
		for _, suf := range []string{"مايه", "مئه", "مائه", "ميه"} {
			add('h', v*100, pre+suf)
		}
	}

	add('u', 1, "حادي", "حاديه", "اول", "اولي", "اوله")
	add('u', 2, "ثاني", "ثانيه")
	add('u', 3, "ثالث", "ثالثه")
	add('u', 4, "رابع", "رابعه")
	add('u', 5, "خامس", "خامسه")
	add('u', 6, "سادس", "سادسه")
	add('u', 7, "سابع", "سابعه")
	add('u', 8, "ثامن", "ثامنه")
	add('u', 9, "تاسع", "تاسعه")
	add('u', 10, "عاشر", "عاشره")
	add('d', 2, "سنتين", "سنتان", "شهرين", "شهران", "يومين", "يومان", "اسبوعين", "اسبوعان", "ساعتين", "ساعتان", "عامين", "عامان", "مرتين", "ليرتين", "نسختين", "ابنين", "ابنان", "بنتين", "بنتان", "اخوين", "اختين")
	return m
}()

func arabicNumbers(s string, strict bool) []int {
	var out []int
	folded := arFold.Replace(s)
	locs := arWord.FindAllStringIndex(folded, -1)
	toks := make([]string, len(locs))
	for i, l := range locs {
		toks[i] = folded[l[0]:l[1]]
	}

	joined := func(i int) bool { return strings.TrimSpace(folded[locs[i-1][1]:locs[i][0]]) == "" }
	i := 0
	for i < len(toks) {
		if _, ok := arLookup(toks[i]); !ok {
			i++
			continue
		}
		total, cur := 0, 0
		var parts []int
		j := i
		for ; j < len(toks); j++ {
			if j > i && !joined(j) {
				break
			}
			n, ok := arLookup(toks[j])
			if !ok {
				break
			}
			if n.kind == 'u' && cur != 0 && cur%10 != 0 && cur < 20 {
				break
			}
			parts = append(parts, n.v)
			switch n.kind {
			case 'u', 't', 'd':
				cur += n.v
			case 'a':
				if cur >= 1 && cur <= 9 {
					cur += 10
				} else {
					cur += n.v
				}
			case 'h':
				if n.v == 100 && cur >= 3 && cur <= 9 {
					cur *= 100
				} else if n.v == 100 && cur == 0 {
					cur = 100
				} else {
					cur += n.v
				}
			case 'k', 'm':
				g := n.v
				if (n.v == 1000 || n.v == 1000000) && cur > 0 && cur < 1000 {
					g = cur * n.v
					if !strict {
						out = append(out, cur)
					}
				}
				if !strict {
					out = append(out, g)
				}
				total += g
				cur = 0
			}
		}
		if v := total + cur; v > 0 {
			out = append(out, v)
		}
		if !strict {
			out = append(out, parts...)
			if cur > 0 {
				out = append(out, cur)
			}
		}
		if j == i {
			j = i + 1
		}
		i = j
	}
	return out
}

func arLookup(w string) (arNum, bool) {
	if strings.HasPrefix(w, "بال") {
		return arNum{}, false
	}
	if n, ok := arNums[w]; ok {
		if w == "عشر" || w == "عشره" {
			n.kind = 'a'
		}
		return n, true
	}
	for _, p := range []string{"ال", "و", "ب", "ل", "ف"} {
		if len(w) > 2*len(p) && strings.HasPrefix(w, p) {
			if n, ok := arLookup(w[len(p):]); ok {
				return n, true
			}
		}
	}
	return arNum{}, false
}
