package site

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// An AI answer's sources and what each paragraph rests on (a law student,
// 23 Nov 2026): every point must link to the exact page it comes from,
// never a search or a whole law, and each paragraph says whether its basis
// is the text of a law (which article), a court ruling in one case (which
// court, when), or neither.

var (
	srcArticlePage = regexp.MustCompile(`^(?:/(?:en|fr))?/(?:law|decree|text)/(\d+)/article/([^/?#]+)/$`)
	srcRulingPage  = regexp.MustCompile(`^(?:/(?:en|fr))?/ruling/(\d+)/$`)
	srcOtherPage   = regexp.MustCompile(`^(?:/(?:en|fr))?/(?:(?:opinion|agreement|research|doctrine)/\d+|topic/[a-z0-9-]+)/$`)
)

// exactSourcePath says whether an answer may cite path: one article, one
// ruling, or one explanatory document or topic page. A search, a list, a
// whole law or any address with a query is not a source.
func exactSourcePath(path string) bool {
	return srcArticlePage.MatchString(path) || srcRulingPage.MatchString(path) || srcOtherPage.MatchString(path)
}

// exactSources keeps the sources an answer may cite, in order.
func exactSources(in []answerSource) []answerSource {
	out := in[:0:0]
	for _, s := range in {
		if exactSourcePath(s.Path) {
			out = append(out, s)
		}
	}
	return out
}

// basisOf is one source's part of a paragraph's basis line: "law" with
// the article and the law's title, "ruling" with the court and the date,
// or "" for an explanation.
func (s *Server) basisOf(ctx context.Context, lang, path string) (kind, label string) {
	if m := srcArticlePage.FindStringSubmatch(path); m != nil {
		num, _ := url.PathUnescape(m[2])
		id, _ := strconv.Atoi(m[1])
		var title string
		if s.db != nil {
			_ = s.db.QueryRow(ctx, `SELECT title FROM legislation WHERE id=$1`, id).Scan(&title)
		}
		return "law", strings.TrimSpace(num + "|" + title)
	}
	if m := srcRulingPage.FindStringSubmatch(path); m != nil {
		id, _ := strconv.Atoi(m[1])
		var court string
		var on *time.Time
		if s.db != nil {
			_ = s.db.QueryRow(ctx, `SELECT coalesce(court,''), decided_on FROM ruling WHERE id=$1`, id).Scan(&court, &on)
		}
		return "ruling", rulingLabel(lang, court, on)
	}
	return "", ""
}

// labelBases gives every paragraph its basis line, from the sources it
// quotes: each article of a law it rests on, each ruling, or, when it
// quotes neither, that it has no law text or ruling behind it.
func (s *Server) labelBases(r *http.Request, v *aiView) {
	lang := s.lang(r)
	type basis struct{ kind, label string }
	bases := make([]basis, len(v.Sources))
	for i, src := range v.Sources {
		k, l := s.basisOf(r.Context(), lang, src.Path)
		bases[i] = basis{k, l}
	}
	for i := range v.Paras {
		var laws, rulings []string
		for _, n := range v.Paras[i].Refs {
			if n < 1 || n > len(bases) {
				continue
			}
			b := bases[n-1]
			switch b.kind {
			case "law":
				num, title, _ := strings.Cut(b.label, "|")
				if title == "" {
					laws = append(laws, s.tr(r, "search.ai_basis_article_only", num))
				} else {
					laws = append(laws, s.tr(r, "search.ai_basis_article", num, title))
				}
			case "ruling":
				if b.label == "" {
					rulings = append(rulings, s.tr(r, "search.ai_basis_ruling_undated"))
				} else {
					rulings = append(rulings, s.tr(r, "search.ai_basis_ruling", b.label))
				}
			}
		}
		var lines []string
		if len(laws) > 0 {
			lines = append(lines, s.tr(r, "search.ai_basis_law", strings.Join(dedupe(laws), "; ")))
		}
		lines = append(lines, dedupe(rulings)...)
		if len(lines) == 0 {
			lines = append(lines, s.tr(r, "search.ai_basis_none"))
		}
		v.Paras[i].Basis = strings.Join(lines, " · ")
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// rulingLabel names a ruling's court and date: "<court>, 26 December 1995".
// The court is isolated (FSI ... PDI): an Arabic court name inside an
// English or French line would otherwise swap places with the date's day.
func rulingLabel(lang, court string, on *time.Time) string {
	var parts []string
	if court != "" {
		parts = append(parts, "\u2068"+court+"\u2069")
	}
	if on != nil {
		parts = append(parts, longDate(lang, *on))
	}
	return strings.Join(parts, ", ")
}
