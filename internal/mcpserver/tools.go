package mcpserver

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Tool implementations, one per MCP tool (tools.json holds the schemas
// tools/list advertises). Every query reads the corpus only; nothing here
// writes, and no text is paraphrased: every text_ar / text_fr field is
// copied straight out of a row.

// toolResult is a tool's answer: the body (wrapped with the not-legal-advice
// line when it succeeded) and whether it is a tool-level error.
type toolResult struct {
	body    any
	isError bool
}

func ok(body any) toolResult { return toolResult{withAdvice(body), false} }

func fail(message string) toolResult { return toolResult{obj{{"error", message}}, true} }

var changeRelations = []string{"replace", "amend", "repeal", "insert", "suspend", "extend"}

var verbs = map[string]string{
	"replace": "Replaced or reworded",
	"amend":   "Edited in part",
	"repeal":  "Repealed",
	"insert":  "Added to",
	"suspend": "Suspended",
	"extend":  "Extended",
}

func verbFor(rel string) string {
	if v, ok := verbs[rel]; ok {
		return v
	}
	return "Changed"
}

// ---------------------------------------------------------------- get_law

// Enough for every code (the Penal Code has 787 articles, the Code of
// Obligations 1,100 or so); a longer list says it was cut.
const maxListItems = 2000

var (
	headingColon = regexp.MustCompile(`[` + jsSpace + `]*:[` + jsSpace + `]*[-:]?[` + jsSpace + `]*-?[` + jsSpace + `]*`)
	headingDash  = regexp.MustCompile(`[` + jsSpace + `]*-[` + jsSpace + `]*-[` + jsSpace + `]*`)
	trailColons  = regexp.MustCompile(`:+$`)
)

// heading tidies a chapter heading the way the site shows it:
// "الباب الاول :- في" becomes "الباب الاول: في".
func heading(h string) string {
	h = headingColon.ReplaceAllString(h, ": ")
	h = headingDash.ReplaceAllString(h, " - ")
	return jsTrim(trailColons.ReplaceAllString(jsTrim(h), ""))
}

// lawSections: a text's chapters; note is the Centre's editorial note on a
// chapter ("اضيف هذا الجزء ... بموجب ...").
func (s *Server) lawSections(ctx context.Context, id int64) ([]any, error) {
	rows, err := s.db.Query(ctx, `SELECT id, parent_id, depth, heading, note FROM legislation_section WHERE law_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var sid, depth int
		var parent *int
		var h string
		var note *string
		if err := rows.Scan(&sid, &parent, &depth, &h, &note); err != nil {
			return nil, err
		}
		o := obj{{"id", sid}, {"depth", depth}, {"heading", heading(h)}}
		if parent != nil {
			o.set("parent_id", *parent)
		}
		o.nonEmpty("note", note)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Server) getLaw(ctx context.Context, a args) (toolResult, error) {
	id, shown, bad := idArg(a["id"])
	if bad {
		return fail("id must be a positive integer"), nil
	}
	l, err := s.loadLaw(ctx, id)
	if err != nil {
		return toolResult{}, err
	}
	if l == nil {
		return fail("law " + shown + " not found"), nil
	}
	rows, err := s.db.Query(ctx, `SELECT position, number, section_id FROM legislation_article WHERE law_id = $1 ORDER BY position LIMIT $2`, id, maxListItems+1)
	if err != nil {
		return toolResult{}, err
	}
	articles := []any{}
	n := 0
	for rows.Next() {
		var pos int
		var num string
		var section *int
		if err := rows.Scan(&pos, &num, &section); err != nil {
			rows.Close()
			return toolResult{}, err
		}
		n++
		if n > maxListItems {
			continue
		}
		o := obj{{"position", pos}, {"number", num}, {"url", articleURL(int(id), num, l.Kind)}}
		if section != nil {
			o.set("section_id", *section)
		}
		articles = append(articles, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return toolResult{}, err
	}
	amendedBy, err := s.lawLevelLinks(ctx, id, "to")
	if err != nil {
		return toolResult{}, err
	}
	amended, err := s.lawLevelLinks(ctx, id, "from")
	if err != nil {
		return toolResult{}, err
	}
	toc, err := s.lawSections(ctx, id)
	if err != nil {
		return toolResult{}, err
	}
	o := lawSummary(l)
	o.str("gazette_number", l.GazetteNumber)
	o.str("gazette_date", l.GazetteDate)
	if l.GazetteIssueID != nil {
		o.set("gazette_issue_id", *l.GazetteIssueID)
	}
	o.set("table_of_contents", toc)
	o.set("articles", articles)
	if n > maxListItems {
		o.set("articles_truncated", fmt.Sprintf("the first %d of %d; get_law_texts pages through all of them", maxListItems, l.ArticleCount))
	}
	o.set("amended_by", amendedBy)
	o.set("amended", amended)
	return ok(o), nil
}

// lawLevelLinks: the texts that changed this one (direction "to") or that
// this one changed (direction "from"), oldest first, one entry per relation
// and other text. Edges to or from a single article count too: the Labour
// Code is changed mostly article by article, so articles lists which.
func (s *Server) lawLevelLinks(ctx context.Context, lawID int64, direction string) ([]any, error) {
	join, cond := "ge.from_id", "ge.to_law_id = $1 AND ge.from_type = 'legislation' AND ge.from_id <> $1"
	if direction == "from" {
		join, cond = "ge.to_law_id", "ge.from_id = $1 AND ge.from_type = 'legislation' AND ge.to_law_id <> $1"
	}
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+`, ge.relation, ge.to_article FROM graph_edge ge JOIN legislation l ON l.id = `+join+` `+s.sql(ctx).lawJoins+`
		WHERE `+cond+` AND ge.status IN ('linked', 'no_article') AND ge.relation = ANY($2)
		ORDER BY `+lawDate+` ASC NULLS FIRST, l.id ASC, ge.id ASC`, lawID, changeRelations)
	if err != nil {
		return nil, err
	}
	type extra struct {
		rel string
		art *string
	}
	var extras []*extra
	laws, err := scanLaws(rows, func() []any {
		e := &extra{}
		extras = append(extras, e)
		return []any{&e.rel, &e.art}
	})
	if err != nil {
		return nil, err
	}
	// The date of the change is the amending text's: the other law's for
	// amended_by, this law's for amended.
	var self *lawRow
	if direction == "from" {
		if self, err = s.loadLaw(ctx, lawID); err != nil {
			return nil, err
		}
	}
	type entry struct {
		o        obj
		articles []string
	}
	var order []string
	byKey := map[string]*entry{}
	for i := range laws {
		r := &laws[i]
		e := extras[i]
		key := e.rel + "|" + strconv.Itoa(r.ID)
		en, seen := byKey[key]
		if !seen {
			d := r
			if self != nil {
				d = self
			}
			o := obj{{"relation", e.rel}, {"verb", verbFor(e.rel)}, {"law", lawSummary(r)}}
			if d.GazetteDate != nil && *d.GazetteDate != "" {
				o.set("effective_on", *d.GazetteDate)
			} else {
				o.str("effective_on", d.IssuedOn)
			}
			en = &entry{o: o}
			byKey[key] = en
			order = append(order, key)
		}
		if e.art != nil && *e.art != "" {
			if !contains(en.articles, *e.art) {
				en.articles = append(en.articles, *e.art)
			}
		}
	}
	out := []any{}
	for _, k := range order {
		en := byKey[k]
		if en.articles != nil {
			sort.SliceStable(en.articles, func(i, j int) bool { return naturalLess(en.articles[i], en.articles[j]) })
			en.o.set("articles", en.articles)
		}
		out = append(out, en.o)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// naturalLess orders article numbers as localeCompare(x, y, "en",
// { numeric: true }) does: runs of digits by their value, other characters
// spaces, then punctuation, then symbols, before digits and letters.
func naturalLess(x, y string) bool { return naturalCompare(x, y) < 0 }

func naturalCompare(x, y string) int {
	a, b := []rune(x), []rune(y)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if unicode.IsDigit(a[i]) && unicode.IsDigit(b[j]) {
			si := i
			for i < len(a) && unicode.IsDigit(a[i]) {
				i++
			}
			sj := j
			for j < len(b) && unicode.IsDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(string(a[si:i]), "0")
			nb := strings.TrimLeft(string(b[sj:j]), "0")
			if len(na) != len(nb) {
				return cmpInt(len(na), len(nb))
			}
			if na != nb {
				return strings.Compare(na, nb)
			}
			continue
		}
		ca, cb := charClass(a[i]), charClass(b[j])
		if ca != cb {
			return cmpInt(ca, cb)
		}
		ra, rb := unicode.ToLower(a[i]), unicode.ToLower(b[j])
		if ra != rb {
			return cmpInt(int(ra), int(rb))
		}
		i++
		j++
	}
	return cmpInt(len(a)-i, len(b)-j)
}

func charClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case unicode.IsPunct(r):
		return 1
	case unicode.IsSymbol(r):
		return 2
	case unicode.IsDigit(r):
		return 3
	}
	return 4
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- get_law_texts

func (s *Server) getLawTexts(ctx context.Context, a args) (toolResult, error) {
	if msg := offsetGone(a, "For one article use get_article."); msg != "" {
		return fail(msg), nil
	}
	id, shown, bad := idArg(a["law_id"])
	if bad {
		return fail("law_id must be a positive integer"), nil
	}
	from, after, msg := readCursor(a["cursor"], positionCursor)
	if msg != "" {
		return fail(msg), nil
	}
	l, err := s.loadLaw(ctx, id)
	if err != nil {
		return toolResult{}, err
	}
	if l == nil {
		return fail("law " + shown + " not found"), nil
	}
	limit := clampInt(a["limit"], 50, 1, 200)
	total, err := s.count(ctx, `SELECT count(*) FROM legislation_article WHERE law_id = $1`, id)
	if err != nil {
		return toolResult{}, err
	}
	// The primary key (law_id, position) answers the page at any depth; one
	// row more than the page says whether there is a next one.
	where, params := `a.law_id = $1 ORDER BY a.position LIMIT $2`, []any{id, limit + 1}
	if after {
		where, params = `a.law_id = $1 AND a.position > $3 ORDER BY a.position LIMIT $2`, append(params, from)
	}
	rows, err := s.queryArticles(ctx, where, params...)
	if err != nil {
		return toolResult{}, err
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = cursor.Int(int64(rows[limit-1].Position))
	}
	errs, err := s.centreErrors(ctx, id, nil)
	if err != nil {
		return toolResult{}, err
	}
	today := s.today()
	articles := []any{}
	for i := range rows {
		r := &rows[i]
		o := obj{{"position", r.Position}, {"number", r.Number}}
		if r.SectionID != nil {
			o.set("section_id", *r.SectionID)
		}
		o.str("note", r.Note)
		if e, has := errs[r.Number]; has {
			o.set("centre_error", e.obj())
		}
		o.merge(articleTexts(r, l.Status == "repealed"))
		if c := calculatedCurrent(r, l.Status, today); c != nil {
			o.set("calculated_current", c)
		}
		o.set("url", articleURL(int(id), r.Number, l.Kind))
		articles = append(articles, o)
	}
	out := obj{{"law", lawSummary(l)}, {"total", total}}
	if next != "" {
		out.set("next_cursor", next)
	}
	out.set("articles", articles)
	return ok(out), nil
}

// ---------------------------------------------------------------- get_article

func (s *Server) getArticle(ctx context.Context, a args) (toolResult, error) {
	lawID, shown, bad := idArg(a["law_id"])
	num, err := strArg(a["number"])
	if err != nil {
		return toolResult{}, err
	}
	asked := jsTrim(num)
	if bad || asked == "" {
		return fail("law_id (a positive integer) and number are required"), nil
	}
	l, err := s.loadLaw(ctx, lawID)
	if err != nil {
		return toolResult{}, err
	}
	if l == nil {
		return fail("law " + shown + " not found"), nil
	}
	var art *articleRow
	for _, n := range numberForms(asked) {
		if art, err = s.firstArticle(ctx, lawID, n); err != nil {
			return toolResult{}, err
		}
		if art != nil {
			break
		}
	}
	if art == nil {
		return fail("article " + mustMarshal(asked) + " not found in law " + shown), nil
	}
	number := art.Number
	changes, err := s.articleChanges(ctx, lawID, number)
	if err != nil {
		return toolResult{}, err
	}
	affected, err := s.articleAffected(ctx, lawID, number)
	if err != nil {
		return toolResult{}, err
	}
	errs, err := s.centreErrors(ctx, lawID, &number)
	if err != nil {
		return toolResult{}, err
	}
	o := obj{{"law", lawSummary(l)}, {"number", number}}
	if art.SectionID != nil {
		o.set("section_id", *art.SectionID)
	}
	o.str("note", art.Note)
	if e, has := errs[number]; has {
		o.set("centre_error", e.obj())
	}
	o.merge(articleTexts(art, l.Status == "repealed"))
	if c := calculatedCurrent(art, l.Status, s.today()); c != nil {
		o.set("calculated_current", c)
	}
	o.set("changes", changes)
	o.set("possibly_affected_by", affected)
	o.set("url", articleURL(int(lawID), number, l.Kind))
	o.str("source_url", l.SourceURL)
	o.changed(l.Changed) // an article belongs to its text's page
	return ok(o), nil
}

// articleChanges: every text that changed this article, oldest first, with
// the amending article's own words.
func (s *Server) articleChanges(ctx context.Context, lawID int64, number string) ([]any, error) {
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+`, ge.relation, ge.from_id, ge.from_article
		FROM graph_edge ge JOIN legislation l ON l.id = ge.from_id `+s.sql(ctx).lawJoins+`
		WHERE ge.to_law_id = $1 AND ge.to_article = $2 AND ge.from_id <> $1
		  AND ge.status IN ('linked', 'no_article') AND ge.relation = ANY($3)
		ORDER BY `+lawDate+` ASC NULLS FIRST, ge.id ASC`, lawID, number, changeRelations)
	if err != nil {
		return nil, err
	}
	type extra struct {
		rel   string
		byID  int
		byArt *string
	}
	var extras []*extra
	laws, err := scanLaws(rows, func() []any {
		e := &extra{}
		extras = append(extras, e)
		return []any{&e.rel, &e.byID, &e.byArt}
	})
	if err != nil {
		return nil, err
	}
	out := []any{}
	// The parser and the model often find the same change: list it once.
	seen := map[string]bool{}
	for i := range laws {
		r, e := &laws[i], extras[i]
		byArt := ""
		if e.byArt != nil {
			byArt = *e.byArt
		}
		key := e.rel + "|" + strconv.Itoa(e.byID) + "|" + byArt
		if seen[key] {
			continue
		}
		seen[key] = true
		o := obj{{"relation", e.rel}, {"verb", verbFor(e.rel)}, {"by", lawSummary(r)}, {"by_article", byArt}}
		if r.GazetteDate != nil {
			o.set("effective_on", *r.GazetteDate)
		} else {
			o.str("effective_on", r.IssuedOn)
		}
		if byArt != "" {
			var text string
			err := s.db.QueryRow(ctx, `SELECT text_ar FROM legislation_article WHERE law_id = $1 AND number = $2 ORDER BY position LIMIT 1`, e.byID, byArt).Scan(&text)
			if err == nil {
				o.set("text_ar", scanReadings(text))
			} else if !isNoRows(err) {
				return nil, err
			}
			o.set("url", articleURL(e.byID, byArt, ""))
		} else {
			o.set("url", fmt.Sprintf("%s/law/%d/", Base, e.byID))
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Server) articleAffected(ctx context.Context, lawID int64, number string) ([]any, error) {
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+`, p.repealing_article FROM possibly_affected p JOIN legislation l ON l.id = p.repealing_law_id `+s.sql(ctx).lawJoins+`
		WHERE p.law_id = $1 AND (p.article_number = $2 OR p.article_number IS NULL)
		ORDER BY `+lawDate+` ASC NULLS FIRST, l.id, p.repealing_article NULLS FIRST`, lawID, number)
	if err != nil {
		return nil, err
	}
	var arts []*string
	laws, err := scanLaws(rows, func() []any {
		arts = append(arts, nil)
		return []any{&arts[len(arts)-1]}
	})
	if err != nil {
		return nil, err
	}
	out := []any{}
	for i := range laws {
		r := &laws[i]
		o := obj{{"by", lawSummary(r)}}
		if arts[i] != nil && *arts[i] != "" {
			o.set("by_article", *arts[i])
			o.set("url", articleURL(r.ID, *arts[i], ""))
		} else {
			o.set("by_article", strOr(arts[i], ""))
			o.set("url", fmt.Sprintf("%s/law/%d/", Base, r.ID))
		}
		out = append(out, o)
	}
	return out, nil
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// ---------------------------------------------------------------- get_ruling

func (s *Server) getRuling(ctx context.Context, a args) (toolResult, error) {
	id, shown, bad := idArg(a["id"])
	if bad {
		return fail("id must be a positive integer"), nil
	}
	if id <= 0 || id > math.MaxInt32 {
		return fail("ruling " + shown + " not found"), nil
	}
	var r struct {
		id                                                       int
		court, courtEn, number, decided, president, text, srcURL *string
		year                                                     *int16
		title                                                    string
		members                                                  []string
		file                                                     bool
	}
	err := s.db.QueryRow(ctx, `SELECT r.id, r.court, c.name_en, r.number, r.year, to_char(r.decided_on, 'YYYY-MM-DD'), r.title, r.president, r.members, r.text_ar, sp.url,
		EXISTS (SELECT 1 FROM ruling_file rf WHERE rf.ruling_id = r.id)
		FROM ruling r
		LEFT JOIN court c ON c.name_ar = r.court
		LEFT JOIN source_page sp ON sp.doc_type = 'ruling' AND sp.centre_id = r.id
		WHERE r.id = $1`, id).Scan(&r.id, &r.court, &r.courtEn, &r.number, &r.year, &r.decided, &r.title, &r.president, &r.members, &r.text, &r.srcURL, &r.file)
	if isNoRows(err) {
		return fail("ruling " + shown + " not found"), nil
	}
	if err != nil {
		return toolResult{}, err
	}
	o := obj{{"id", r.id}, {"title", r.title}, {"url", fmt.Sprintf("%s/ruling/%d/", Base, r.id)}}
	o.nonEmpty("court", r.court)
	o.nonEmpty("court_en", r.courtEn)
	o.nonEmpty("number", r.number)
	if r.year != nil && *r.year != 0 {
		o.set("year", int(*r.year))
	}
	o.nonEmpty("decided_on", r.decided)
	o.nonEmpty("president", r.president)
	if len(r.members) > 0 {
		o.set("members", r.members)
	}
	if r.text != nil && *r.text != "" {
		o.set("text_ar", *r.text)
	} else if r.file {
		o.set("note", "The Centre holds this judgment as a scanned file only: pdf_url opens it on the Lebanese University's site; "+
			"the ruling page (url) shows a machine reading of it.")
	} else {
		o.set("note", "The Centre holds this judgment as a file only, not as typed text.")
	}
	// The scan at the source, for every ruling that has one: the original
	// is the reference, so every answer carries it.
	if r.file {
		o.set("pdf_url", pdfURL(r.id))
	}
	o.nonEmpty("source_url", r.srcURL)
	changed, err := s.pageChanged(ctx, "ruling", r.id)
	if err != nil {
		return toolResult{}, err
	}
	o.changed(changed)
	rows, err := s.db.Query(ctx, `SELECT n, text_ar, low_confidence FROM ruling_page WHERE ruling_id = $1 ORDER BY n`, id)
	if err != nil {
		return toolResult{}, err
	}
	defer rows.Close()
	var pages []any
	low := false
	for rows.Next() {
		var n int16
		var text string
		var lc bool
		if err := rows.Scan(&n, &text, &lc); err != nil {
			return toolResult{}, err
		}
		p := obj{{"n", int(n)}, {"text_ar", strings.NewReplacer("", "⟦", "", " | ", "", "⟧").Replace(text)}}
		if lc {
			p.set("low_confidence", true)
			low = true
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return toolResult{}, err
	}
	if len(pages) > 0 {
		o.set("pages", pages)
		note := "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference."
		if low {
			note += " Pages with low_confidence: true are hard to read (mostly handwriting): expect errors."
		}
		o.set("pages_note", note)
	}
	return ok(o), nil
}

// ---------------------------------------------------------------- get_gazette_issue

func (s *Server) getGazetteIssue(ctx context.Context, a args) (toolResult, error) {
	id, shown, bad := idArg(a["id"])
	if bad {
		return fail("id must be a positive integer"), nil
	}
	if id <= 0 || id > math.MaxInt32 {
		return fail("gazette issue " + shown + " not found"), nil
	}
	var gid int
	var number, published, src *string
	err := s.db.QueryRow(ctx, `SELECT g.id, g.number, to_char(g.published_on, 'YYYY-MM-DD'), sp.url
		FROM gazette_issue g LEFT JOIN source_page sp ON sp.doc_type = 'journal' AND sp.centre_id = g.id
		WHERE g.id = $1`, id).Scan(&gid, &number, &published, &src)
	if isNoRows(err) {
		return fail("gazette issue " + shown + " not found"), nil
	}
	if err != nil {
		return toolResult{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+` FROM gazette_entry e JOIN legislation l ON l.id = e.law_id `+s.sql(ctx).lawJoins+` WHERE e.issue_id = $1 ORDER BY l.id`, id)
	if err != nil {
		return toolResult{}, err
	}
	laws, err := scanLaws(rows, nil)
	if err != nil {
		return toolResult{}, err
	}
	texts := []any{}
	for i := range laws {
		texts = append(texts, lawSummary(&laws[i]))
	}
	o := obj{{"id", gid}}
	o.str("number", number)
	o.str("published_on", published)
	o.set("texts", texts)
	o.set("url", fmt.Sprintf("%s/gazette/%d/", Base, gid))
	o.str("source_url", src)
	changed, err := s.pageChanged(ctx, "gazette", gid)
	if err != nil {
		return toolResult{}, err
	}
	o.changed(changed)
	return ok(o), nil
}

// ---------------------------------------------------------------- get_links

var keyRE = regexp.MustCompile(`^(law|ruling|opinion|research|gazette)/(\d+)(?:/art/(.+))?$`)

// A quote is a whole article's wording at times, many KB; 200 of them made
// one oversized answer. Cut to this many characters (quote_truncated says so).
const quoteMax = 600

// enumArg is `args[k] ?? def` checked against the allowed values.
func enumArg(v any, def string, allowed ...string) (string, bool) {
	if v == nil {
		return def, true
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false
	}
	for _, a := range allowed {
		if s == a {
			return s, true
		}
	}
	return "", false
}

func (s *Server) getLinks(ctx context.Context, a args) (toolResult, error) {
	if msg := offsetGone(a, "To narrow the links use relation."); msg != "" {
		return fail(msg), nil
	}
	raw, err := strArg(a["key"])
	if err != nil {
		return toolResult{}, err
	}
	key := jsTrim(raw)
	m := keyRE.FindStringSubmatch(key)
	if m == nil {
		return fail(`key must look like "law/<id>", "law/<id>/art/<number>", "ruling/<id>", "opinion/<id>", "research/<id>" or "gazette/<id>"`), nil
	}
	direction, good := enumArg(a["direction"], "to", "to", "from")
	if !good {
		return fail(`direction must be "to" or "from"`), nil
	}
	order, good := enumArg(a["order"], "newest", "newest", "oldest")
	if !good {
		return fail(`order must be "newest" or "oldest"`), nil
	}
	isLaw := m[1] == "law" && m[3] == ""
	defScope := "law"
	if isLaw {
		defScope = "all"
	}
	scope, good := enumArg(a["scope"], defScope, "all", "law")
	if !good {
		return fail(`scope must be "all" or "law"`), nil
	}
	limit := clampInt(a["limit"], 50, 1, 200)
	from, after, msg := readCursor(a["cursor"], linkCursor)
	if msg != "" {
		return fail(msg), nil
	}

	// A law key with scope "all" takes in the edges of its articles too.
	params := []any{key}
	var cond string
	if isLaw && scope == "all" {
		id, _ := strconv.ParseInt(m[2], 10, 64)
		if id > math.MaxInt32 {
			id = -1 // no law has it
		}
		params = append(params, id)
		if direction == "to" {
			cond = "(ge.to_key = $1 OR ge.to_law_id = $2)"
		} else {
			cond = "(ge.from_key = $1 OR (ge.from_type = 'legislation' AND ge.from_id = $2))"
		}
	} else if direction == "to" {
		cond = "ge.to_key = $1"
	} else {
		cond = "ge.from_key = $1"
	}
	if truthy(a["relation"]) {
		params = append(params, jsString(a["relation"]))
		cond += fmt.Sprintf(" AND ge.relation = $%d", len(params))
	}
	const date = "coalesce(l.gazette_date, l.issued_on)"
	count, err := s.count(ctx, `SELECT count(*) FROM graph_edge ge WHERE `+cond, params...)
	if err != nil {
		return fail(fmt.Sprintf("could not read the links of %s: %s; try a smaller limit", key, jsSlice(err.Error(), 200))), nil
	}
	// The order is the texts with a date first, by date, then by edge id,
	// newest or oldest. It is written as one key whose parts all run the same
	// way, so the next page is one row comparison with the last edge shown:
	// newest is (dated, date, id) descending, oldest (undated, date, id)
	// ascending, with no date read as cursor.NoDate.
	first, dir, cmp := date+" IS NOT NULL", "DESC", "<"
	if order == "oldest" {
		first, dir, cmp = date+" IS NULL", "ASC", ">"
	}
	sortKey := fmt.Sprintf("(%s), coalesce(%s, date '%s'), ge.id", first, date, cursor.NoDate)
	if after {
		params = append(params, from.dated != (order == "oldest"), from.date, from.id)
		cond += fmt.Sprintf(" AND (%s) %s ($%d::boolean, $%d::date, $%d::integer)", sortKey, cmp, len(params)-2, len(params)-1, len(params))
	}
	params = append(params, limit+1)
	rows, err := s.db.Query(ctx, `SELECT ge.relation, ge.source, ge.status, ge.from_key, ge.to_key, ge.to_cite, ge.part, ge.quote,
		to_char(`+date+`, 'YYYY-MM-DD'), ge.id
		FROM graph_edge ge LEFT JOIN (legislation l JOIN legislation_kind k ON k.name_ar = l.kind) ON ge.from_type = 'legislation' AND l.id = ge.from_id
		WHERE `+cond+fmt.Sprintf(`
		ORDER BY (%s) %s, coalesce(%s, date '%s') %s, ge.id %s LIMIT $%d`, first, dir, date, cursor.NoDate, dir, dir, len(params)), params...)
	if err != nil {
		return fail(fmt.Sprintf("could not read the links of %s: %s; try a smaller limit", key, jsSlice(err.Error(), 200))), nil
	}
	defer rows.Close()
	links := []any{}
	var last linkKey
	next := ""
	for rows.Next() {
		var rel, source, status, fromKey string
		var toKey, toCite, part, quote, d *string
		var id int64
		if err := rows.Scan(&rel, &source, &status, &fromKey, &toKey, &toCite, &part, &quote, &d, &id); err != nil {
			return toolResult{}, err
		}
		if len(links) == limit {
			next = last.token()
			break
		}
		last = linkKey{d != nil, strOr(d, cursor.NoDate), id}
		o := obj{{"relation", rel}, {"source", source}, {"status", status}, {"from_key", fromKey}}
		o.str("to_key", toKey)
		o.str("to_cite", toCite)
		o.str("part", part)
		o.str("date", d)
		if quote != nil && *quote != "" {
			rs := []rune(*quote)
			if len(rs) > quoteMax {
				o.set("quote", string(rs[:quoteMax])+"…")
				o.set("quote_truncated", true)
			} else {
				o.set("quote", *quote)
			}
		}
		links = append(links, o)
	}
	if err := rows.Err(); err != nil {
		return toolResult{}, err
	}
	out := obj{{"key", key}, {"direction", direction}}
	if isLaw {
		out.set("scope", scope)
	}
	out.set("order", order)
	out.set("total", count)
	if next != "" {
		out.set("next_cursor", next)
	}
	out.set("links", links)
	return ok(out), nil
}

// ---------------------------------------------------------------- list_legislation, list_gazette_issues

var dateArgRe = regexp.MustCompile(`^(\d{4})(?:-(\d{1,2})(?:-(\d{1,2}))?)?$`)

// dateBound reads "2025", "2025-03" or "2025-03-17" as the first (or, for an
// upper bound, last) day it covers, or "" when it is not a real date.
func dateBound(v any, upper bool) string {
	var str string
	switch x := v.(type) {
	case string:
		str = x
	default:
		f, isNum := numberArg(v)
		if !isNum {
			return ""
		}
		str = jsNumberString(f)
	}
	m := dateArgRe.FindStringSubmatch(foldDigits(jsTrim(str)))
	if m == nil {
		return ""
	}
	y := atoi(m[1])
	if y < 1800 || y > 2200 {
		return ""
	}
	if m[2] == "" {
		if upper {
			return fmt.Sprintf("%d-12-31", y)
		}
		return fmt.Sprintf("%d-01-01", y)
	}
	mo := atoi(m[2])
	if mo < 1 || mo > 12 {
		return ""
	}
	last := time.Date(y, time.Month(mo)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if m[3] == "" {
		if upper {
			return fmt.Sprintf("%d-%02d-%02d", y, mo, last)
		}
		return fmt.Sprintf("%d-%02d-01", y, mo)
	}
	d := atoi(m[3])
	if d < 1 || d > last {
		return ""
	}
	return fmt.Sprintf("%d-%02d-%02d", y, mo, d)
}

type listRangeVal struct {
	since, until *string
	lo, hi       string
	order        string
}

// listRange validates since, until and order the same way for both listings.
func listRange(a args) (listRangeVal, string) {
	bad := func(name string) string {
		return name + ` must be a date as YYYY, YYYY-MM or YYYY-MM-DD (e.g. "2025" or "2025-05-16")`
	}
	var r listRangeVal
	if present(a["since"]) {
		if r.lo = dateBound(a["since"], false); r.lo == "" {
			return r, bad("since")
		}
		s := jsString(a["since"])
		r.since = &s
	}
	if present(a["until"]) {
		if r.hi = dateBound(a["until"], true); r.hi == "" {
			return r, bad("until")
		}
		s := jsString(a["until"])
		r.until = &s
	}
	if r.lo != "" && r.hi != "" && r.lo > r.hi {
		return r, "since is after until"
	}
	order, good := enumArg(a["order"], "newest", "newest", "oldest")
	if !good {
		return r, `order must be "newest" or "oldest"`
	}
	r.order = order
	return r, ""
}

const listMax = 100

// kindNames is the Arabic names of the kinds kind names: the kind itself, or
// the kinds whose English name it is (two are "Constitution").
func (s *Server) kindNames(ctx context.Context, kind string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT name_ar FROM legislation_kind WHERE name_ar = $1 OR lower(name_en) = lower($1) ORDER BY name_ar`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func (s *Server) listLegislation(ctx context.Context, a args) (toolResult, error) {
	if msg := offsetGone(a, "To jump to a period use since and until; for one text use get_law."); msg != "" {
		return fail(msg), nil
	}
	r, msg := listRange(a)
	if msg != "" {
		return fail(msg), nil
	}
	limit := clampInt(a["limit"], 50, 1, listMax)
	from, after, msg := readCursor(a["cursor"], dateIDCursor)
	if msg != "" {
		return fail(msg), nil
	}
	kind := ""
	if k, isStr := a["kind"].(string); isStr {
		kind = jsTrim(k)
	}
	where := []string{lawDate + " IS NOT NULL"}
	var params []any
	if r.lo != "" {
		params = append(params, r.lo)
		where = append(where, fmt.Sprintf("%s >= $%d::date", lawDate, len(params)))
	}
	if r.hi != "" {
		params = append(params, r.hi)
		where = append(where, fmt.Sprintf("%s <= $%d::date", lawDate, len(params)))
	}
	if kind != "" {
		// The kind's Arabic names are read first, so the filter is l.kind
		// itself and legislation_pub_kind answers the page in order. Matched
		// through a join on legislation_kind (or an IN subquery) the planner
		// cannot tell a rare kind from a common one and scanned every dated
		// text for it: 50 to 100 ms for a kind of one text.
		names, err := s.kindNames(ctx, kind)
		if err != nil {
			return toolResult{}, err
		}
		if len(names) > 1 {
			params = append(params, names)
			where = append(where, fmt.Sprintf("l.kind = ANY($%d::text[])", len(params)))
		} else {
			name := kind // no kind has that name, and no text has it as its kind
			if len(names) == 1 {
				name = names[0]
			}
			params = append(params, name)
			where = append(where, fmt.Sprintf("l.kind = $%d", len(params)))
		}
	}
	w := strings.Join(where, " AND ")
	dir, cmp := "DESC", "<"
	if r.order == "oldest" {
		dir, cmp = "ASC", ">"
	}
	// lawJoins is one row per law and every law has its kind (a foreign key),
	// so legislation alone counts the same rows.
	total, err := s.count(ctx, `SELECT count(*) FROM legislation l WHERE `+w, params...)
	if err != nil {
		return toolResult{}, err
	}
	if after {
		params = append(params, from.date, from.id)
		w += fmt.Sprintf(" AND (%s, l.id) %s ($%d::date, $%d::integer)", lawDate, cmp, len(params)-1, len(params))
	}
	params = append(params, limit+1)
	// The page's ids are picked first, from legislation_pub_date or
	// legislation_pub_kind, and only those rows are read in full: lawCols is
	// two subqueries and four joins. Every join is one row per law, so the
	// rows and their order are the same (internal/site/handlers.go, laws).
	order := fmt.Sprintf(` ORDER BY %s %s, l.id %s`, lawDate, dir, dir)
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+`, to_char(`+lawDate+`, 'YYYY-MM-DD')
		FROM (SELECT l.id FROM legislation l WHERE `+w+order+fmt.Sprintf(` LIMIT $%d`, len(params))+
		`) p JOIN legislation l ON l.id = p.id `+s.sql(ctx).lawJoins+order, params...)
	if err != nil {
		return toolResult{}, err
	}
	var dates []string
	laws, err := scanLaws(rows, func() []any {
		dates = append(dates, "")
		return []any{&dates[len(dates)-1]}
	})
	if err != nil {
		return toolResult{}, err
	}
	next := ""
	if len(laws) > limit {
		laws = laws[:limit]
		next = cursor.DateID(dates[limit-1], int64(laws[limit-1].ID))
	}
	list := []any{}
	for i := range laws {
		l := &laws[i]
		o := lawSummary(l)
		o.str("gazette_number", l.GazetteNumber)
		o.str("gazette_date", l.GazetteDate)
		if l.GazetteIssueID != nil {
			o.set("gazette_issue_id", *l.GazetteIssueID)
		}
		o.set("date", dates[i])
		list = append(list, o)
	}
	out := obj{}
	out.str("since", r.since)
	out.str("until", r.until)
	if kind != "" {
		out.set("kind", kind)
	}
	out.set("order", r.order)
	out.set("total", total)
	if next != "" {
		out.set("next_cursor", next)
	}
	out.set("laws", list)
	return ok(out), nil
}

func (s *Server) listGazetteIssues(ctx context.Context, a args) (toolResult, error) {
	if msg := offsetGone(a, "To jump to a period use since and until; for one issue use get_gazette_issue."); msg != "" {
		return fail(msg), nil
	}
	r, msg := listRange(a)
	if msg != "" {
		return fail(msg), nil
	}
	limit := clampInt(a["limit"], 50, 1, listMax)
	from, after, msg := readCursor(a["cursor"], dateIDCursor)
	if msg != "" {
		return fail(msg), nil
	}
	where := []string{"g.published_on IS NOT NULL"}
	var params []any
	if r.lo != "" {
		params = append(params, r.lo)
		where = append(where, fmt.Sprintf("g.published_on >= $%d::date", len(params)))
	}
	if r.hi != "" {
		params = append(params, r.hi)
		where = append(where, fmt.Sprintf("g.published_on <= $%d::date", len(params)))
	}
	w := strings.Join(where, " AND ")
	dir, cmp := "DESC", "<"
	if r.order == "oldest" {
		dir, cmp = "ASC", ">"
	}
	total, err := s.count(ctx, `SELECT count(*) FROM gazette_issue g WHERE `+w, params...)
	if err != nil {
		return toolResult{}, err
	}
	if after {
		params = append(params, from.date, from.id)
		w += fmt.Sprintf(" AND (g.published_on, g.id) %s ($%d::date, $%d::integer)", cmp, len(params)-1, len(params))
	}
	params = append(params, limit+1)
	// ids first, from gazette_pub_date, then the page's rows, as listLegislation
	order := fmt.Sprintf(` ORDER BY g.published_on %s, g.id %s`, dir, dir)
	rows, err := s.db.Query(ctx, `SELECT g.id, g.number, to_char(g.published_on, 'YYYY-MM-DD'),
		(SELECT count(*) FROM gazette_entry e WHERE e.issue_id = g.id)::int
		FROM (SELECT g.id FROM gazette_issue g WHERE `+w+order+fmt.Sprintf(` LIMIT $%d`, len(params))+
		`) p JOIN gazette_issue g ON g.id = p.id`+order, params...)
	if err != nil {
		return toolResult{}, err
	}
	defer rows.Close()
	issues := []any{}
	var last dateID
	next := ""
	for rows.Next() {
		var id, count int
		var number *string
		var published string
		if err := rows.Scan(&id, &number, &published, &count); err != nil {
			return toolResult{}, err
		}
		if len(issues) == limit {
			next = cursor.DateID(last.date, last.id)
			break
		}
		last = dateID{published, int64(id)}
		o := obj{{"id", id}}
		o.str("number", number)
		o.set("published_on", published)
		o.set("text_count", count)
		source := "centre"
		if id >= 2_000_000_000 {
			source = "scans"
		}
		o.set("source", source)
		o.set("url", fmt.Sprintf("%s/gazette/%d/", Base, id))
		issues = append(issues, o)
	}
	if err := rows.Err(); err != nil {
		return toolResult{}, err
	}
	out := obj{}
	out.str("since", r.since)
	out.str("until", r.until)
	out.set("order", r.order)
	out.set("total", total)
	if next != "" {
		out.set("next_cursor", next)
	}
	out.set("issues", issues)
	return ok(out), nil
}

// ---------------------------------------------------------------- possibly_affected

func (s *Server) possiblyAffected(ctx context.Context, a args) (toolResult, error) {
	lawID, shown, bad := idArg(a["law_id"])
	if bad {
		return fail("law_id must be a positive integer"), nil
	}
	target, err := s.loadLaw(ctx, lawID)
	if err != nil {
		return toolResult{}, err
	}
	if target == nil {
		return fail("law " + shown + " not found"), nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+s.sql(ctx).lawCols+`, p.repealing_article, p.article_number FROM possibly_affected p
		JOIN legislation l ON l.id = p.repealing_law_id `+s.sql(ctx).lawJoins+`
		WHERE p.law_id = $1 ORDER BY `+lawDate+` ASC NULLS FIRST, l.id, p.repealing_article NULLS FIRST, p.article_number NULLS FIRST`, lawID)
	if err != nil {
		return toolResult{}, err
	}
	type extra struct{ art, num *string }
	var extras []*extra
	laws, err := scanLaws(rows, func() []any {
		e := &extra{}
		extras = append(extras, e)
		return []any{&e.art, &e.num}
	})
	if err != nil {
		return toolResult{}, err
	}
	list := []any{}
	for i := range laws {
		e := extras[i]
		o := obj{{"by", lawSummary(&laws[i])}, {"by_article", strOr(e.art, "")}}
		o.str("article_number", e.num)
		if e.num != nil && *e.num != "" {
			o.set("url", articleURL(int(lawID), *e.num, ""))
		} else {
			o.set("url", fmt.Sprintf("%s/law/%d/", Base, lawID))
		}
		list = append(list, o)
	}
	return ok(obj{{"law", lawSummary(target)}, {"possibly_affected_by", list}}), nil
}
