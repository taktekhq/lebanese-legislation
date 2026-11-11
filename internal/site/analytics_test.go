package site

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The analytics install is the standard one: no consent banner, analytics
// always on once the server turns them on (SetAnalytics). On 30 Oct a
// consent banner and then a hand-written sender replaced both SDKs: GA4 saw
// one page title for the whole site and a "source" parameter that turned
// ChatGPT visits into "direct", and PostHog's page views stopped coming from
// posthog-js, so the website-silent alarm (it counts $lib = 'web') fired
// while people were reading the site.
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

	scripts := []string{`src="/ga.js?v=`, `src="/assets/load.js?v=`, `data-ph="/ph.js?v=`}
	// Off by default: a copy hosted elsewhere sends nothing to our projects.
	page := get("/blog/")
	for _, script := range scripts {
		if strings.Contains(page, script) {
			t.Errorf("analytics are off, yet the page loads %s", script)
		}
	}
	s.SetAnalytics(true)
	page = get("/blog/")
	for _, want := range scripts {
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
	// One $web_vitals event a page instead of two: wait a minute for the late
	// metrics (CLS, INP) before sending. An object with only this key leaves
	// web vitals, their four metrics and network timing as the project sets
	// them; "web_vitals: false" or an allow list here would hide a metric.
	if !strings.Contains(ph, "capture_performance: { web_vitals_delayed_flush_ms: 60000 }") {
		t.Error("ph.js must delay the web vitals flush to 60 s and set nothing else in capture_performance")
	}
	// A survey is shown in the page's language. Without this posthog-js picks the
	// browser's, and an Arabic page in an English browser gets English text set
	// right to left.
	if !strings.Contains(ph, `override_display_language: document.documentElement.lang || "ar"`) {
		t.Error("ph.js must show surveys in the page's language")
	}
	// PostHog's snippet adopts an array; "|| {}" throws on the first capture.
	if !strings.Contains(ph, "window.posthog||[]") {
		t.Error("ph.js must end PostHog's snippet with window.posthog||[]")
	}
	// A paid editor's sessions are all recorded and identified; long pages are
	// not recorded for anyone else (5 Nov 2026).
	for _, want := range []string{`localStorage.getItem("lebleg_editor")`, `posthog.identify("editor:" + editor`, `posthog.startSessionRecording(true)`, `disable_session_recording: long && !editor`} {
		if !strings.Contains(ph, want) {
			t.Errorf("ph.js must contain %s", want)
		}
	}

	load := get("/assets/load.js")
	if !strings.Contains(load, "/gtag/js?id=G-RDYRK422NP") || !strings.Contains(load, "data-ph") {
		t.Error("assets/load.js must fetch gtag.js and the PostHog script")
	}
}
