package site

import (
	"context"
	"strings"
	"sync"
	"time"
)

// titleEnNote is what a title_en field means; the API root and the developer
// docs repeat it. The Arabic title is the official one.
const titleEnNote = "title_en is an unofficial English rendering of the Arabic title, written by a model (Claude Sonnet) from the Arabic title alone. The Arabic title and text are official and more accurate; the English only helps people and search engines find a text. The text itself is never translated."

// titlesEn holds legislation_title_en in memory for the JSON API: a search
// result lists up to 50 texts and each needs its English title, and one map
// lookup beats one query per row. It is reloaded when an hour old, so a
// nightly data load shows within the hour. An older database without the
// table gives an empty map: title_en is simply absent.
type titlesEn struct {
	mu   sync.Mutex
	at   time.Time
	byID map[int]string
}

func (t *titlesEn) get(ctx context.Context, s *Server, id int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byID == nil || time.Since(t.at) > time.Hour {
		m := map[int]string{}
		if s.db != nil {
			if rows, err := s.db.Query(ctx, `SELECT law_id, title_en FROM legislation_title_en`); err == nil {
				for rows.Next() {
					var id int
					var te string
					if rows.Scan(&id, &te) == nil && strings.TrimSpace(te) != "" {
						m[id] = te
					}
				}
				rows.Close()
				if rows.Err() == nil {
					t.byID, t.at = m, time.Now()
				}
			}
		}
		if t.byID == nil { // not loaded (no table, or the request ended): none this time, ask again next time
			return ""
		}
	}
	return t.byID[id]
}

// lawSum is lawSummary with the unofficial English title added.
func (s *Server) lawSum(ctx context.Context, l LawRow, sourceURL string) apiLawSummary {
	out := lawSummary(l, sourceURL)
	out.UpdatedAt = s.listedChange(ctx, "law", l.ID)
	if te := s.titles.get(ctx, s, l.ID); te != "" {
		out.TitleEn = &te
	}
	return out
}
