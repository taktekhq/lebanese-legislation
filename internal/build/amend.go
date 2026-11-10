package build

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/modelout"
)

// Amendments and versions. Every article that amends another text is read
// (internal/amend), the text and article it names are found, and each
// change is recorded; then every amended article gets its versions, oldest
// first, each from the date and the text that made it.

type change struct {
	id         int
	byLaw      int
	byArticle  string
	seq        int
	action     string
	law        int // 0: not found
	article    string
	part       string
	text, old  string
	effective  time.Time
	status     string
	reasons    []string
	method     string
	resolvedBy string
	evidence   string
	amending   string // the amending article in full
	inside     bool   // the Centre's text of the article has the wording and more (see inside)
	judgedBy   string // the model whose judgement applied
	judgement  string // includes, predates, later, unclear
}

func (b *builder) amendments() error {
	ix, err := b.lawIndex()
	if err != nil {
		return err
	}
	numbers, err := b.articleNumbers()
	if err != nil {
		return err
	}
	rows, err := b.conn.Query(b.ctx, `SELECT a.law_id, a.number, a.text_ar FROM legislation_article a
		WHERE ar_norm(a.text_ar) ~ '(تعدل|يعدل|عدلت|تستبدل|يستبدل|تلغي|يلغي|تضاف|يضاف)'
		ORDER BY a.law_id, a.position`)
	if err != nil {
		return err
	}
	var changes []*change
	prevLaw, prevTarget := 0, 0
	for rows.Next() {
		var lawID int
		var number, text string
		if err := rows.Scan(&lawID, &number, &text); err != nil {
			return err
		}
		if lawID != prevLaw {
			prevLaw, prevTarget = lawID, 0
		}
		by := ix.Law(lawID)
		eff := by.Gazette
		if eff.IsZero() {
			eff = by.Issued
		}
		for seq, op := range amend.Parse(text) {
			target, how := 0, ""
			switch {
			case op.Ref.Named() && !op.Ref.Same:
				target, how = ix.Resolve(op.Ref, by.Issued)
				if target == 0 && op.Ref.Num == 0 && op.Ref.Name != "" {
					// A name we don't know: the title may say it better.
					if t, h := ix.TitleRef(by.Title, by.Issued); t > 0 {
						target, how = t, h
					}
				}
			case prevTarget > 0:
				target, how = prevTarget, "as the article before"
			default:
				target, how = ix.TitleRef(by.Title, by.Issued)
				if target == 0 {
					how = "names no text"
				}
			}
			if target == lawID {
				continue // a text amending itself (a budget law's own articles)
			}
			if target > 0 {
				prevTarget = target
			}
			arts := op.Articles
			if len(arts) == 0 {
				arts = []string{""}
			}
			for i, art := range arts {
				c := &change{byLaw: lawID, byArticle: number, seq: seq + 1, action: op.Action, law: target,
					article: art, part: op.Part, old: op.Old, effective: eff, method: "pattern", evidence: op.Evidence, amending: text}
				switch {
				case len(op.Wording) == len(op.Articles) && len(op.Articles) > 0:
					c.text = op.Wording[i]
				case len(op.Wording) == 1:
					c.text = op.Wording[0]
				}
				if target > 0 {
					c.resolvedBy = how
				}
				c.reasons = append(c.reasons, op.Flags...)
				c.status = decide(c, op, how, numbers)
				changes = append(changes, c)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	changes, err = b.editorial(changes, ix)
	if err != nil {
		return err
	}
	// One row per change: the same article can be read twice (a law and its
	// copy as a decree-implemented law are both in the corpus).
	seen := map[string]bool{}
	kept := changes[:0]
	for _, c := range changes {
		key := fmt.Sprint(c.byLaw, "|", c.byArticle, "|", c.seq, "|", c.article)
		if !seen[key] {
			seen[key] = true
			kept = append(kept, c)
		}
	}
	changes = kept
	for i, c := range changes {
		c.id = i + 1
	}
	versions, err := b.versions(changes)
	if err != nil {
		return err
	}
	if versions, err = b.corrections(versions); err != nil {
		return err
	}
	for _, c := range changes {
		b.rep.Amendments[c.status]++
	}
	if err := b.saveChanges(changes); err != nil {
		return err
	}
	_, err = b.conn.CopyFrom(b.ctx, pgx.Identifier{"article_version"}, versionCols, pgx.CopyFromRows(versions))
	return err
}

// versionCols are article_version's columns as versions and corrections
// lay out their rows. source_kind and source_id name where the version's
// wording was read (sources.go), NULL when it has none.
var versionCols = []string{"law_id", "article_number", "major", "minor", "valid_from", "valid_to", "state", "text_ar",
	"text_source", "amendment_id", "note", "source_kind", "source_id"}

// decide decides a change's status, and notes why when it isn't applied.
func decide(c *change, op amend.Op, how string, numbers map[int]map[string]bool) string {
	switch {
	case c.law == 0:
		c.reasons = append(c.reasons, how)
		return "review"
	case op.Whole && c.action == amend.Repeal && c.part == "":
		return "applied"
	case op.Whole && c.action == amend.Repeal:
		return "partial" // repealed only as far as the part says
	case op.Whole:
		c.reasons = append(c.reasons, "changes the text as a whole")
		return "review"
	case c.article == "":
		return "partial" // a table, an annex, a list in the text
	case c.action != amend.Insert && !numbers[c.law][c.article]:
		c.reasons = append(c.reasons, "no article "+c.article+" in the text")
		return "review"
	case c.part != "":
		return "partial"
	case len(op.Flags) > 0 && (has(op.Flags, "split") || has(op.Flags, "edits")):
		return "partial"
	case c.action != amend.Repeal && c.text == "":
		c.reasons = append(c.reasons, "new wording not found")
		return "partial"
	case c.action != amend.Repeal && len([]rune(c.text)) < 12:
		c.reasons = append(c.reasons, "new wording too short to trust")
		return "review"
	}
	return "applied"
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (b *builder) lawIndex() (*amend.Index, error) {
	rows, err := b.conn.Query(b.ctx, `SELECT l.id, l.kind, coalesce(l.number, ''), l.issued_on, l.gazette_date, l.title,
		l.status = 'in_force', EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id) FROM legislation l`)
	if err != nil {
		return nil, err
	}
	laws, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (amend.Law, error) {
		var l amend.Law
		var issued, gazette *time.Time
		err := row.Scan(&l.ID, &l.Kind, &l.Number, &issued, &gazette, &l.Title, &l.InForce, &l.Code)
		if issued != nil {
			l.Issued = *issued
		}
		if gazette != nil {
			l.Gazette = *gazette
		}
		return l, err
	})
	if err != nil {
		return nil, err
	}
	return amend.NewIndex(laws), nil
}

func (b *builder) articleNumbers() (map[int]map[string]bool, error) {
	rows, err := b.conn.Query(b.ctx, `SELECT law_id, number FROM legislation_article`)
	if err != nil {
		return nil, err
	}
	out := map[int]map[string]bool{}
	for rows.Next() {
		var id int
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]bool{}
		}
		out[id][n] = true
	}
	return out, rows.Err()
}

// editorial applies db/amendments.tsv: a person's confirmations,
// rejections, repeals and additions.
func (b *builder) editorial(changes []*change, ix *amend.Index) ([]*change, error) {
	ds, err := amend.ParseDecisions(db.Amendments)
	if err != nil {
		return nil, err
	}
	amending := func(byLaw int, byArt string) (string, bool) {
		var text string
		err := b.conn.QueryRow(b.ctx, `SELECT text_ar FROM legislation_article WHERE law_id = $1 AND number = $2`, byLaw, byArt).Scan(&text)
		return text, err == nil
	}
	return applyDecisions(changes, ds, ix, amending, b.rep.note), nil
}

// applyDecisions applies a person's decisions to the pattern pass's changes.
// A repeal also takes a change whose text the pattern pass couldn't name
// (Law 68/2026, article 4: "من قانون العقوبات والمواد" read as one name). A
// decision on a change the pattern pass never read adds it: an apply with
// the wording between the article's « », a repeal with no wording. A reject
// that matches nothing here may be the model's link (modelout.Graph drops it).
func applyDecisions(changes []*change, ds []amend.Decision, ix *amend.Index,
	amending func(byLaw int, byArt string) (string, bool), note func(string, ...any)) []*change {
	for _, d := range ds {
		found := false
		for _, c := range changes {
			if c.byLaw != d.ByLaw || c.byArticle != d.ByArticle || c.article != d.Article {
				continue
			}
			if c.law != d.Law && (c.law != 0 || d.Decision != amend.DecideRepeal) {
				continue
			}
			found = true
			switch d.Decision {
			case amend.DecideReject:
				c.status = "rejected"
			case amend.DecideApply:
				// A person read the wording as the whole article: a part the
				// pattern pass named, or an addition it read, was a misreading.
				if c.action == amend.Insert && c.part == "+" {
					c.action = amend.Replace
				}
				c.status, c.method, c.part = "applied", "editorial", ""
			case amend.DecideRepeal:
				if c.law == 0 {
					c.law, c.resolvedBy, c.reasons = d.Law, "by hand", nil
				}
				c.action, c.part, c.text, c.status, c.method = amend.Repeal, "", "", "applied", "editorial"
			}
		}
		if found || d.Decision == amend.DecideReject {
			continue
		}
		text, ok := amending(d.ByLaw, d.ByArticle)
		if !ok {
			note("amendments.tsv: %s: amending article not found", d.Line)
			continue
		}
		eff := time.Time{}
		if by := ix.Law(d.ByLaw); by != nil {
			eff = by.Gazette
			if eff.IsZero() {
				eff = by.Issued
			}
		}
		c := &change{byLaw: d.ByLaw, byArticle: d.ByArticle, seq: 99, action: amend.Replace, law: d.Law,
			article: d.Article, effective: eff, status: "applied", method: "editorial",
			resolvedBy: "by hand", evidence: amend.FoldString(text), amending: text}
		if d.Decision == amend.DecideRepeal {
			c.action = amend.Repeal
		} else {
			// Not read by the pattern: take the wording between the article's « ».
			c.text = quoted(text)
		}
		changes = append(changes, c)
	}
	return changes
}

// quoted is the wording between an article's first « and its last ».
func quoted(s string) string {
	i, j := strings.Index(s, "«"), strings.LastIndex(s, "»")
	if i < 0 || j <= i {
		return ""
	}
	return strings.TrimSpace(s[i+len("«") : j])
}

func (b *builder) saveChanges(changes []*change) error {
	var rows [][]any
	for _, c := range changes {
		rows = append(rows, []any{c.id, c.byLaw, c.byArticle, c.seq, c.action, nz(c.law), ns(c.article), ns(c.part),
			ns(c.text), ns(c.old), nt(c.effective), c.status, ns(strings.Join(c.reasons, "; ")), c.method, ns(c.resolvedBy),
			ns(c.judgedBy), ns(c.judgement), ns(c.evidence)})
	}
	_, err := b.conn.CopyFrom(b.ctx, pgx.Identifier{"amendment"},
		[]string{"id", "by_law_id", "by_article", "seq", "action", "law_id", "article_number", "part", "text_ar",
			"old_text_ar", "effective_on", "status", "reason", "method", "resolved_by", "judged_by", "judgement", "evidence"}, pgx.CopyFromRows(rows))
	return err
}

func nz(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func ns(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nt(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// versions builds each amended article's versions. The Centre's text is one
// of them: the one after the latest change it already shows, or the first
// when it shows none (as with Article 73 of the Code of Criminal Procedure
// before Law 321/2023).
func (b *builder) versions(changes []*change) ([][]any, error) {
	type key struct {
		law     int
		article string
	}
	groups := map[key][]*change{}
	for _, c := range changes {
		if (c.status == "applied" || c.status == "partial") && c.law > 0 && c.article != "" {
			k := key{c.law, c.article}
			groups[k] = append(groups[k], c)
		}
	}
	var lawIDs []int
	for k := range groups {
		lawIDs = append(lawIDs, k.law)
	}
	centre := map[key]string{}
	centreSrc := map[key]textSource{} // where the Centre's text was read, as its article row says
	rows, err := b.conn.Query(b.ctx, `SELECT a.law_id, a.number, a.text_ar, coalesce(a.source_kind, ''), coalesce(a.source_id, '')
		FROM legislation_article a WHERE a.law_id = ANY($1)`, lawIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		var t string
		var src textSource
		if err := rows.Scan(&k.law, &k.article, &t, &src.kind, &src.id); err != nil {
			return nil, err
		}
		if _, ok := groups[k]; ok {
			centre[k], centreSrc[k] = t, src
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seen, err := b.seen()
	if err != nil {
		return nil, err
	}
	judged, err := modelout.LoadJudgements()
	if err != nil {
		return nil, err
	}
	var out [][]any
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].law != keys[j].law {
			return keys[i].law < keys[j].law
		}
		return keys[i].article < keys[j].article
	})
	for _, k := range keys {
		u := centre[k]
		cs, at, unknown := place(u, groups[k], k.law, seen, judged)
		if len(cs) == 0 {
			continue
		}
		vs := buildVersions(u, centreSrc[k], cs, at, unknown)
		for i, v := range vs {
			// A major version lasts until the next major one begins.
			var to any
			for _, w := range vs[i+1:] {
				if w.major > v.major {
					to = nt(w.from)
					break
				}
			}
			var srcKind, srcID any
			if v.text != "" {
				srcKind, srcID = ns(v.src.kind), ns(v.src.id)
			}
			out = append(out, []any{k.law, k.article, v.major, v.minor, nt(v.from), to, v.state, ns(v.text), v.source,
				nz(v.amendment), ns(v.note), srcKind, srcID})
		}
		b.rep.Versions += len(vs)
		if last := vs[len(vs)-1]; at < len(cs)-1 && (last.text != "" || last.state == "repealed") {
			b.rep.NewerThanCentre++
		}
	}
	return out, nil
}

// place puts the Centre's text u of an article among the changes cs made
// to it. It returns the changes that stand, oldest first, the index of the
// one after which the Centre's text stands (-1: before them all), and
// whether a later change we haven't identified made the Centre's text. On
// the way each change gets its judgement and its final status: a wording
// that is only a part of the article is marked (inside) or becomes partial.
func place(u string, cs []*change, law int, seen seenTimes, judged modelout.Judgements) ([]*change, int, bool) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if !a.effective.Equal(b.effective) {
			return a.effective.Before(b.effective)
		}
		if a.byLaw != b.byLaw {
			return a.byLaw < b.byLaw
		}
		return a.seq < b.seq
	})
	// A model's checked answer, where the patterns couldn't tell (see
	// judge): only for the same inputs it was shown. It says
	// which version stands, never what one says.
	for _, c := range cs {
		if shows(u, c) {
			continue // the Centre's text already shows the change
		}
		if v, ok := judged.Verdict(c.byLaw, c.byArticle, c.seq, c.article, c.amending, u, c.part); ok {
			c.judgedBy, c.judgement = string(v.Model), string(v.State)
		}
	}
	// A new wording that is only a part of the article never stands as
	// the article: the Centre's text does, and the change is a note.
	for _, c := range cs {
		if c.status != "applied" || c.part != "" || c.text == "" || c.action == amend.Repeal || c.method == "editorial" {
			continue
		}
		switch {
		case inside(u, c.text):
			c.inside = true
			c.reasons = append(c.reasons, "the Centre's text has this wording and more")
		case restated(u, c):
			// Short, and still the whole article: the amending article
			// quotes all of the old one after "بدلا من".
		case short(u, c.text):
			c.status = "partial"
			c.reasons = append(c.reasons, "new wording under half the article's length: a part of it, or cut short")
		}
	}
	shown := -1 // the latest change the Centre's text already shows
	later := -1 // the latest change the Centre's text is later than
	for i, c := range cs {
		if shows(u, c) || c.judgement == "includes" {
			shown = i
		}
		if c.judgement == "later" {
			later = i
		}
	}
	// The Centre's text is the state after change "at"; with unknown, it
	// comes from a later change we haven't identified.
	at, unknown := shown, false
	if later > shown {
		at, unknown = later, true
	}
	// Changes after it are newer than the Centre's text, when there is
	// a reason to think so: it shows an earlier change; a person or the
	// model said so; the change quotes the wording it replaced and that
	// is the Centre's; or the amending text appeared after the Centre's
	// text last changed. Otherwise the Centre's text may as well be the
	// newer one (amended again by a text we haven't linked): it waits
	// for review, and meanwhile the article shows the change as a note.
	if at < len(cs)-1 {
		kept := cs[:at+1]
		for _, c := range cs[at+1:] {
			newer := at >= 0 && c.effective.After(cs[at].effective) || c.method == "editorial" || c.judgement == "predates" ||
				c.status == "partial" && c.judgement != "unclear" || quotesOld(u, c) || seen.after(c.byLaw, law)
			if newer {
				c.reasons = append(c.reasons, "not in the Centre's text")
				if c.status == "review" {
					c.status = "applied"
				}
				kept = append(kept, c)
			} else {
				c.status = "review"
				c.reasons = append(c.reasons, "the Centre's text matches no version: is it older or newer than this change?")
			}
		}
		cs = kept
	}
	return cs, at, unknown
}

// quotesOld says whether c quotes the wording it replaced, and the Centre's
// text u is that wording: then u is older than c.
func quotesOld(u string, c *change) bool {
	if c.old == "" || u == "" {
		return false
	}
	co := core(c.old)
	return len([]rune(co)) >= 12 && (strings.Contains(core(u), co) || coverage(words(c.old), words(u)) >= 0.9)
}

// restated says whether c quotes, as the wording it replaced, most of the
// Centre's text u: then c's new wording replaces the whole article, however
// short it is (Decree 3125/2018: "تمتد فترة عمل الدعم التقني … لغاية
// 31/1/2021" for an article of four lines).
func restated(u string, c *change) bool {
	return quotesOld(u, c) && !short(u, c.old)
}

// The initial crawl saw every text by this day; a text first seen after it
// is new, and its changes can be dated against the Centre's.
var backfillDone = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

type seenTimes struct {
	law  map[int]time.Time // when the crawl first saw each text
	text map[int]time.Time // when the Centre's full text of each last changed
}

// after says whether the amending text appeared after the backfill and
// after the Centre last changed the amended text: the Centre hasn't caught
// up with it.
func (s seenTimes) after(byLaw, law int) bool {
	first, ok := s.law[byLaw]
	if !ok || !first.After(backfillDone) {
		return false
	}
	return first.After(s.text[law])
}

func (b *builder) seen() (seenTimes, error) {
	s := seenTimes{law: map[int]time.Time{}, text: map[int]time.Time{}}
	rows, err := b.conn.Query(b.ctx, `SELECT doc_type, centre_id, first_seen, last_changed FROM source_page
		WHERE doc_type IN ('law', 'lawtext')`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var kind string
		var id int
		var first, changed time.Time
		if err := rows.Scan(&kind, &id, &first, &changed); err != nil {
			return s, err
		}
		if kind == "law" {
			s.law[id] = first
		} else {
			s.text[id] = changed
		}
	}
	return s, rows.Err()
}

type version struct {
	major, minor int
	from         time.Time
	state        string
	text         string
	source       string     // text_source: centre, amendment, editorial, none
	src          textSource // where text was read (sources.go)
	amendment    int
	note         string
}

// textSource is a wording's source_kind and source_id (sources.go).
type textSource struct{ kind, id string }

// published is where change c's wording, or the wording it quotes as
// replaced, was read: the amending article.
func (c *change) published() textSource {
	return textSource{SourceAmendment, AmendmentSource(c.byLaw, c.byArticle)}
}

// buildVersions lays out an article's versions: 1.0 (unless an amendment
// created the article), then a major version per change. u
// is the Centre's text and usrc where it was read; at the index of the
// change after which the Centre's text stands (-1: before them all);
// unknown says a later change we haven't identified made it.
func buildVersions(u string, usrc textSource, cs []*change, at int, unknown bool) []version {
	var vs []version
	major := 0
	if !(cs[0].action == amend.Insert && cs[0].part == "") {
		major = 1
		v := version{major: 1, state: "in_force", source: "none"}
		if at < 0 && u != "" {
			v.text, v.source, v.src = u, "centre", usrc
		}
		vs = append(vs, v)
	}
	for i, c := range cs {
		// A change that quotes the wording it replaced fills a gap before it.
		if prev := len(vs) - 1; c.old != "" && c.part == "" && c.status == "applied" && !c.inside && prev >= 0 && vs[prev].text == "" && vs[prev].state == "in_force" {
			vs[prev].text, vs[prev].source, vs[prev].src = c.old, "amendment", c.published()
		}
		major++
		v := version{major: major, from: c.effective, state: "in_force", source: "none", amendment: c.id}
		whole := c.part == "" && c.text != "" && c.status == "applied" && !c.inside
		switch {
		case c.action == amend.Repeal && c.part == "":
			v.state = "repealed"
		case whole && (c.action == amend.Replace || c.action == amend.Insert):
			v.text, v.source, v.src = c.text, "amendment", c.published()
		case i == at && !unknown && u != "":
			v.text, v.source, v.src = u, "centre", usrc
		}
		vs = append(vs, v)
		if i == at && unknown && u != "" {
			major++
			vs = append(vs, version{major: major, state: "in_force", text: u, source: "centre", src: usrc,
				note: "a later change whose amending text we haven't identified"})
		}
	}
	return vs
}

// shows says whether the Centre's text u already carries change c: all of
// the new wording's letters in order, or nearly all of its words.
func shows(u string, c *change) bool {
	cu := core(u)
	switch {
	case c.action == amend.Repeal && c.part == "":
		return len([]rune(cu)) < 60 && strings.Contains(cu, "ملغ")
	case c.action == amend.Insert && c.part == "":
		return cu != ""
	case c.text == "" || cu == "":
		return false
	}
	w := unchangedNote.ReplaceAllString(c.text, " ")
	ct := core(w)
	if len([]rune(ct)) < 12 {
		return false
	}
	if strings.Contains(cu, ct) {
		return true
	}
	ww := words(w)
	if len(ww) < 8 {
		return false
	}
	return coverage(ww, words(u)) >= 0.9
}

// inside says whether the Centre's text u has the wording w and more: w's
// letters in order, with text after them or more than a heading before
// them. Then w is a part of the article (one clause replaced, a paragraph
// added), or the article changed again since, and u is the article. A
// wording u only resembles (most of its words, as shows accepts) proves
// nothing here: u may be the older text with another sum.
func inside(u, w string) bool {
	cu, cw := core(u), core(unchangedNote.ReplaceAllString(w, " "))
	i := strings.Index(cu, cw)
	if i < 0 || cw == "" {
		return false
	}
	before := utf8.RuneCountInString(cu[:i])
	after := utf8.RuneCountInString(cu[i+len(cw):])
	return after >= insideAfter || before+after > insideAround
}

// The Centre opens some articles with a heading or a note of its own
// ("ماده 21 المعدله", "المعدله بالقانون المنفذ بالمرسوم رقم 6102 …"), up to
// 60 letters in the corpus, and closes a few with a short one ("مرسوم
// تطبيقي لهذه الماده" is 21): a wording that lacks only those is still the
// whole article.
const (
	insideAfter  = 22
	insideAround = 60
)

// short says whether the wording w is under half the length of the
// Centre's text u: read as the whole article it would drop most of it. It
// is a part (named in a way the patterns missed), or it was cut short, or
// the article grew since; either way the change is a note until a person
// confirms it (db/amendments.tsv).
func short(u, w string) bool {
	lu, lw := utf8.RuneCountInString(core(u)), utf8.RuneCountInString(core(w))
	return lu > 0 && lw*2 < lu
}

// "[والباقي دون تعديل]", "(الباقي بدون تعديل)", "...": an amendment's own
// notes in its wording, never in the article.
var unchangedNote = regexp.MustCompile(`[\[(][^\])]{0,40}(?:دون تعديل|بدون تعديل|دون تغيير)[^\])]{0,10}[\])]|\.{3,}|…`)

// core keeps a text's letters and digits, folded: what two copies of the
// same wording share whatever their punctuation and spacing.
func core(s string) string {
	var b strings.Builder
	for _, r := range amend.FoldString(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// coverage is the share of a's words found in b.
func coverage(a, b map[string]bool) float64 {
	if len(a) == 0 {
		return 0
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a))
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(amend.FoldString(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[w] = true
	}
	return out
}

// corrections applies db/article_corrections.tsv: an article's wording where
// the Centre's is wrong, read by a person from the printed edition. Each is a
// minor version after the article's latest one (1.1 after the Centre's 1.0
// for an article never amended), with the same dates: the same legal state,
// worded right. vs are article_version rows, as versions makes them.
func (b *builder) corrections(vs [][]any) ([][]any, error) {
	for i, ln := range strings.Split(db.ArticleCorrections, "\n") {
		f := strings.Split(ln, "\t")
		if strings.HasPrefix(ln, "#") || strings.TrimSpace(ln) == "" || f[0] == "law_id" {
			continue
		}
		law, err := strconv.Atoi(f[0])
		if len(f) != 4 || err != nil || strings.TrimSpace(f[2]) == "" {
			b.rep.note("article_corrections.tsv line %d unreadable", i+1)
			continue
		}
		number, text, readFrom := f[1], CorrectionText(f[2]), f[3]
		var centre string
		var csrc textSource
		err = b.conn.QueryRow(b.ctx, `SELECT text_ar, coalesce(source_kind, ''), coalesce(source_id, '') FROM legislation_article
			WHERE law_id = $1 AND number = $2 ORDER BY position LIMIT 1`,
			law, number).Scan(&centre, &csrc.kind, &csrc.id)
		if errors.Is(err, pgx.ErrNoRows) {
			b.rep.note("article correction for %d article %s: no such article", law, number)
			continue
		} else if err != nil {
			return nil, err
		}
		var skip string
		if vs, skip = correct(vs, law, number, text, readFrom, centre, csrc); skip != "" {
			b.rep.note("article correction for %d article %s: %s", law, number, skip)
			continue
		}
		b.rep.Versions++
	}
	return vs, nil
}

// correct adds a correction row's wording as a minor version after the
// article's latest one in vs; centre is the Centre's text of the article,
// csrc where it was read. It says why when it adds nothing. The latest
// version can be an amending law's wording as the Centre carries it, with
// a typo the Centre's own article reads right: the row then quotes the
// Centre's article, and the latest version, not the Centre's, decides
// whether it still changes anything.
func correct(vs [][]any, law int, number, text, readFrom, centre string, csrc textSource) ([][]any, string) {
	last := -1
	for j, v := range vs {
		if v[0] == law && v[1] == number {
			last = j
		}
	}
	if last < 0 {
		if centre == text {
			return vs, "the Centre's text now reads the same; drop the row"
		}
		vs = append(vs, []any{law, number, 1, 0, nil, nil, "in_force", centre, "centre", nil, nil, ns(csrc.kind), ns(csrc.id)})
		last = len(vs) - 1
	}
	l := vs[last]
	if l[6] == "repealed" {
		return vs, "the article is repealed"
	}
	if t, ok := l[7].(string); ok && t == text {
		return vs, "the article's latest version now reads the same; drop the row"
	}
	// A person's wording: it passes the wording guard by its hash, as an
	// approved edit (internal/provenance reads the same file).
	return slices.Insert(vs, last+1, []any{law, number, l[2], l[3].(int) + 1, l[4], l[5], "in_force", text, "editorial", nil, readFrom,
		SourceEdit, CorrectionSource(law, number)}), ""
}
