package amend

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What an amending provision does to its target.
const (
	Replace = "replace"
	Repeal  = "repeal"
	Insert  = "insert"
)

// Op is one change an amending article makes.
type Op struct {
	Action   string
	Articles []string // the articles changed, numbered as the database numbers them
	Part     string   // the part changed ("الفقره الثانيه", "+" for an addition to the article); "" for all of it
	Whole    bool     // the change is to the named text as a whole (a repeal of the law)
	Ref      Ref      // the text changed, as named; zero when the article doesn't say
	Wording  []string // the new wording as published: one per article, or a single one
	Old      string   // the wording replaced, when the article quotes it ("بدلا من")
	Flags    []string // retroactive, dated (from another date), edits (a list of small edits)
	Evidence string   // the article's opening words, folded, for review
}

var (
	lead = regexp.MustCompile(`^(?:\(?\d{1,2} ?[-.)\]] ?|\(?(?:اولا|ثانيا|ثالثا|رابعا|خامسا|سادسا|سابعا|ثامنا|تاسعا|عاشرا|[ابجد])\)? ?[-:.)] ?)`)
	// Present-tense operative verbs ("تعدل", "يلغى"), and the older "عدلت".
	// "ألغيت"/"أضيفت" open the Centre's notes inside a repealed article
	// ("ألغيت بموجب …"), not amendments, so they are left out.
	verb = regexp.MustCompile(`^و?(تعدل|يعدل|عدلت|تستبدل|يستبدل|تلغي|يلغي|تضاف|يضاف)(?: |$)`)
	// Words between the verb and its object that change when, not what.
	filler  = regexp.MustCompile(`^(?:(بمفعول رجعي|مع المفعول الرجعي|بالمفعول الرجعي|بصوره رجعيه)|((?:اعتبارا|ابتداء) من (?:تاريخ (?:العمل|نشر|صدور|نفاذ)[^,:]{0,40}?(?:القانون|المرسوم|القرار|نشره|صدوره|الرسميه)|\d{1,2} ?[/-] ?\d{1,2} ?[/-] ?\d{2,4}|(?:اول|مطلع|بدايه) [^ ]+(?: [^ ]+)? \d{4}|(?:ال)?سنه [^ ]+|\d{4}))|بصوره استثنائيه|استثنائيا|موقتا|مؤقتا|نهائيا|وتعتبر كانها لم تكن|وتعتبر كان لم تكن)(?: |,|$)`)
	object  = regexp.MustCompile(`^(?:نص |احكام |مضمون |مطلع |صيغه )?`)
	part    = regexp.MustCompile(`^(?:ال)?(?:فقره|فقرتان|فقرتين|فقرات|بند|بندان|بندين|بنود|مقطع|مقطعان|مقطعين|جمله|عباره|عبارات|كلمه|كلمات|حرف|نبذه|سطر|حاشيه)(?: |$)`)
	artWord = regexp.MustCompile(`^(?:ال)?(?:ماده|مواد|مادتان|مادتين)(?: رقم)? `)
	newArt  = regexp.MustCompile(`^(?:ال)?(?:ماده|مواد|مادتان|مادتين) (?:جديده|جديدتان|جديدتين|اضافيه|جديد)(?: (?:برقم|رقمها|تحمل الرقم|رقم|تحت الرقم|تحت رقم) )?`)
	ofArt   = regexp.MustCompile(` (?:من|في) (?:ال)?(?:ماده|مادتين|مواد)(?: رقم)? `)
	toNew   = regexp.MustCompile(`^,? ?(?:نص |احكام )?(?:ال)?(?:ماده|مواد|مادتان|مادتين|المادتين)(?: (?:التاليه|الاتيه|الجديده|جديده|التاليتان|التاليتين|الاتيتان|الاتيتين))?(?: (?:برقم|رقمها|تحمل الرقم|رقم) )?`)
	skipMod = regexp.MustCompile(`^ (?:المعدله|المعدل|المضافه|المضاف|الوارده|الوارد|المذكوره|المنصوص عليها)[^:«"]{0,90}? (?:من|في) `)
	// Where the new wording is introduced.
	marker     = regexp.MustCompile(`(?:وفقا لما هو مبين ادناه|كما هو مبين ادناه|وفقا لما يلي|وفقا لما ياتي|وفق ما يلي|وفق ما ياتي|لتصبح|ليصبح|بحيث تصبح|بحيث يصبح|بحيث تصبحان|وتصبح|ويصبح|فتصبح|تصبح|يصبح|ويستعاض عنها|ويستعاض عنه|ويستعاض عنهما|ويستعاض عنهم|ويستعاض|ويستبدل بها|ويستبدل بهما|ويستبدل|وتستبدل|ويحل محلها|ويحل محله|كما يلي|كما ياتي|كالاتي|كالتالي|علي الشكل|علي الوجه|علي الصوره|علي النحو|بالنص|بالنصوص|بالنصين|بما يلي|بما ياتي|النص التالي|النص الاتي|ما يلي|ما ياتي|الاحكام التاليه|الاحكام الاتيه|الفقره التاليه|الفقره الاتيه|البند التالي|البند الاتي)`)
	substitute = regexp.MustCompile(`^(?:ويستعاض|ويستبدل|وتستبدل|ويحل محل|بالنص|بالنصوص|بالنصين)`)
	instead    = regexp.MustCompile(`(?:[ .,»"]|^)(?:بدلا من|بدلا عن|عوضا عن|عوضا من)(?: النص| العباره| عباره)?(?: السابق| الحالي)? ?:? ?(?:[ :«"]|$)`)
	heading    = regexp.MustCompile(`(?:^|[ .:«"])(?:ال)?ماده (\d{1,4}(?: مكرر(?: \d{1,2})?)?|[^ :]{3,12}(?: [^ :]{3,12}){0,3})(?: [(]?(?:الجديده|جديده|المعدله|المضافه)[()]?)?(?: من [^:«"]{0,90}?)? ?[:-](?: ?(?:ال)?جديده(?: من [^:«"]{0,90}?)? ?:)?`)
	// A wording that is itself a list of edits ("يقرا في الفقره … ما يلي",
	// "تشطب عباره …") changes part of the article, not all of it.
	editing = regexp.MustCompile(`^(?:\d{1,2} ?[-.)] ?)?(?:يقرا|تقرا|يستبدل|تستبدل|تضاف|يضاف|تلغي|يلغي|تشطب|يشطب|تحذف|يحذف|تعدل|يعدل)(?: |$)`)
	limited = regexp.MustCompile(` (?:فيما يتعلق|فيما خص|فيما يختص|في ما يتعلق|لجهه|بالنسبه ل|في الشق|في الجزء|باستثناء|ما عدا)`)
	// The Centre's stand-in where it didn't type the wording out.
	placeholder = regexp.MustCompile(`^.{0,20}(?:للاطلاع علي|يرجي مراجعه|راجع) (?:التعديلات|الملحق|ملحق)`)
	// A part named after the article's number: "الماده /9/ البند السادس فقره
	// (4) (ب) من القانون …", "الماده الرابعه - الفقره 6 من المرسوم …".
	partAfter = regexp.MustCompile(`^ ?[/,-]? ?-? ?((?:ال)?(?:فقره|فقرتان|فقرتين|فقرات|بند|بندان|بندين|بنود|مقطع|جمله|نبذه) [^:«"]{0,80}?) (?:من|في) `)
	// Between the text named and its new wording, words that say only a part
	// changes. An addition: "باضافه فقره الي اخرها", "ليضاف اليها البند
	// التالي", "حيث تضاف الفقره الاتيه", "لجهه اضافه تعريف …".
	adding = regexp.MustCompile(`(?:^|[ ,])(?:و?ذلك )?(?:باضافه|لجهه اضافه|(?:بحيث |حيث |ل|و)?(?:يضاف|تضاف)) `)
	// A part that takes the new wording: "بحيث تصبح الفقره الاخيره كالتالي".
	becomes = regexp.MustCompile(`(?:تصبح|يصبح) ((?:ال)?(?:فقره|بند|مقطع|جمله|نبذه) [^:«"]{2,60}?) (?:كالتالي|كالاتي|كما يلي|كما ياتي|علي الشكل|علي الوجه|علي النحو|وفقا|وفق)`)
	// "ثانيا: بدون تعديل": the wording leaves out the items that stay as
	// they were, so it is not the article's text. ("والباقي دون تعديل" after
	// a wording is no such sign: the rest may be the text's other articles.)
	restKept = regexp.MustCompile(`: ?(?:دون|بدون|من دون|بلا) (?:اي )?(?:تعديل|تغيير)`)
	// "الفقره التاليه" introduces a wording; it names no part.
	following   = regexp.MustCompile(`^(?:ال)?(?:فقره|بند|مقطع|جمله|نبذه) (?:التاليه|الاتيه|التالي|الاتي)$`)
	clauseStart = regexp.MustCompile(`^(?:\(?\d{1,2} ?[-.)] ?|\(?(?:اولا|ثانيا|ثالثا|رابعا|خامسا|سادسا|سابعا|ثامنا|تاسعا|عاشرا)\)? ?[-:.)] ?)?و?(?:تعدل|يعدل|تستبدل|يستبدل|تلغي|يلغي|تضاف|يضاف)[^:«]{0,80}?(?:الماده|المواد|المادتان|المادتين|الفقره|البند|القانون|المرسوم|القرار)`)
)

// Parse reads the changes an article makes to other texts. Paragraphs that
// open with their own operative verb ("1- تعدل …", "2- تلغى …") are read as
// separate changes, unless they sit inside a quoted wording.
func Parse(article string) []Op {
	var ops []Op
	for _, c := range clauses(article) {
		if op, ok := parseClause(Fold(c)); ok {
			ops = append(ops, op)
		}
	}
	return ops
}

// item is the number a line opens with as an item of a list ("2- …",
// "7 ـ أ ـ", "(8) …"), -1 when it opens with none.
var item = regexp.MustCompile(`^\(?([0-9٠-٩]{1,2}) ?[-–ـ.)]`)

func itemNumber(line string) int {
	m := item.FindStringSubmatch(line)
	if m == nil {
		return -1
	}
	n := 0
	for _, r := range m[1] {
		if r >= '٠' && r <= '٩' {
			r = '0' + (r - '٠')
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// clauses cuts an article into its changes. A clause also ends where the
// list it is an item of goes on: in "1- تعدل المادة 38 … : \"…\n2- يعمل
// بهذا القانون فور نشره", item 2 is the amending law's own, not part of
// article 38's new wording (an unclosed quote hides that). Once the wording
// opens a list of its own (a line "1- …"), or starts right away with the
// next number (the new wording of a paragraph 2, numbered as it is), its
// numbers are the wording's, and the clause runs on as before.
func clauses(s string) []string {
	var out []string
	start, depth := 0, 0
	num, inner := -1, false // the clause's own item number; the wording has a list of its own
	body := 0               // lines of the clause after its opening one
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], '\n')
		if j < 0 {
			j = len(s)
		} else {
			j += i
		}
		line := strings.TrimSpace(s[i:j])
		n := itemNumber(line)
		if line != "" && i > start && depth <= 0 && strings.TrimSpace(s[start:i]) != "" {
			opens := clauseStart.MatchString(FoldString(line))
			if opens || (num > 0 && !inner && body > 0 && n == num+1) {
				out = append(out, s[start:i])
				start, depth, num, inner, body = i, 0, n, false, 0
			} else {
				if num > 0 && (n == 1 || body == 0 && n == num+1) {
					inner = true
				}
				body++
			}
		}
		if num < 0 && n > 0 && clauseStart.MatchString(FoldString(line)) {
			num = n
		}
		depth += strings.Count(line, "«") - strings.Count(line, "»")
		i = j + 1
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, s[start:])
	}
	return out
}

func parseClause(t Text) (Op, bool) {
	x := t.N
	pos := 0
	if m := lead.FindStringIndex(x); m != nil {
		pos = m[1]
	}
	m := verb.FindStringSubmatchIndex(x[pos:])
	if m == nil {
		return Op{}, false
	}
	v := x[pos+m[2] : pos+m[3]]
	op := Op{Evidence: cut(x, 220)}
	switch v {
	case "تعدل", "يعدل", "عدلت", "تستبدل", "يستبدل":
		op.Action = Replace
	case "تلغي", "يلغي":
		op.Action = Repeal
	default:
		op.Action = Insert
	}
	pos += m[1]
	for {
		f := filler.FindStringSubmatchIndex(x[pos:])
		if f == nil || f[1] == 0 {
			break
		}
		if f[2] >= 0 {
			op.Flags = append(op.Flags, "retroactive")
		}
		if f[4] >= 0 {
			op.Flags = append(op.Flags, "dated")
		}
		pos += f[1]
	}
	if strings.HasPrefix(x[pos:], "في ") && op.Action == Replace {
		op.Flags = append(op.Flags, "edits") // "يعدل في الماده 18 … ما ياتي: 1- … 2- …"
		pos += len("في ")
	}
	obj := object.FindString(x[pos:])
	pos += len(obj)

	var ok bool
	if op.Action == Insert {
		pos, ok = op.insertObject(x, pos)
	} else {
		pos, ok = op.object(x, pos)
	}
	if !ok || op.Ref.Self {
		return Op{}, false
	}
	if obj == "مطلع " && op.Part == "" && !op.Whole {
		op.Part = "مطلع" // the article's opening words, not all of it
	}
	op.wording(t, pos)
	return op, true
}

// object reads what a replace or repeal changes, and the text it is in.
func (op *Op) object(x string, pos int) (int, bool) {
	rest := x[pos:]
	switch {
	case part.MatchString(rest):
		m := ofArt.FindStringIndex(rest)
		limit := len(rest)
		if i := strings.IndexAny(rest, ":«"); i >= 0 {
			limit = i
		}
		if m != nil && m[0] < 220 && m[0] < limit {
			op.Part = strings.TrimSpace(rest[:m[0]])
			nums, n := numbers(rest[m[1]:])
			if n == 0 {
				return 0, false
			}
			op.Articles = nums
			pos += m[1] + n
		} else {
			// A part of the text itself: a table row, an annex line.
			end := strings.Index(rest, " من ")
			if end < 0 || end > 220 {
				return 0, false
			}
			op.Part = strings.TrimSpace(rest[:end])
			pos += end
		}
	case artWord.MatchString(rest):
		m := artWord.FindStringIndex(rest)
		nums, n := numbers(rest[m[1]:])
		if n == 0 {
			return 0, false
		}
		op.Articles = nums
		pos += m[1] + n
		if p := partAfter.FindStringSubmatchIndex(x[pos:]); p != nil && len(nums) == 1 {
			op.Part = strings.TrimSpace(x[pos+p[2] : pos+p[3]])
			pos += p[3]
		}
	default:
		r, n := ref(rest)
		if n == 0 || (r.Num == 0 && r.Date.IsZero() && r.Name == "") {
			return 0, false // "تلغى جميع النصوص المخالفه …": names nothing
		}
		op.Whole, op.Ref = true, r
		// "… فيما يتعلق ب…", "… لجهه …": only as far as something goes.
		if m := limited.FindStringIndex(x[pos+n:]); m != nil && m[0] < 120 {
			op.Part = strings.TrimSpace(cut(x[pos+n+m[0]:], 160))
		}
		return pos + n, true
	}
	return op.target(x, pos), true
}

// insertObject reads what an addition adds, and where.
func (op *Op) insertObject(x string, pos int) (int, bool) {
	rest := x[pos:]
	if m := newArt.FindStringIndex(rest); m != nil {
		// "تضاف ماده جديده برقم 73 مكرر الي القانون …"
		if nums, n := numbers(rest[m[1]:]); n > 0 {
			op.Articles = nums
			m[1] += n
		}
		pos += m[1]
		return op.target(x, pos), true
	}
	if !strings.HasPrefix(rest, "الي ") {
		return 0, false
	}
	pos += len("الي ")
	rest = x[pos:]
	if m := artWord.FindStringIndex(rest); m != nil {
		// "تضاف الي الماده 49 من القانون … الفقره التاليه": added to an article.
		nums, n := numbers(rest[m[1]:])
		if n == 0 {
			return 0, false
		}
		op.Articles, op.Part = nums, "+"
		return op.target(x, pos+m[1]+n), true
	}
	// "يضاف الي قانون العمل … نص الماده التاليه: «الماده 12/10: …»"
	r, n := ref(rest)
	if n == 0 || r.Self {
		return 0, false
	}
	op.Ref = r
	pos += n
	if m := toNew.FindStringIndex(x[pos:]); m != nil && m[1] > 2 {
		pos += m[1]
		if nums, k := numbers(x[pos:]); k > 0 {
			op.Articles = nums
			pos += k
		}
		return pos, true
	}
	return 0, false // added to a table or a list in the text: not an article
}

// target reads " من <ref>" after the object, skipping "المعدله بموجب …".
func (op *Op) target(x string, pos int) int {
	if m := skipMod.FindStringIndex(x[pos:]); m != nil {
		pos += m[1] - len(" من ") // land on " من "
		if !strings.HasPrefix(x[pos:], " من ") && !strings.HasPrefix(x[pos:], " في ") {
			pos += 1
		}
	}
	for _, w := range []string{" من ", " في ", " الي "} {
		if strings.HasPrefix(x[pos:], w) {
			if r, n := ref(x[pos+len(w):]); n > 0 {
				op.Ref = r
				return pos + len(w) + n
			}
		}
	}
	return pos
}

// wording finds the new wording after pos, as published, and the old one
// when the article quotes it.
func (op *Op) wording(t Text, pos int) {
	x := t.N
	m := marker.FindStringIndex(x[pos:])
	if r := strings.TrimLeft(x[pos:], " "); strings.HasPrefix(r, ":") {
		// The object already said it: "… نص الماده التاليه: «…»".
		c := len(x) - len(r)
		m = []int{c - pos, c - pos}
	}
	if m == nil || m[0] > 260 {
		return
	}
	at := pos + m[1]
	// A marker ends a word: "ويستعاض عنه" also matches "ويستعاض عنهما".
	for at < len(x) {
		r, n := utf8.DecodeRuneInString(x[at:])
		if !unicode.IsLetter(r) {
			break
		}
		at += n
	}
	if op.Action == Repeal {
		if !substitute.MatchString(x[pos+m[0]:]) {
			return // "تلغي الماده 5 ويستعاض …" only; otherwise a plain repeal
		}
		op.Action = Replace
	}
	if i := strings.IndexAny(x[at:], ":"); i >= 0 && i < 90 && !strings.ContainsAny(x[at:at+i], "«\"") {
		at += i + 1
	} else {
		// "… بالنص الاتي «…»": no colon.
		for _, w := range []string{" الاتي", " التالي", " الاتيه", " التاليه", " الاتيين", " التاليين"} {
			if strings.HasPrefix(x[at:], w) {
				at += len(w)
				break
			}
		}
	}
	if op.Part == "" && op.Action == Replace && len(op.Articles) == 1 {
		// What the words before the wording say changes.
		between := x[pos:at]
		if b := becomes.FindStringSubmatch(between); b != nil && !following.MatchString(b[1]) {
			op.Part = strings.TrimSpace(b[1])
		} else if a := adding.FindStringIndex(between); a != nil && !strings.Contains(between[a[1]:], "صبح") {
			// Not "لجهه اضافه فقره 7 اليها بحيث تصبح كالاتي": that wording
			// is the article as it becomes.
			op.Action, op.Part = Insert, "+"
		}
	}
	if op.Part == "" && restKept.MatchString(x[at:]) {
		op.Flags = append(op.Flags, "edits")
	}
	end := len(x)
	if q := instead.FindStringIndex(x[at:]); q != nil && q[0] > 0 {
		op.Old = unquote(t.Span(at+q[1], len(x)))
		end = at + q[0]
	}
	full := unquote(t.Span(at, end))
	if full == "" || placeholder.MatchString(FoldString(full)) {
		return
	}
	// Split by the new articles' headings ("الماده 504 الجديده: …").
	f := Fold(full)
	hs := heading.FindAllStringSubmatchIndex(f.N, -1)
	type piece struct {
		num  string
		from int
		body int
	}
	var pieces []piece
	for _, h := range hs {
		num, n := one(f.N[h[2]:h[3]])
		if n == 0 {
			continue
		}
		pieces = append(pieces, piece{num, h[0], h[1]})
	}
	if op.Action == Insert && len(op.Articles) == 0 && len(pieces) > 0 {
		for _, p := range pieces {
			op.Articles = append(op.Articles, p.num)
		}
	}
	if len(op.Articles) > 1 && op.Part == "" {
		// Each article's wording runs from its heading to the next heading;
		// every article named must have exactly one.
		body := map[string]string{}
		count := map[string]int{}
		for i, p := range pieces {
			stop := len(f.N)
			if i+1 < len(pieces) {
				stop = pieces[i+1].from
			}
			count[p.num]++
			body[p.num] = unquote(f.Span(p.body, stop))
		}
		for _, a := range op.Articles {
			if count[a] != 1 || body[a] == "" {
				op.Flags = append(op.Flags, "split")
				return
			}
		}
		for _, a := range op.Articles {
			op.Wording = append(op.Wording, body[a])
		}
		return
	}
	if len(op.Articles) > 1 && op.Part == "" {
		op.Flags = append(op.Flags, "split")
		return
	}
	if len(pieces) > 0 && pieces[0].from < 3 && op.Part == "" {
		full = unquote(f.Span(pieces[0].body, len(f.N)))
	}
	if editing.MatchString(FoldString(full)) && op.Part == "" {
		op.Flags = append(op.Flags, "edits")
	}
	op.Wording = []string{full}
}

// unquote trims a wording's surrounding quotes: "«…»." becomes "…", and a
// period inside the quotes stays.
func unquote(s string) string {
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "«\"“:،, "))
	for _, q := range []string{"»", "\"", "”"} {
		if t := strings.TrimSuffix(s, q+"."); t != s {
			s = t
			break
		}
		if t := strings.TrimSuffix(s, q); t != s {
			s = t
			break
		}
	}
	s = strings.TrimSpace(s)
	if strings.Count(s, "«") > strings.Count(s, "»") {
		s = strings.Replace(s, "«", "", 1)
	}
	return s
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// HasVerb says whether an article has a clause that opens with an operative
// amending verb (the verbs Parse reads). It is the recall check's count of
// articles that should have produced a change (internal/recall).
func HasVerb(article string) bool {
	for _, c := range clauses(article) {
		x := Fold(c).N
		if m := lead.FindStringIndex(x); m != nil {
			x = x[m[1]:]
		}
		if verb.MatchString(x) {
			return true
		}
	}
	return false
}

// Opening is the first n words of an article, folded, without its list
// number: the key the recall check groups misses by.
func Opening(article string, n int) string {
	x := FoldString(article)
	if m := lead.FindStringIndex(x); m != nil {
		x = x[m[1]:]
	}
	w := strings.Fields(x)
	if len(w) > n {
		w = w[:n]
	}
	return strings.Join(w, " ")
}
