package site

import (
	"context"
	"fmt"
	"net/http"
)

type CitedArticle struct {
	LawID   int
	Number  string
	LawName string
}

func (s *Server) rulingArticles(ctx context.Context, id int) ([]CitedArticle, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT e.to_law_id, e.to_article, l.kind || coalesce(' رقم ' || l.number, ''),
		coalesce(a.position, 2147483647)
		FROM graph_edge e
		JOIN legislation l ON l.id = e.to_law_id
		JOIN legislation_kind k ON k.name_ar = l.kind
		LEFT JOIN legislation_article a ON a.law_id = e.to_law_id AND a.number = e.to_article
		WHERE e.from_type = 'ruling' AND e.from_id = $1 AND e.status = 'linked'
		  AND e.to_article IS NOT NULL AND a.law_id IS NOT NULL
		ORDER BY e.to_law_id, e.to_article, coalesce(a.position, 2147483647)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[[2]string]bool{}
	var out []CitedArticle
	for rows.Next() {
		var x CitedArticle
		var position int
		if err := rows.Scan(&x.LawID, &x.Number, &x.LawName, &position); err != nil {
			return nil, err
		}
		key := [2]string{fmt.Sprint(x.LawID), x.Number}
		if !seen[key] {
			seen[key] = true
			out = append(out, x)
		}
	}
	return out, rows.Err()
}

func (s *Server) articleRulings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if s.rightWord(w, r, id) {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	l, err := s.loadLaw(ctx, id, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	want := r.PathValue("n")
	var article *Article
	for _, a := range l.List {
		if Slug(a.Number) == want {
			article = a
			break
		}
	}
	if article == nil {
		s.notFound(w, r)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT 'ruling', r.id, r.title, coalesce(r.court, ''), r.decided_on, NULL
		FROM graph_edge e JOIN ruling r ON r.id = e.from_id
		WHERE e.from_type = 'ruling' AND e.to_law_id = $1 AND e.to_article = $2 AND e.status = 'linked'
		GROUP BY r.id, r.title, r.court, r.decided_on ORDER BY r.decided_on DESC NULLS LAST, r.id DESC`, id, article.Number)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanDocRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	seen := map[int]bool{}
	unique := list[:0]
	for _, x := range list {
		if !seen[x.ID] {
			seen[x.ID] = true
			unique = append(unique, x)
		}
	}
	list = unique
	title := fmt.Sprintf("%s · %s", s.trOr(r, "Rulings citing article", "art.rulings_full_title"), article.Number)
	s.render(w, r, "list", 200, Page{Title: s.brandTitle(r, title), Path: artPath(id, article.Number) + "rulings/",
		Desc: s.trOr(r, "Every ruling in the graph that cites this article, newest first.", "art.rulings_full_desc"),
		Data: map[string]any{"Kind": map[string]string{
			"Back": lawPath(id), "BackLabel": l.Ident(), "Eyebrow": s.trOr(r, "Case law", "law.case_law_eyebrow"),
			"H1": s.trOr(r, "Rulings citing article", "art.rulings_full_title"), "Noun": s.trOr(r, "ruling", "rulings.noun"),
			"Many": s.trOr(r, "rulings", "rulings.many")}, "List": List{Total: len(list), Rows: list}}})
}
