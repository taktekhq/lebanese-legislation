package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubTrafficDays(t *testing.T) {
	forbid := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/traffic/") && forbid {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/traffic/views"):
			w.Write([]byte(`{"views":[{"timestamp":"2026-11-01T00:00:00Z","count":94,"uniques":9},{"timestamp":"2026-11-03T00:00:00Z","count":5,"uniques":1}]}`))
		case strings.HasSuffix(r.URL.Path, "/traffic/clones"):
			w.Write([]byte(`{"clones":[{"timestamp":"2026-11-01T00:00:00Z","count":551,"uniques":40},{"timestamp":"2026-11-02T00:00:00Z","count":7,"uniques":2}]}`))
		case strings.HasSuffix(r.URL.Path, "/popular/referrers"):
			w.Write([]byte(`[{"referrer":"github.com","count":18,"uniques":4}]`))
		default:
			w.Write([]byte(`{"stargazers_count":1,"forks_count":0,"subscribers_count":2,"open_issues_count":3}`))
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	c := &client{http: srv.Client(), github: srv.URL, ghToken: "t"}
	ev, err := c.gitHub(context.Background(), "o/r", now)
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]map[string]any{}
	for _, e := range ev {
		if e.Event == "github_traffic_day" {
			days[e.Properties["day"].(string)] = e.Properties
			if e.UUID != stableUUID("github_traffic_day/o/r/"+e.Timestamp) {
				t.Errorf("uuid not stable: %s", e.UUID)
			}
		}
	}
	if len(days) != 2 || days["2026-11-01"]["views"] != 94 || days["2026-11-01"]["clones"] != 551 || days["2026-11-02"]["clones"] != 7 {
		t.Errorf("days (today left out): %v", days)
	}
	if ev[0].Event != "github_repo_stats" || ev[0].Properties["stars"] != 1 || ev[len(ev)-1].Event != "github_referrer_14d" {
		t.Errorf("events: %v", ev)
	}
	forbid = true
	ev, err = c.gitHub(context.Background(), "o/r", now)
	if err != nil || len(ev) != 1 {
		t.Errorf("without traffic access only the counters: %v %v", ev, err)
	}
}

func TestStableUUID(t *testing.T) {
	a, b := stableUUID("x"), stableUUID("x")
	if a != b || len(a) != 36 || a[14] != '5' || stableUUID("y") == a {
		t.Errorf("%s %s", a, b)
	}
}
