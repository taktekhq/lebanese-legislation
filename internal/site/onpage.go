package site

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

// On-page facts for titles, meta descriptions, answer lines and structured
// data. Everything here is built from the record's own fields (kind, number,
// date, Gazette issue, article count, amending texts, repeal flag) and UI
// strings; no model writes any of it. The one model-written thing the site
// shows, the unofficial English title, is labelled where it appears.

// titleMax is the length a page title aims for. The identifier is never cut;
// the subject takes what is left.
const titleMax = 60

// descMax is the length a meta description aims for.
const descMax = 155

// descMin is the shortest meta description a page ships; descCap the longest.
const (
	descMin = 70
	descCap = 160
)

// finishMeta puts a page's title and description inside the limits search
// engines and the Ahrefs audit check: a title of at most titleMax characters,
// a description of descMin to descCap. A short description gets the site's
// one-line description after it; nothing is invented per page.
func finishMeta(b *i18n.Bundle, title, desc string) (string, string) {
	if len([]rune(title)) > titleMax {
		title = cutWords(title, titleMax)
	}
	desc = strings.TrimSpace(desc)
	if len([]rune(desc)) < descMin {
		if desc != "" && !strings.HasSuffix(desc, ".") {
			desc += "."
		}
		desc = strings.TrimSpace(desc + " " + b.T("meta.pad"))
	}
	if len([]rune(desc)) > descCap {
		desc = cutWords(desc, descCap)
	}
	return title, desc
}

// enDate prints a date for an English sentence ("24 Nov 2015"); the Arabic
// pages keep the Lebanese 24/11/2015.
func enDate(b *i18n.Bundle, t *time.Time) string {
	if t == nil {
		return ""
	}
	if b.Lang == "ar" {
		return dmy(t)
	}
	return t.Format("2 Jan 2006")
}

// identIn is how a text is identified in the page's language: kind, number,
// date ("Law No. 44 of 24 Nov 2015", "قانون رقم 44 تاريخ 24/11/2015"). A kind
// with no English name falls back to the Arabic identifier.
func identIn(b *i18n.Bundle, l LawRow) string {
	kind := l.Kind
	if b.Lang != "ar" {
		if l.KindEn == nil || *l.KindEn == "" {
			return l.Ident()
		}
		kind = *l.KindEn
	}
	switch {
	case l.Number != nil && l.IssuedOn != nil:
		return b.T("seo.ident_nd", kind, *l.Number, enDate(b, l.IssuedOn))
	case l.Number != nil:
		return b.T("seo.ident_n", kind, *l.Number)
	case l.IssuedOn != nil:
		return b.T("seo.ident_d", kind, enDate(b, l.IssuedOn))
	}
	return kind
}

// citeIn is how an article names its text: a principal code by its name in
// the page's language, anything else by its identifier.
func citeIn(b *i18n.Bundle, l *Law) string {
	if l.Code != nil && b.Lang != "ar" {
		return *l.Code
	}
	if b.Lang == "ar" {
		return l.CiteName()
	}
	return identIn(b, l.LawRow)
}

// cutWords shortens s to at most n characters, on a word boundary when one is
// near, with an ellipsis that counts toward n.
func cutWords(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	r = r[:n-1]
	for i := len(r) - 1; i > n/2; i-- {
		if r[i] == ' ' {
			r = r[:i]
			break
		}
	}
	return strings.TrimRight(string(r), " ,;:-·") + "…"
}

// fitTitle joins identifier and subject with " · " within max characters. The
// identifier is never cut; the subject is dropped when under ten characters
// would be left for it.
func fitTitle(ident, subject string, max int) string {
	subject = strings.TrimSpace(subject)
	room := max - len([]rune(ident)) - 3
	if subject == "" || subject == ident || room < 10 {
		return ident
	}
	return ident + " · " + cutWords(subject, room)
}

// amendedCount is how many later texts the page lists as changing this one:
// the same list the "Amended by" row shows.
func (l *Law) amendedCount() int {
	if g := l.LinkGroups(); len(g) > 0 {
		return len(g)
	}
	return len(l.AmendedBy())
}

// subjectFor is the subject a page title and the English page lead with: the
// unofficial English title or a code's English name on the English site,
// otherwise the Arabic title as published.
func subjectFor(b *i18n.Bundle, l *Law) string {
	if b.Lang != "ar" {
		if l.TitleEn != nil {
			return *l.TitleEn
		}
		if l.Code != nil {
			return *l.Code
		}
	}
	return l.Title
}

// lawTitle is the <title> of a text's page: identifier first, then subject,
// about sixty characters.
func lawTitle(b *i18n.Bundle, l *Law) string {
	return fitTitle(identIn(b, l.LawRow), subjectFor(b, l), titleMax)
}

// gazetteSentence says where the text was published, from the record, or "".
func gazetteSentence(b *i18n.Bundle, l *Law) string {
	num := ""
	if l.GazetteNumber != nil {
		num = strings.TrimSpace(*l.GazetteNumber)
	}
	switch {
	case num != "" && l.GazetteDate != nil:
		return b.T("seo.ans_gazette_nd", num, enDate(b, l.GazetteDate))
	case num != "":
		return b.T("seo.ans_gazette_n", num)
	case l.GazetteDate != nil:
		return b.T("seo.ans_gazette_d", enDate(b, l.GazetteDate))
	}
	return ""
}

// statusSentence is honest about status: "repealed" only when the record says
// so, otherwise that no repeal is recorded. It never says "in force".
func statusSentence(b *i18n.Bundle, status string) string {
	if status == "repealed" {
		return b.T("seo.ans_repealed")
	}
	return b.T("seo.ans_no_repeal")
}

// lawFacts lists the record's facts about a text as sentences, in the order
// they matter: where published, size, amendments, language, status.
func lawFacts(b *i18n.Bundle, l *Law) []string {
	var out []string
	if s := gazetteSentence(b, l); s != "" {
		out = append(out, s)
	}
	if l.Articles > 0 {
		out = append(out, b.T("seo.ans_articles", thousands(l.Articles)))
	}
	if !l.Annulled() && !l.NotInForce() {
		out = append(out, statusSentence(b, l.Status))
	}
	for _, d := range l.Council {
		out = append(out, b.T("seo.ans_council_"+d.Effect, d.Decision, enDate(b, d.On)))
	}
	if n := l.amendedCount(); n > 0 {
		out = append(out, b.T("seo.ans_amended", thousands(n)))
	}
	if len(l.List) > 0 {
		if l.HasFr {
			out = append(out, b.T("seo.ans_lang_arfr"))
		} else {
			out = append(out, b.T("seo.ans_lang_ar"))
		}
	}
	return out
}

// lawAnswer is the answer line at the top of a text's page: what it is and
// what the record says about it, in one short paragraph of plain text.
func lawAnswer(b *i18n.Bundle, l *Law) string {
	ident := identIn(b, l.LawRow)
	lead := ident + "."
	if l.Code != nil {
		name := l.Title
		if b.Lang != "ar" {
			name = *l.Code
		}
		lead = b.T("seo.ans_code", name, ident)
	}
	return strings.Join(append([]string{lead}, lawFacts(b, l)...), " ")
}

// articleFacts are the sentences of an article page's answer line: which
// article of which text, then the text's record facts.
func articleFacts(b *i18n.Bundle, l *Law, number string) []string {
	cite := citeIn(b, l)
	parts := []string{b.T("seo.art_ans", number, cite)}
	if ident := identIn(b, l.LawRow); ident != cite {
		parts = append(parts, ident+".")
	}
	parts = append(parts, gazetteSentence(b, l))
	if !l.Annulled() && !l.NotInForce() {
		parts = append(parts, statusSentence(b, l.Status))
	}
	for _, a := range l.List {
		if a.Number != number {
			continue
		}
		for _, c := range a.Council {
			parts = append(parts, b.T("seo.art_council_"+c.Effect, c.Decision, enDate(b, c.On)))
		}
		break
	}
	return nonEmpty(parts)
}

// articleAnswer is the answer line on an article's page.
func articleAnswer(b *i18n.Bundle, l *Law, number string) string {
	return strings.Join(articleFacts(b, l, number), " ")
}

// articleDesc is the meta description of an article's page: the same facts,
// then the start of the article if there is room.
func articleDesc(b *i18n.Bundle, l *Law, number, text string) string {
	f := articleFacts(b, l, number)
	return fitDesc(f[0], append(f[1:], cutWords(text, 80)), descMax)
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// fitDesc joins the lead and as many of the facts as fit within max, keeping
// the order. A fact that does not fit is skipped, a shorter one may still.
func fitDesc(lead string, facts []string, max int) string {
	out := lead
	for _, f := range facts {
		if len([]rune(out))+1+len([]rune(f)) <= max {
			out += " " + f
		}
	}
	return cutWords(out, max)
}

// lawDesc is the meta description: identifier, then record facts, about 150
// characters.
func lawDesc(b *i18n.Bundle, l *Law) string {
	return fitDesc(identIn(b, l.LawRow)+".", lawFacts(b, l), descMax)
}

// artTitle is an article page's <title>: the article and its text's name.
func artTitle(b *i18n.Bundle, l *Law, number string) string {
	return b.T("seo.art_title", number, cutWords(citeIn(b, l), titleMax-len([]rune(b.T("seo.art_title", number, "")))))
}

// legislationForce is the schema.org legal-force value the record proves, or
// "". Only a repeal is a fact in the record; "in force" is only the absence of
// one, so it is not claimed.
func legislationForce(status string) string {
	if status == "repealed" {
		return "https://schema.org/NotInForce"
	}
	return ""
}

// dataDate returns the date the archive last saw a change in the source
// records of the given kinds (source_page.last_changed), cached for a few
// minutes. Zero when unknown.
func (s *Server) dataDate(ctx context.Context, key, query string) time.Time {
	k := json.RawMessage(strconv.Quote(key))
	if v, ok := s.ttlLRU.get("dataDate", k); ok {
		return v.(time.Time)
	}
	var t *time.Time
	if err := s.db.QueryRow(ctx, query).Scan(&t); err != nil || t == nil {
		return time.Time{}
	}
	s.ttlLRU.set("dataDate", k, *t)
	return *t
}

// Queries for the "last updated" line of the list pages, by kind.
var dataDateSQL = map[string]string{
	"laws":     `SELECT max(last_changed) FROM source_page WHERE doc_type IN ('law', 'lawtext')`,
	"rulings":  `SELECT max(last_changed) FROM source_page WHERE doc_type = 'ruling'`,
	"gazette":  `SELECT max(last_changed) FROM source_page WHERE doc_type = 'journal'`,
	"official": `SELECT max(loaded_at) FROM official_text WHERE superseded_by_law_id IS NULL`,
}

// Updated is the "Last updated <date>" line of a list page, or of a single
// page (lastchanged.go), which also sets Date and By.
type Updated struct {
	Label string  // "Last updated 20 Oct 2026"
	ISO   string  // 2026-10-20, for <time datetime>
	Date  string  // the date alone, "20 October 2026", for a facts row
	By    *LawRow // the text that last amended the page's text, when known
}

// updated builds a list page's "Last updated" line from the date the archive
// last saw a change in that list's source records; nil when it is not known.
func (s *Server) updated(r *http.Request, kind string) *Updated {
	ctx, cancel := s.ctx(r)
	defer cancel()
	t := s.dataDate(ctx, kind, dataDateSQL[kind])
	if t.IsZero() {
		return nil
	}
	b := s.bundle(r)
	return &Updated{Label: b.T("seo.updated", enDate(b, &t)), ISO: t.Format("2006-01-02")}
}

// englishLD points the addresses inside a breadcrumb or list at the English
// pages, so structured data on /en/ names /en/ URLs (the legislation objects
// are built with theirs). Other objects pass through.
func englishLD(in []any) []any {
	out := make([]any, len(in))
	for i, o := range in {
		out[i] = o
		m, ok := o.(map[string]any)
		if !ok || (m["@type"] != "BreadcrumbList" && m["@type"] != "ItemList") {
			continue
		}
		items, ok := m["itemListElement"].([]any)
		if !ok {
			continue
		}
		cp := make(map[string]any, len(m))
		for k, v := range m {
			cp[k] = v
		}
		var ni []any
		for _, it := range items {
			im, ok := it.(map[string]any)
			if !ok {
				ni = append(ni, it)
				continue
			}
			c := make(map[string]any, len(im))
			for k, v := range im {
				c[k] = v
			}
			for _, k := range []string{"item", "url"} {
				if u, ok := c[k].(string); ok && strings.HasPrefix(u, Base+"/") && !strings.HasPrefix(u, Base+"/en/") {
					c[k] = Base + "/en" + strings.TrimPrefix(u, Base)
				}
			}
			ni = append(ni, c)
		}
		cp["itemListElement"] = ni
		out[i] = cp
	}
	return out
}
