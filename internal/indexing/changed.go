package indexing

import "time"

// FirstFetch is the day the scraper first fetched the Centre's pages: no page
// here existed, so none changed, before it.
var FirstFetch = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

// ChangedOn is the day a page last changed (page_state.changed_at), as
// 2006-01-02, or "" when t cannot be that day: unknown, before the page
// existed here (earlier builds seeded the text's own date, and a 1964 decree's
// page did not change in 1964) or after now. The sitemaps' lastmod, the
// pages' "Last updated" line, their JSON-LD dateModified and the API's
// updated_at all use it, so they always agree.
func ChangedOn(t, now time.Time) string {
	if t.IsZero() || t.Before(FirstFetch) {
		return ""
	}
	d := t.UTC().Format("2006-01-02")
	if d > now.UTC().Format("2006-01-02") {
		return ""
	}
	return d
}
