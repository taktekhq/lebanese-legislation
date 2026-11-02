package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The pages in R2 are what visitors read since the 2 Nov cutover, so they
// must load the same analytics as the live server (internal/app): GA4 and
// PostHog on unless ANALYTICS=off. The first exports left them out, and
// PostHog page views fell from about 50 an hour to under 10.
func TestExportedPagesLoadAnalytics(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{{"", true}, {"on", true}, {"off", false}} {
		srv, err := newSite(nil, func(string) string { return tc.env })
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", "/blog/", nil))
		page := w.Body.String()
		for _, script := range []string{`src="/ga.js?v=`, `src="/assets/load.js?v=`, `data-ph="/ph.js?v=`} {
			if got := strings.Contains(page, script); got != tc.want {
				t.Errorf("ANALYTICS=%q: page has %s = %v, want %v", tc.env, script, got, tc.want)
			}
		}
	}
}
