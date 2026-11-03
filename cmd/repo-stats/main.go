// Command repo-stats reads the public counters of the project's GitHub
// repositories and Hugging Face datasets and sends them to PostHog, once a
// day, from the stats workflow. Pages that run no JavaScript (a README, a
// dataset card) count their views through the site's badges; this adds what
// only the two platforms know: stars, forks, GitHub's views and clones per
// day, referrers, and Hugging Face downloads and likes.
//
//	POSTHOG_KEY=phc_... GITHUB_TOKEN=... repo-stats
//	repo-stats -dry-run    # print the events instead of sending them
//
// GitHub's traffic numbers need a token with read access to the repository's
// administration; without one only the public counters are sent. Each
// traffic day is sent with a fixed uuid, so the 14 days GitHub returns can be
// sent again every day without counting twice.
package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type event struct {
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Timestamp  string         `json:"timestamp,omitempty"`
	UUID       string         `json:"uuid,omitempty"`
	Properties map[string]any `json:"properties"`
}

type client struct {
	http    *http.Client
	github  string
	hf      string
	ghToken string
}

func (c *client) getJSON(ctx context.Context, url, token string, v any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "lebanese-legislation-repo-stats (+https://github.com/taktekhq/lebanese-legislation)")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasPrefix(url, c.github) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode != http.StatusOK {
		return res.StatusCode, fmt.Errorf("%s answered %d", url, res.StatusCode)
	}
	return res.StatusCode, json.Unmarshal(body, v)
}

// stableUUID turns a key into a UUID-shaped id, the same every run.
func stableUUID(key string) string {
	h := sha1.Sum([]byte(key))
	h[6] = h[6]&0x0f | 0x50
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func (c *client) gitHub(ctx context.Context, repo string, now time.Time) ([]event, error) {
	var r struct {
		Stars    int    `json:"stargazers_count"`
		Forks    int    `json:"forks_count"`
		Watchers int    `json:"subscribers_count"`
		Issues   int    `json:"open_issues_count"`
		Pushed   string `json:"pushed_at"`
	}
	if _, err := c.getJSON(ctx, c.github+"/repos/"+repo, c.ghToken, &r); err != nil {
		return nil, err
	}
	distinct := "github:" + repo
	out := []event{{Event: "github_repo_stats", DistinctID: distinct, Timestamp: now.Format(time.RFC3339), Properties: map[string]any{
		"repo": repo, "stars": r.Stars, "forks": r.Forks, "watchers": r.Watchers, "open_issues": r.Issues, "pushed_at": r.Pushed,
	}}}
	if c.ghToken == "" {
		return out, nil
	}
	type day struct {
		Timestamp string `json:"timestamp"`
		Count     int    `json:"count"`
		Uniques   int    `json:"uniques"`
	}
	var views struct {
		Views []day `json:"views"`
	}
	var clones struct {
		Clones []day `json:"clones"`
	}
	if code, err := c.getJSON(ctx, c.github+"/repos/"+repo+"/traffic/views", c.ghToken, &views); err != nil {
		// 403: the token has no administration read on this repository (yet).
		log.Printf("%s traffic: %v (only public counters sent)", repo, err)
		if code == http.StatusForbidden {
			return out, nil
		}
		return nil, err
	}
	if _, err := c.getJSON(ctx, c.github+"/repos/"+repo+"/traffic/clones", c.ghToken, &clones); err != nil {
		return nil, err
	}
	days := map[string]map[string]any{}
	for _, d := range views.Views {
		days[d.Timestamp] = map[string]any{"repo": repo, "views": d.Count, "unique_visitors": d.Uniques, "clones": 0, "unique_cloners": 0}
	}
	for _, d := range clones.Clones {
		p := days[d.Timestamp]
		if p == nil {
			p = map[string]any{"repo": repo, "views": 0, "unique_visitors": 0}
			days[d.Timestamp] = p
		}
		p["clones"], p["unique_cloners"] = d.Count, d.Uniques
	}
	today := now.UTC().Format("2006-01-02")
	for ts, p := range days {
		// Today is still counting; send only finished days.
		if strings.HasPrefix(ts, today) {
			continue
		}
		p["day"] = ts[:10]
		out = append(out, event{Event: "github_traffic_day", DistinctID: distinct, Timestamp: ts, UUID: stableUUID("github_traffic_day/" + repo + "/" + ts), Properties: p})
	}
	var refs []struct {
		Referrer string `json:"referrer"`
		Count    int    `json:"count"`
		Uniques  int    `json:"uniques"`
	}
	if _, err := c.getJSON(ctx, c.github+"/repos/"+repo+"/traffic/popular/referrers", c.ghToken, &refs); err != nil {
		return nil, err
	}
	for _, r := range refs {
		out = append(out, event{Event: "github_referrer_14d", DistinctID: distinct, Timestamp: now.Format(time.RFC3339), Properties: map[string]any{
			"repo": repo, "referrer": r.Referrer, "views": r.Count, "unique_visitors": r.Uniques,
		}})
	}
	return out, nil
}

func (c *client) huggingFace(ctx context.Context, repo string, now time.Time) (event, error) {
	kind, id, _ := strings.Cut(repo, "/")
	var v struct {
		Downloads        float64 `json:"downloads"`
		DownloadsAllTime float64 `json:"downloadsAllTime"`
		Likes            float64 `json:"likes"`
		LastModified     string  `json:"lastModified"`
	}
	expand := "expand[]=downloads&expand[]=downloadsAllTime&expand[]=likes&expand[]=lastModified"
	if kind == "spaces" {
		expand = "expand[]=likes&expand[]=lastModified" // Spaces have no download counter
	}
	_, err := c.getJSON(ctx, c.hf+"/"+kind+"/"+id+"?"+expand, "", &v)
	// The same event the old MCP Worker sent, so the PostHog insights continue.
	return event{Event: "hf_dataset_stats", DistinctID: "lebaneselegislation-hf-stats", Timestamp: now.Format(time.RFC3339), Properties: map[string]any{
		"dataset": id, "kind": kind, "downloads_30d": v.Downloads, "downloads_all_time": v.DownloadsAllTime, "likes": v.Likes, "last_modified": v.LastModified,
	}}, err
}

func send(ctx context.Context, hc *http.Client, host, key string, events []event) error {
	body, err := json.Marshal(map[string]any{"api_key": key, "batch": events})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(host, "/")+"/batch/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("posthog answered %d: %s", res.StatusCode, b)
	}
	return nil
}

func main() {
	log.SetFlags(0)
	repos := flag.String("github", "taktekhq/lebanese-legislation,taktekhq/lebanese-legislation-archive", "GitHub repositories, comma-separated")
	hfRepos := flag.String("huggingface", "datasets/taktekhq/lebanese-legislation,datasets/taktekhq/lebanese-legislation-archive,datasets/taktekhq/lebanese-court-scans,spaces/taktekhq/lebanese-legal-handwriting", "Hugging Face repositories as <kind>/<owner>/<name>, comma-separated")
	host := flag.String("posthog-host", envOr("POSTHOG_HOST", "https://eu.i.posthog.com"), "PostHog ingestion host")
	dry := flag.Bool("dry-run", false, "print the events as JSON instead of sending them")
	githubAPI := flag.String("github-api", "https://api.github.com", "GitHub API base")
	hfAPI := flag.String("huggingface-api", "https://huggingface.co/api", "Hugging Face API base")
	flag.Parse()
	key := os.Getenv("POSTHOG_KEY")
	if key == "" && !*dry {
		log.Fatal("repo-stats: set POSTHOG_KEY, or pass -dry-run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := &client{http: &http.Client{Timeout: 30 * time.Second}, github: strings.TrimRight(*githubAPI, "/"), hf: strings.TrimRight(*hfAPI, "/"), ghToken: os.Getenv("GITHUB_TOKEN")}
	now := time.Now().UTC().Truncate(time.Second)
	var events []event
	failed := 0
	for _, r := range split(*repos) {
		ev, err := c.gitHub(ctx, r, now)
		if err != nil {
			log.Printf("github %s: %v", r, err)
			failed++
		}
		events = append(events, ev...)
	}
	for _, r := range split(*hfRepos) {
		ev, err := c.huggingFace(ctx, r, now)
		if err != nil {
			log.Printf("hugging face %s: %v", r, err)
			failed++
			continue
		}
		events = append(events, ev)
	}
	if *dry {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", " ")
		if err := enc.Encode(events); err != nil {
			log.Fatal(err)
		}
	} else if len(events) > 0 {
		if err := send(ctx, c.http, *host, key, events); err != nil {
			log.Fatalf("repo-stats: %v", err)
		}
		log.Printf("repo-stats: sent %d events", len(events))
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
