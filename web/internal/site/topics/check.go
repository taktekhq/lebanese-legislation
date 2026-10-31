package topics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Facts struct {
	Text  string
	Extra string
}

type Source interface {
	Article(law int, number string) (Facts, bool, error)
}

func Check(t *Topic, src Source) []string {
	p := Structure(t)
	if src != nil {
		p = append(p, Grounding(t, src)...)
	}
	return p
}

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func Structure(t *Topic) []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(t.Slug+": "+f, a...)) }
	if !slugRe.MatchString(t.Slug) {
		add("bad slug")
	}
	for _, d := range []string{t.Published, t.Updated} {
		if _, err := time.Parse("2006-01-02", d); err != nil || !dateRe.MatchString(d) {
			add("bad date %q", d)
		}
	}
	if t.Updated < t.Published {
		add("updated %s is before published %s", t.Updated, t.Published)
	}
	ids := map[string]bool{}
	for _, c := range t.Articles {
		if c.ID == "" || ids[c.ID] {
			add("cite id %q empty or repeated", c.ID)
		}
		ids[c.ID] = true
		if c.Law <= 0 || strings.TrimSpace(c.Article) == "" {
			add("cite %s has no law or article", c.ID)
		}
		if utf8.RuneCountInString(strings.TrimSpace(c.Quote)) < 8 {
			add("cite %s: quote is empty or too short", c.ID)
		}
	}
	used := map[string]bool{}
	check := func(lang string, c *Content) {
		l := func(f string, a ...any) { add("["+lang+"] "+f, a...) }
		for name, v := range map[string]string{"title": c.Title, "question": c.Question, "description": c.Description} {
			if strings.TrimSpace(v) == "" {
				l("%s is empty", name)
			}
		}
		if n := utf8.RuneCountInString(c.Description); n > 160 {
			l("description is %d characters, over 160", n)
		}
		if len(c.Sections) < 3 || len(c.Sections) > 6 {
			l("%d sections, want 3 to 6", len(c.Sections))
		}
		para := func(where string, q Para, answer bool) {
			if strings.TrimSpace(q.Text) == "" {
				l("%s: empty text", where)
			}
			if q.Note && answer {
				l("%s: the answer cannot be a note", where)
			}
			if len(q.Cites) == 0 && !q.Note {
				l("%s: states law with no cite and is not marked as a note", where)
			}
			if len(q.Cites) > 0 && q.Note {
				l("%s: a note carries no cite", where)
			}
			for _, id := range q.Cites {
				if !ids[id] {
					l("%s: cites %q which is not in articles", where, id)
				}
				used[id] = true
			}
		}
		para("answer", c.Answer, true)
		if n := sentences(c.Answer.Text); n < 1 || n > 4 {
			l("the answer is %d sentences, want 2 to 3 (4 at most)", n)
		}
		for i, s := range c.Sections {
			if strings.TrimSpace(s.Heading) == "" {
				l("section %d has no heading", i+1)
			}
			if len(s.Paras) == 0 && s.Table == nil {
				l("section %d is empty", i+1)
			}
			for j, q := range s.Paras {
				para(fmt.Sprintf("section %d paragraph %d", i+1, j+1), q, false)
			}
			if s.Table != nil {
				if len(s.Table.Head) == 0 || len(s.Table.Rows) == 0 {
					l("section %d: table without head or rows", i+1)
				}
				for k, r := range s.Table.Rows {
					if len(r.Cells) != len(s.Table.Head) {
						l("section %d row %d: %d cells for %d columns", i+1, k+1, len(r.Cells), len(s.Table.Head))
					}
					if len(r.Cites) == 0 {
						l("section %d row %d: no cite", i+1, k+1)
					}
					for _, id := range r.Cites {
						if !ids[id] {
							l("section %d row %d: cites %q which is not in articles", i+1, k+1, id)
						}
						used[id] = true
					}
				}
			}
		}
		for _, s := range allStrings(c) {
			if strings.ContainsRune(s, '\u2014') {
				l("contains an em dash: %.40q", s)
			}
		}
	}
	check("en", &t.EN)
	check("ar", &t.AR)
	for _, c := range t.Articles {
		if !used[c.ID] {
			add("cite %s is never used", c.ID)
		}
	}

	if a, b := shape(&t.EN), shape(&t.AR); a != b {
		add("English and Arabic differ in shape:\n  en %s\n  ar %s", a, b)
	}
	return p
}

func shape(c *Content) string {
	var b strings.Builder
	fmt.Fprintf(&b, "answer%v", c.Answer.Cites)
	for _, s := range c.Sections {
		b.WriteString(" | ")
		for _, q := range s.Paras {
			fmt.Fprintf(&b, "p%v%v ", q.Cites, q.Note)
		}
		if s.Table != nil {
			fmt.Fprintf(&b, "table%d", len(s.Table.Rows))
			for _, r := range s.Table.Rows {
				fmt.Fprintf(&b, "%v", r.Cites)
			}
		}
	}
	return b.String()
}

func allStrings(c *Content) []string {
	out := []string{c.Title, c.Question, c.Description, c.Answer.Text}
	for _, s := range c.Sections {
		out = append(out, s.Heading)
		for _, q := range s.Paras {
			out = append(out, q.Text)
		}
		if s.Table != nil {
			out = append(out, s.Table.Caption)
			out = append(out, s.Table.Head...)
			for _, r := range s.Table.Rows {
				out = append(out, r.Cells...)
			}
		}
	}
	return out
}

var sentenceEnd = regexp.MustCompile(`[.!?؟](?:\s|$)`)

func sentences(s string) int {
	return len(sentenceEnd.FindAllStringIndex(strings.TrimSpace(s)+" ", -1))
}

func Grounding(t *Topic, src Source) []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(t.Slug+": "+f, a...)) }
	facts := map[string]Facts{}
	for _, c := range t.Articles {
		f, ok, err := src.Article(c.Law, c.Article)
		if err != nil {
			add("cite %s: %v", c.ID, err)
			continue
		}
		if !ok {
			add("cite %s: article %s of law %d does not exist", c.ID, c.Article, c.Law)
			continue
		}
		facts[c.ID] = f
		if !strings.Contains(f.Text, c.Quote) {
			add("cite %s: the quote is not an exact piece of article %s of law %d: %.60q", c.ID, c.Article, c.Law, c.Quote)
		}
	}
	allowed := func(ids []string, extra ...string) map[string]bool {
		set := map[string]bool{}
		for _, id := range ids {
			f, ok := facts[id]
			if !ok {
				continue
			}
			for n := range Numbers(f.Text+"\n"+f.Extra, false) {
				set[n] = true
			}
			for n := range Numbers(artNumber(t, id), false) {
				set[n] = true
			}
		}
		for _, e := range extra {
			for n := range Numbers(e, false) {
				set[n] = true
			}
		}
		return set
	}
	var every []string
	for _, c := range t.Articles {
		every = append(every, c.ID)
	}
	artNums := map[string]bool{}
	for _, c := range t.Articles {
		for n := range Numbers(c.Article, false) {
			artNums[n] = true
		}
	}
	numbers := func(lang, where, text string, ids []string, all bool) {
		text = stripArticleRefs(text, artNums)
		set := allowed(ids)
		if all {
			set = allowed(every)
		}
		var bad []string
		for n := range Numbers(text, true) {
			if n == "0" || n == "1" || set[n] {

				continue
			}
			bad = append(bad, n)
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			add("[%s] %s: number %s is in no cited article: %.80q", lang, where, strings.Join(bad, ", "), text)
		}
	}
	for lang, c := range map[string]*Content{"en": &t.EN, "ar": &t.AR} {
		numbers(lang, "title", c.Title, nil, true)
		numbers(lang, "question", c.Question, nil, true)
		numbers(lang, "answer", c.Answer.Text, c.Answer.Cites, false)
		for i, s := range c.Sections {
			numbers(lang, fmt.Sprintf("heading %d", i+1), s.Heading, nil, true)
			for j, q := range s.Paras {
				if q.Note {
					numbers(lang, fmt.Sprintf("section %d note %d", i+1, j+1), q.Text, nil, true)
					continue
				}
				numbers(lang, fmt.Sprintf("section %d paragraph %d", i+1, j+1), q.Text, q.Cites, false)
			}
			if s.Table != nil {
				for k, r := range s.Table.Rows {
					numbers(lang, fmt.Sprintf("section %d row %d", i+1, k+1), strings.Join(r.Cells, " | "), r.Cites, false)
				}
			}
		}
	}
	sort.Strings(p)
	return p
}

func artNumber(t *Topic, id string) string {
	if c := t.Cite(id); c != nil {
		return c.Article
	}
	return ""
}

var articleRef = regexp.MustCompile(`(?i)(?:articles?|arts?\.?|المادة|المواد|المادتين|مادة|المادتان)\s*\d+(?:\s*(?:,|،|-|–|and|to|و)?\s*\d+)*`)
var digitsOnly = regexp.MustCompile(`\d+`)

func stripArticleRefs(text string, articles map[string]bool) string {
	text = digitsASCII.Replace(text)
	return articleRef.ReplaceAllStringFunc(text, func(m string) string {
		return digitsOnly.ReplaceAllStringFunc(m, func(d string) string {
			if articles[canon(d)] {
				return ""
			}
			return d
		})
	})
}
