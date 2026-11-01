package rulingfile

// Voting: a page's published text comes from several independent readings.
// Two readings (A and B) are aligned word by word. Where they agree, the word
// is published as A wrote it. Where they differ, every other reading votes:
// it is aligned to A too, and it backs A or B only if it wrote exactly the
// same words at that place (folded for spelling variants that don't change
// meaning: alef and yeh forms, tatweel, diacritics, digit scripts). The side
// with strictly more readers behind it wins; a tie stays marked with both
// readings (the site shows them, as for the Gazette scans). Words are never
// written here: what is published is always one reader's words.

import (
	"regexp"
	"strings"
	"unicode"
)

// Marks around a span the readers could not settle: start, A's words,
// separator, B's words, end. The site's marks() renders them.
const (
	markStart = ""
	markSep   = ""
	markEnd   = ""
)

// Vote is how one page's merge went.
type Vote struct {
	Words    int // words in A
	Agreed   int // words A and B wrote alike
	Settled  int // disputed spans a majority settled
	Disputed int // disputed spans left marked
}

type word struct {
	s  string
	nl bool // the reader ended a line after it
}

// numSep joins the parts of a number or date that a reader spaced out
// ("٢١ / ٦ / ٢٠٠٢" is "٢١/٦/٢٠٠٢"): only whitespace goes, never a digit.
// Each pattern needs real whitespace, so a part already joined never matches
// again and the next gap is reached on the next pass.
var (
	numSpaceBefore = regexp.MustCompile(`([0-9٠-٩۰-۹])[ \t]+([/\-.][ \t]*[0-9٠-٩۰-۹])`)
	numSpaceAfter  = regexp.MustCompile(`([0-9٠-٩۰-۹][/\-.])[ \t]+([0-9٠-٩۰-۹])`)
)

func words(text string) []word {
	for range 4 { // "١ / ٢ / ٣" has overlapping gaps
		text = numSpaceBefore.ReplaceAllString(text, "${1}${2}")
		text = numSpaceAfter.ReplaceAllString(text, "${1}${2}")
	}
	var out []word
	for _, ln := range strings.Split(text, "\n") {
		f := strings.Fields(ln)
		for i, x := range f {
			out = append(out, word{x, i == len(f)-1})
		}
	}
	return out
}

var foldWord = strings.NewReplacer("أ", "ا", "إ", "ا", "آ", "ا", "ى", "ي", "ـ", "",
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
	"‎", "", "‏", "")

// note is a reader's bracketed remark in place of text ("[توقيع]", "[ختم]",
// "[؟]"): all of them mean the same to a vote, that the reader wrote no text there.
var note = regexp.MustCompile(`^\[[^\]]*\]$`)

// key folds a word for comparing readings. Diacritics go; letters that change
// meaning (ة and ه, ء seats) stay apart.
func key(w string) string {
	if note.MatchString(w) && w != "[هامش]" {
		return "[؟]"
	}
	w = foldWord.Replace(w)
	var b strings.Builder
	for _, r := range w {
		if r >= 0x64B && r <= 0x652 {
			continue
		}
		if strings.ContainsRune(".,;:!?()«»\"'،؛", r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lcs returns, for each word of a, the index in b it is matched to (-1 if none).
func lcs(a, b []string) []int {
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	match := make([]int, n)
	for i := range match {
		match[i] = -1
	}
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case a[i] == b[j]:
			match[i] = j
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return match
}

func keys(ws []word) []string {
	k := make([]string, len(ws))
	for i, w := range ws {
		k[i] = key(w.s)
		if k[i] == "" {
			k[i] = w.s
		}
	}
	return k
}

// voter is another reading aligned to A: what it wrote between two of A's words.
type voter struct {
	k     []string
	match []int // A index -> voter index
}

// between is the voter's words strictly between its matches for A[lo-1] and
// A[hi] (the agreed words around a disputed span), or ok=false when it has
// no match for one of them.
func (v voter) between(lo, hi int) (string, bool) {
	start := -1
	if lo > 0 {
		if v.match[lo-1] < 0 {
			return "", false
		}
		start = v.match[lo-1]
	}
	end := len(v.k)
	if hi < len(v.match) {
		if v.match[hi] < 0 {
			return "", false
		}
		end = v.match[hi]
	}
	if end < start+1 {
		return "", false
	}
	return strings.Join(v.k[start+1:end], " "), true
}

// Merge3 merges readings a and b, letting the others settle what they dispute.
func Merge3(a, b string, others ...string) (string, Vote) {
	wa, wb := words(a), words(b)
	ka, kb := keys(wa), keys(wb)
	mab := lcs(ka, kb)
	var vs []voter
	for _, o := range others {
		ko := keys(words(o))
		vs = append(vs, voter{k: ko, match: lcs(ka, ko)})
	}
	var v Vote
	v.Words = len(wa)
	var out strings.Builder
	emit := func(s string, nl bool) {
		out.WriteString(s)
		if nl {
			out.WriteString("\n")
		} else {
			out.WriteString(" ")
		}
	}
	i, j := 0, 0
	for i < len(wa) || j < len(wb) {
		if i < len(wa) && mab[i] == j {
			emit(wa[i].s, wa[i].nl)
			v.Agreed++
			i++
			j++
			continue
		}
		// A disputed span: A's words up to its next agreed word, B's words up to that word's match.
		i0 := i
		for i < len(wa) && mab[i] < 0 {
			i++
		}
		jEnd := len(wb)
		if i < len(wa) {
			jEnd = mab[i]
		}
		spanA, spanB := wa[i0:i], wb[j:jEnd]
		j = jEnd
		kA, kB := strings.Join(ka[i0:i], " "), strings.Join(kb[j-len(spanB):j], " ")
		forA, forB := 0, 0
		for _, vt := range vs {
			got, ok := vt.between(i0, i)
			switch {
			case !ok:
			case got == kA:
				forA++
			case got == kB:
				forB++
			}
		}
		nl := len(spanA) > 0 && spanA[len(spanA)-1].nl
		switch {
		case forA > forB:
			v.Settled++
			if len(spanA) > 0 {
				emit(join(spanA), nl)
			}
		case forB > forA:
			v.Settled++
			if len(spanB) > 0 {
				emit(join(spanB), nl)
			}
		default:
			v.Disputed++
			emit(markStart+join(spanA)+markSep+join(spanB)+markEnd, nl)
		}
	}
	return strings.TrimSpace(out.String()), v
}

func join(ws []word) string {
	s := make([]string, len(ws))
	for i, w := range ws {
		s[i] = w.s
	}
	return strings.Join(s, " ")
}

// Chatter says a reading holds the model talking rather than transcribing
// ("Pages 4 to 5 are not transcribed yet...", "I can't produce a verbatim
// transcription"), so it must not be used. It looks for what a model says
// about the task, not plain English: judgments quote English letters and
// contracts word for word ("Please therefore contact Athens ... the P.&I.
// renewal"), and the old list (the, you, please, pages) threw those readings
// away, Gemini's and Claude's alike (26 Oct 2026).
//
// Strong phrases are task talk ("transcription", "these pages", "illegible");
// weak ones are also ordinary letter English ("unable to", "sorry"). An
// answer with (almost) no Arabic is talk on one strong phrase, so a whole
// English letter on a page stands; any other answer needs two distinct
// phrases of either kind. Curly apostrophes count as straight ones.
func Chatter(text string) bool {
	t := strings.ToLower(strings.ReplaceAll(text, "\u2019", "'"))
	strong, all := map[string]bool{}, map[string]bool{}
	for _, m := range chatterStrong.FindAllString(t, -1) {
		strong[m], all[m] = true, true
	}
	for _, m := range chatterWeak.FindAllString(t, -1) {
		all[m] = true
	}
	if arabicShare(t) < 0.05 {
		return len(strong) >= 1
	}
	return len(all) >= 2
}

var (
	chatterStrong = regexp.MustCompile(`\b(transcri(?:be|bed|bing|ption)s?|i can(?:not|'t)|i'll|you asked|requested pages?|pages you requested|apologi[sz]e|these pages|clearer (?:scan|image|copy)|illegible|blurry|blank page|no readable text|(?:not able|unable) to read|unable to assist|page is empty|do not have access|verbatim)\b`)
	chatterWeak   = regexp.MustCompile(`\b(unable to|not able to|unfortunately|sorry|could you|this page|images?|copyright\w*)\b`)
)

// arabicShare is the share of a text's letters that are Arabic.
func arabicShare(s string) float64 {
	ar, all := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			all++
			if unicode.Is(unicode.Arabic, r) {
				ar++
			}
		}
	}
	if all == 0 {
		return 1 // nothing to judge: digits and marks only
	}
	return float64(ar) / float64(all)
}
