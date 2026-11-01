package site

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The analytics install is the standard one (no consent banner, analytics
// always on, when the site turns analytics on). On 30 Oct a consent banner and then a
// hand-written sender replaced both SDKs: GA4 saw one page title for the whole
// site and a "source" parameter that turned ChatGPT visits into "direct", and
// PostHog's page views stopped coming from posthog-js, so the website-silent
// alarm (it counts $lib = 'web') fired while people were reading the site.
// scripts/analytics-smoke.cjs checks the same in a real browser.
func TestAnalyticsStandardInstall(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s: status %d", path, w.Code)
		}
		return w.Body.String()
	}

	page := get("/blog/")
	for _, want := range []string{`src="/ga.js?v=`, `src="/assets/load.js?v=`, `data-ph="/ph.js?v=`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not load %s", want)
		}
	}

	ga := get("/ga.js")
	if !strings.Contains(ga, `gtag("config", "G-RDYRK422NP");`) {
		t.Error(`ga.js must configure GA4 with the plain gtag("config", "G-RDYRK422NP")`)
	}
	// Overriding these replaces GA4's own page title, address, referrer and
	// traffic source with ours; consent and opt-out switches turn it off.
	for _, banned := range []string{"page_title", "page_location", "page_referrer", "send_page_view", `"consent"`, "ga-disable", "globalPrivacyControl", "doNotTrack"} {
		if strings.Contains(ga, banned) {
			t.Errorf("ga.js contains %q", banned)
		}
	}
	if m := regexp.MustCompile(`\b(source|medium|campaign|term|content)\s*:`).FindString(ga); m != "" {
		t.Errorf("ga.js sends an event parameter GA4 reads as the traffic source: %q", m)
	}

	ph := get("/ph.js")
	for _, want := range []string{`api_host: "/ph"`, "capture_pageview: true", "capture_pageleave: true", `person_profiles: "identified_only"`} {
		if !strings.Contains(ph, "posthog.init(") || !strings.Contains(ph, want) {
			t.Errorf("ph.js must call posthog.init with %s", want)
		}
	}
	// PostHog's snippet adopts an array; "|| {}" throws on the first capture.
	if !strings.Contains(ph, "window.posthog||[]") {
		t.Error("ph.js must end PostHog's snippet with window.posthog||[]")
	}

	load := get("/assets/load.js")
	if !strings.Contains(load, "https://www.googletagmanager.com/gtag/js?id=G-RDYRK422NP") || !strings.Contains(load, "data-ph") {
		t.Error("assets/load.js must fetch gtag.js and the PostHog script")
	}
}
