package site

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/taktekhq/lebanese-legislation/web/internal/site/topics"
	"golang.org/x/text/unicode/norm"
)

type TopicHit struct{ Slug, Title, Snippet, Lang string }

func (h TopicHit) Path() string {
	prefix := ""
	if h.Lang == "en" {
		prefix = "/en"
	}
	return prefix + "/topic/" + h.Slug + "/"
}

func topicFold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) || r == 'ـ' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

var topicStop = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "in": true, "and": true, "or": true, "on": true, "for": true, "to": true, "at": true, "by": true, "with": true, "is": true, "are": true, "be": true, "what": true, "how": true, "does": true, "do": true, "can": true, "i": true, "my": true, "me": true, "it": true, "this": true, "that": true,
	"le": true, "la": true, "les": true, "de": true, "du": true, "des": true, "et": true, "ou": true, "un": true, "une": true, "en": true, "au": true, "aux": true, "pour": true, "sur": true, "dans": true, "est": true, "que": true, "qu": true, "l": true, "d": true,
}

func searchTopics(q string) []TopicHit {
	folded := topicFold(q)
	var terms []string
	for _, term := range strings.Fields(folded) {
		if !topicStop[term] {
			terms = append(terms, term)
		}
	}
	if len(terms) == 0 {
		return nil
	}
	type ranked struct {
		hit   TopicHit
		score int
	}
	var found []ranked
	for _, t := range topics.All() {
		var best *ranked
		for _, lang := range []string{"en", "ar"} {
			c := t.Lang(lang)
			parts := []string{c.Title, c.Question, c.Description, c.Answer.Text}
			for _, section := range c.Sections {
				parts = append(parts, section.Heading)
				for _, p := range section.Paras {
					parts = append(parts, p.Text)
				}
				if table := section.Table; table != nil {
					parts = append(parts, table.Caption)
					parts = append(parts, table.Head...)
					for _, row := range table.Rows {
						parts = append(parts, row.Cells...)
					}
				}
			}
			body := " " + topicFold(strings.Join(parts, " ")) + " "
			matches := true
			for _, term := range terms {
				if !strings.Contains(body, " "+term+" ") {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
			title := " " + topicFold(c.Title) + " "
			score := 0
			for _, term := range terms {
				if strings.Contains(title, " "+term+" ") {
					score += 10
				}
			}
			if strings.Contains(body, " "+folded+" ") {
				score += 20
			}
			snippet, bestPart := "", -1

			for i, part := range parts {
				normalized := " " + topicFold(part) + " "
				partScore := 0
				for _, term := range terms {
					if strings.Contains(normalized, " "+term+" ") {
						partScore++
					}
				}
				if strings.Contains(normalized, " "+folded+" ") {
					partScore += len(terms)
				}
				if i >= 3 && partScore > 0 {
					partScore++
				}
				if partScore > bestPart {
					snippet, bestPart = part, partScore
				}
			}
			hit := ranked{TopicHit{Slug: t.Slug, Title: c.Title, Snippet: snippet, Lang: lang}, score}
			if best == nil || hit.score > best.score {
				best = &hit
			}
		}
		if best != nil {
			found = append(found, *best)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	out := make([]TopicHit, 0, min(5, len(found)))
	for _, h := range found[:min(5, len(found))] {
		out = append(out, h.hit)
	}
	return out
}

func (s *Server) addEnglishTitles(ctx context.Context, res *Results) {
	if res == nil {
		return
	}
	laws, n := s.lawsByEnglishTitle(ctx, res.Q, 10+len(res.Laws))
	seen := make(map[int]bool, len(res.Laws))
	for _, l := range res.Laws {
		seen[l.ID] = true
	}
	for _, l := range laws {
		if seen[l.ID] || len(res.Laws) >= 10 {
			continue
		}
		res.Laws = append(res.Laws, l)
		seen[l.ID] = true
	}
	res.LawCount = max(res.LawCount, n, len(res.Laws))
	if len(englishTitlePats(res.Q)) > 0 {
		for i := range res.Laws {
			res.Laws[i].TitleEn = s.titles.get(ctx, s, res.Laws[i].ID)
		}
	}
}
