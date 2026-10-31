package site

import (
	"context"
	"sort"
	"time"
)

type Link struct {
	Relation  string
	Part      *string
	By        LawRow
	ByArticle string
	Effective *time.Time
	Text      string
	Sources   []string
}

func (k Link) Verb() string {
	switch k.Relation {
	case "replace":
		return "Replaced or reworded"
	case "amend":
		return "Edited in part"
	case "repeal":
		return "Repealed"
	case "insert":
		return "Added to"
	case "suspend":
		return "Suspended"
	case "extend":
		return "Extended"
	case "note_amended_by":
		return "Amended, as the Centre notes,"
	case "note_repealed_by":
		return "Repealed, as the Centre notes,"
	case "note_added_by":
		return "Added, as the Centre notes,"
	}
	return "Changed"
}

func (k Link) VerbKey() string {
	switch k.Relation {
	case "replace", "amend", "repeal", "insert", "suspend", "extend",
		"note_amended_by", "note_repealed_by", "note_added_by":
		return k.Relation
	}
	return "default"
}

type Affected struct {
	By        LawRow
	ByArticle string
}

var linkRelations = []string{"replace", "amend", "repeal", "insert", "suspend", "extend",
	"note_amended_by", "note_repealed_by", "note_added_by"}

func (s *Server) attachLinks(ctx context.Context, l *Law, byNumber map[string]*Article) error {
	rows, err := s.db.Query(ctx, `
		WITH e AS (
		  SELECT e.from_id AS by_id, e.from_article AS by_article, e.to_article AS article,
		         e.relation, e.part, e.source
		  FROM graph_edge e
		  WHERE e.to_law_id = $1 AND e.status IN ('linked', 'no_article') AND e.relation = ANY($2)
		    AND e.relation NOT LIKE 'note_%' AND e.from_id <> $1
		  UNION ALL
		  SELECT e.to_law_id, NULL, e.from_article, e.relation, NULL, e.source
		  FROM graph_edge e
		  WHERE e.from_type = 'legislation' AND e.from_id = $1 AND e.relation LIKE 'note_%'
		    AND e.status = 'linked' AND e.to_law_id <> $1
		)
		SELECT e.by_id, coalesce(e.by_article, ''), coalesce(e.article, ''), e.relation, e.part, e.source,
		       coalesce(l.gazette_date, l.issued_on), coalesce(a.text_ar, ''), `+lawRowCols+`
		FROM e JOIN legislation l ON l.id = e.by_id JOIN legislation_kind k ON k.name_ar = l.kind
		LEFT JOIN legislation_article a ON a.law_id = e.by_id AND a.number = e.by_article`, l.ID, linkRelations)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct {
		by         int
		byArt, art string
	}
	seen := map[key]*Link{}
	var order []key
	for rows.Next() {
		var k Link
		var art, source string
		dest := append([]any{new(int), &k.ByArticle, &art, &k.Relation, &k.Part, &source, &k.Effective, &k.Text}, lawRowDest(&k.By)...)
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		id := key{k.By.ID, k.ByArticle, art}
		if prev, ok := seen[id]; ok {
			prev.Sources = appendNew(prev.Sources, source)
			continue
		}
		k.Sources = []string{source}
		seen[id] = &k
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := seen[order[i]].Effective, seen[order[j]].Effective
		return a != nil && (b == nil || a.Before(*b))
	})
	for _, id := range order {
		k := *seen[id]
		if id.art == "" {
			l.Links = append(l.Links, k)
			continue
		}
		if a := byNumber[id.art]; a != nil {
			a.Links = append(a.Links, k)
		}
	}

	rows, err = s.db.Query(ctx, `SELECT p.repealing_article, coalesce(p.article_number, ''), `+lawRowCols+`
		FROM possibly_affected p JOIN legislation l ON l.id = p.repealing_law_id JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE p.law_id = $1 ORDER BY l.issued_on, p.repealing_article`, l.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var f Affected
		var art string
		dest := append([]any{&f.ByArticle, &art}, lawRowDest(&f.By)...)
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		if art == "" {
			l.Affected = append(l.Affected, f)
		} else if a := byNumber[art]; a != nil {
			a.Affected = append(a.Affected, f)
		}
	}
	return rows.Err()
}

func appendNew(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

type LinkGroup struct {
	By       LawRow
	Articles []string
	latest   *time.Time
}

func (g LinkGroup) Arts() string { return ChangeGroup{Articles: g.Articles}.Arts() }

func (l *Law) LinkGroups() []LinkGroup {
	byID := map[int]*LinkGroup{}
	var order []int
	add := func(k Link, art string) {
		g := byID[k.By.ID]
		if g == nil {
			g = &LinkGroup{By: k.By}
			byID[k.By.ID] = g
			order = append(order, k.By.ID)
		}
		g.Articles = appendNew(g.Articles, art)
		if k.Effective != nil && (g.latest == nil || k.Effective.After(*g.latest)) {
			g.latest = k.Effective
		}
	}
	for _, k := range l.Links {
		add(k, "")
	}
	for _, a := range l.List {
		for _, k := range a.Links {
			add(k, a.Number)
		}
	}
	out := make([]LinkGroup, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].latest, out[j].latest
		return a != nil && (b == nil || a.After(*b))
	})
	return out
}

type RulingRef struct {
	ID     int
	Court  string
	Number string
	Year   *int16
	On     *time.Time
}

const maxRulings = 20

func (s *Server) attachRulings(ctx context.Context, l *Law, byNumber map[string]*Article) error {
	rows, err := s.db.Query(ctx, `SELECT e.to_article, r.id, coalesce(r.court, ''), coalesce(r.number, ''), r.year, r.decided_on
		FROM graph_edge e JOIN ruling r ON r.id = e.from_id
		WHERE e.from_type = 'ruling' AND e.to_law_id = $1 AND e.to_article IS NOT NULL
		  AND e.status = 'linked'
		ORDER BY r.decided_on DESC NULLS LAST, r.id DESC`, l.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]map[int]bool{}
	for rows.Next() {
		var art string
		var x RulingRef
		if err := rows.Scan(&art, &x.ID, &x.Court, &x.Number, &x.Year, &x.On); err != nil {
			return err
		}
		a := byNumber[art]
		if a == nil {
			continue
		}
		if seen[art] == nil {
			seen[art] = map[int]bool{}
		}
		if seen[art][x.ID] {
			continue
		}
		seen[art][x.ID] = true
		a.RulingCount++
		if len(a.Rulings) < maxRulings {
			a.Rulings = append(a.Rulings, x)
		}
	}
	return rows.Err()
}
