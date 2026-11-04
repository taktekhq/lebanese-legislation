package site

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/indexing"
)

// When a page's content last changed: page_state.changed_at, which the build
// moves only when what the page shows changes (an amendment coming into force,
// a ruling's reading, a fix at the Centre, our own corrections; see
// internal/build/indexing.go). It feeds the visible "Last updated" line, the
// JSON-LD dateModified and the API's updated_at. The text's own date never
// does, and a page without a usable page_state row shows no date at all
// (indexing.ChangedOn says which dates are usable, as for the sitemaps).

// changedOn is when page kind/id last changed, or nil when it is not known. An
// older database without page_state answers nil too.
func (s *Server) changedOn(ctx context.Context, kind string, id int) *time.Time {
	if s.db == nil {
		return nil
	}
	var t time.Time
	if err := s.db.QueryRow(ctx, `SELECT changed_at FROM page_state WHERE kind = $1 AND id = $2`, kind, id).Scan(&t); err != nil {
		return nil
	}
	return usableChange(t)
}

// latestChange is when the last of the texts a hub lists changed: the latest
// page_state date of the legislation l rows that cond (with args) selects.
func (s *Server) latestChange(ctx context.Context, cond string, args ...any) *time.Time {
	if s.db == nil {
		return nil
	}
	var t *time.Time
	if err := s.db.QueryRow(ctx, `SELECT max(ps.changed_at) FROM legislation l
		JOIN page_state ps ON ps.kind = 'law' AND ps.id = l.id WHERE `+cond, args...).Scan(&t); err != nil || t == nil {
		return nil
	}
	return usableChange(*t)
}

// usableChange is t when indexing.ChangedOn accepts it, else nil.
func usableChange(t time.Time) *time.Time {
	if indexing.ChangedOn(t, time.Now()) == "" {
		return nil
	}
	return &t
}

// changedISO is t as the API's updated_at and JSON-LD's dateModified
// (2026-11-02), nil when t is nil.
func changedISO(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := indexing.ChangedOn(*t, time.Now())
	if v == "" {
		return nil
	}
	return &v
}

// setModified adds dateModified to a JSON-LD object when the page's change
// date is known.
func setModified(ld map[string]any, t *time.Time) {
	if d := changedISO(t); d != nil {
		ld["dateModified"] = *d
	}
}

// longDate prints a day with the month's name: "2 November 2026", or
// "2 تشرين الثاني 2026" on the Arabic pages.
func longDate(lang string, t time.Time) string {
	months := enMonths
	if lang == "ar" {
		months = arMonths
	}
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

// pageUpdated builds a page's "Last updated" line from its change date, with
// the text that last amended it when there is one (by); nil when the date is
// not known.
func (s *Server) pageUpdated(r *http.Request, t *time.Time, by *LawRow) *Updated {
	iso := changedISO(t)
	if iso == nil {
		return nil
	}
	b := s.bundle(r)
	day := longDate(b.Lang, *t)
	return &Updated{Label: b.T("seo.updated", day), ISO: *iso, Date: day, By: by}
}

// updatedFact is the "Last updated" row of a document's facts list.
func (s *Server) updatedFact(r *http.Request, u *Updated) [2]any {
	if u == nil {
		return [2]any{"", ""}
	}
	return [2]any{s.tr(r, "page.updated"), template.HTML(`<time datetime="` + u.ISO + `">` + html.EscapeString(u.Date) + `</time>`)}
}

// lastAmendment is the text whose change to l came into force last, by today:
// the reason a text's page most likely changed. Only applied changes count,
// from the amendment table and from the links of every article.
func (l *Law) lastAmendment() *LawRow {
	var by *LawRow
	var at time.Time
	now := time.Now()
	consider := func(row LawRow, on *time.Time) {
		if on == nil || on.After(now) || (by != nil && !on.After(at)) || row.ID == l.ID {
			return
		}
		r := row
		by, at = &r, *on
	}
	for _, c := range l.Changes {
		if c.Status == "applied" || c.Status == "partial" {
			consider(c.By, c.Effective)
		}
	}
	for _, a := range l.List {
		for _, k := range a.Links {
			consider(k.By, k.Effective)
		}
	}
	return by
}

// lastArticleAmendment is the text whose change to article a came into force
// last, by today; nil when it was never amended.
func lastArticleAmendment(a *Article) *LawRow {
	var by *LawRow
	var at time.Time
	now := time.Now()
	for _, k := range a.Links {
		if k.Effective == nil || k.Effective.After(now) || (by != nil && !k.Effective.After(at)) {
			continue
		}
		row := k.By
		by, at = &row, *k.Effective
	}
	return by
}

// pageDates holds the usable page_state dates in memory for the JSON API: a
// search or a list names up to 50 texts, rulings or issues, and each carries
// its updated_at; one map lookup beats one query per row. Like titlesEn it is
// reloaded when an hour old, so a nightly load shows within the hour (a
// single text, ruling or issue asked for by id reads page_state directly).
// An older database without the table gives no dates.
type pageDates struct {
	mu   sync.Mutex
	at   time.Time
	days map[pageKey]int32 // days since 1970-01-01
}

type pageKey struct {
	kind string
	id   int32
}

// listedChange is when page kind/id last changed, as updated_at, from the
// hourly copy; nil when not known.
func (s *Server) listedChange(ctx context.Context, kind string, id int) *string {
	d := &s.pageDates
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.days == nil || time.Since(d.at) > time.Hour {
		if s.db == nil {
			return nil
		}
		rows, err := s.db.Query(ctx, `SELECT kind, id, changed_at FROM page_state WHERE changed_at >= $1`, indexing.FirstFetch)
		if err != nil {
			return nil
		}
		m := map[pageKey]int32{}
		kinds := map[string]string{} // one copy of each kind's name
		for rows.Next() {
			var k string
			var i int32
			var t time.Time
			if rows.Scan(&k, &i, &t) != nil {
				continue
			}
			if kinds[k] == "" {
				kinds[k] = k
			}
			m[pageKey{kinds[k], i}] = int32(t.Unix() / 86400)
		}
		rows.Close()
		if rows.Err() != nil {
			return nil // the request ended: none this time, ask again next time
		}
		d.days, d.at = m, time.Now()
	}
	v, ok := d.days[pageKey{kind, int32(id)}]
	if !ok {
		return nil
	}
	t := time.Unix(int64(v)*86400, 0).UTC()
	return changedISO(&t)
}
