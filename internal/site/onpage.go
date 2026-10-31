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

const titleMax = 60

const descMax = 155

const (
	descMin = 70
	descCap = 160
)

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

func enDate(b *i18n.Bundle, t *time.Time) string {
	if t == nil {
		return ""
	}
	if b.Lang == "ar" {
		return dmy(t)
	}
	return t.Format("2 Jan 2006")
}

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

func citeIn(b *i18n.Bundle, l *Law) string {
	if l.Code != nil && b.Lang != "ar" {
		return *l.Code
	}
	if b.Lang == "ar" {
		return l.CiteName()
	}
	return identIn(b, l.LawRow)
}

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

func fitTitle(ident, subject string, max int) string {
	subject = strings.TrimSpace(subject)
	room := max - len([]rune(ident)) - 3
	if subject == "" || subject == ident || room < 10 {
		return ident
	}
	return ident + " · " + cutWords(subject, room)
}

func (l *Law) amendedCount() int {
	if g := l.LinkGroups(); len(g) > 0 {
		return len(g)
	}
	return len(l.AmendedBy())
}

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

func lawTitle(b *i18n.Bundle, l *Law) string {
	return fitTitle(identIn(b, l.LawRow), subjectFor(b, l), titleMax)
}

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

func statusSentence(b *i18n.Bundle, status string) string {
	if status == "repealed" {
		return b.T("seo.ans_repealed")
	}
	return b.T("seo.ans_no_repeal")
}

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

func articleAnswer(b *i18n.Bundle, l *Law, number string) string {
	return strings.Join(articleFacts(b, l, number), " ")
}

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

func fitDesc(lead string, facts []string, max int) string {
	out := lead
	for _, f := range facts {
		if len([]rune(out))+1+len([]rune(f)) <= max {
			out += " " + f
		}
	}
	return cutWords(out, max)
}

func lawDesc(b *i18n.Bundle, l *Law) string {
	return fitDesc(identIn(b, l.LawRow)+".", lawFacts(b, l), descMax)
}

func artTitle(b *i18n.Bundle, l *Law, number string) string {
	return b.T("seo.art_title", number, cutWords(citeIn(b, l), titleMax-len([]rune(b.T("seo.art_title", number, "")))))
}

func legislationForce(status string) string {
	if status == "repealed" {
		return "https://schema.org/NotInForce"
	}
	return ""
}

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

var dataDateSQL = map[string]string{
	"laws":     `SELECT max(last_changed) FROM source_page WHERE doc_type IN ('law', 'lawtext')`,
	"rulings":  `SELECT max(last_changed) FROM source_page WHERE doc_type = 'ruling'`,
	"gazette":  `SELECT max(last_changed) FROM source_page WHERE doc_type = 'journal'`,
	"official": `SELECT max(loaded_at) FROM official_text WHERE superseded_by_law_id IS NULL`,
}

type Updated struct {
	Label string
	ISO   string
}

func (s *Server) updated(r *http.Request, kind string) *Updated {
	ctx, cancel := s.ctx(r)
	defer cancel()
	t := s.dataDate(ctx, kind, dataDateSQL[kind])
	if t.IsZero() {
		return nil
	}
	b := i18n.New(s.lang(r))
	return &Updated{Label: b.T("seo.updated", enDate(b, &t)), ISO: t.Format("2006-01-02")}
}

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
