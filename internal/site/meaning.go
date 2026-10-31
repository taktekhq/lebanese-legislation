package site

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

type QueryRewriter interface {
	Rewrite(context.Context, string) (nlsearch.Plan, error)
}

var ErrMeaningUnavailable = errors.New("search by meaning unavailable")

func (s *Server) SearchDocuments(ctx context.Context, q string) (Results, error) {
	if path := s.articleRef(ctx, q); path != "" {
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) == 4 {
			id, _ := strconv.Atoi(parts[1])
			rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar=l.kind WHERE l.id=$1`, id)
			if err != nil {
				return Results{}, err
			}
			laws, err := scanLawRows(rows)
			if err != nil {
				return Results{}, err
			}
			if len(laws) == 1 {
				a := laws[0]
				a.Article = parts[3]
				_ = s.db.QueryRow(ctx, `SELECT text_ar FROM legislation_article WHERE law_id=$1 AND number=$2 ORDER BY position LIMIT 1`, id, a.Article).Scan(&a.Snippet)
				a.Snippet = textWindow(a.Snippet, q, 900)
				return Results{Q: q, Articles: []LawRow{a}, ArtCount: 1}, nil
			}
		}
	}
	r, _, err := s.searchCached(ctx, q)
	return r, err
}

func (s *Server) SearchMeaning(ctx context.Context, q string, rw QueryRewriter) (Results, error) {
	t, _ := tidyQuery(q)
	if t == "" {
		t = q
	}
	if _, _, ok := parseArticleRef(t); ok && s.articleRef(ctx, t) != "" {
		return s.SearchDocuments(ctx, q)
	}
	rq := readQuery(t)
	if rq.lawCite() || rq.rulingCite() {
		return s.SearchDocuments(ctx, q)
	}
	baseline, err := s.SearchDocuments(ctx, q)
	if err != nil {
		return baseline, err
	}
	for _, l := range baseline.Laws {
		if arNormGo(strings.TrimSpace(l.Title)) == arNormGo(t) {
			return baseline, nil
		}
	}
	p, err := rw.Rewrite(ctx, q)
	if err != nil {
		return baseline, fmt.Errorf("%w: %v", ErrMeaningUnavailable, err)
	}
	out := Results{Q: q, Download: WantsDownload(q), Via: strings.Join(p.Queries, " / "), ViaKind: "meaning"}
	sets := []Results{baseline}
	topicSeen := map[string]bool{}
	for _, h := range baseline.Topics {
		topicSeen[h.Slug] = true
		out.Topics = append(out.Topics, h)
	}
	for _, term := range p.Queries {
		r, err := s.SearchDocuments(ctx, term)
		if err != nil {
			return baseline, err
		}
		sets = append(sets, r)
		for _, h := range r.Topics {
			if !topicSeen[h.Slug] && len(out.Topics) < 5 {
				topicSeen[h.Slug] = true
				out.Topics = append(out.Topics, h)
			}
		}
	}
	laws, articles, rulings := map[int]bool{}, map[string]bool{}, map[int]bool{}

	for i := 0; i < 10; i++ {
		for _, r := range sets {
			if i < len(r.Laws) {
				l := r.Laws[i]
				if !laws[l.ID] && len(out.Laws) < 10 {
					laws[l.ID] = true
					out.Laws = append(out.Laws, l)
				}
			}
			if i < len(r.Articles) {
				a := r.Articles[i]
				key := strconv.Itoa(a.ID) + "/" + a.Article
				if !articles[key] && len(out.Articles) < 10 {
					articles[key] = true
					out.Articles = append(out.Articles, a)
				}
			}
			if i < len(r.Rulings) {
				d := r.Rulings[i]
				if !rulings[d.ID] && len(out.Rulings) < 10 {
					rulings[d.ID] = true
					out.Rulings = append(out.Rulings, d)
				}
			}
		}
	}
	out.LawCount, out.ArtCount, out.RulCount = len(out.Laws), len(out.Articles), len(out.Rulings)
	return out, nil
}
