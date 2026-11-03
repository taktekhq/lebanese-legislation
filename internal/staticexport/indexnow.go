package staticexport

// IndexNow tells Bing (and through api.indexnow.org, Yandex, Naver and Seznam)
// which pages changed, so they recrawl them instead of waiting for the
// sitemaps. Bing's index is what ChatGPT and Copilot search, so a changed law
// page reaches their answers sooner. Protocol: https://www.indexnow.org/documentation.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IndexNowBatch is the most URLs the protocol accepts in one request.
const IndexNowBatch = 10000

// IndexNow submits URLs of one host.
type IndexNow struct {
	Endpoint string // https://api.indexnow.org/indexnow
	Host     string // lebaneselegislation.com
	Key      string // served as https://<Host>/<Key>.txt
	Client   *http.Client
	Batch    int                 // URLs per request; 0 means IndexNowBatch
	Wait     func(time.Duration) // between retries; nil means time.Sleep
}

// IndexNowResult is one request.
type IndexNowResult struct {
	URLs   int    `json:"urls"`
	Status int    `json:"status"`
	Body   string `json:"body,omitempty"`
	Tries  int    `json:"tries"`
}

// Submit sends urls in batches. It stops at the first batch the endpoint
// refuses (403 wrong key, 422 URL of another host, 429 after retries), since
// the next batch would get the same answer.
func (n *IndexNow) Submit(ctx context.Context, urls []string) ([]IndexNowResult, error) {
	size := n.Batch
	if size <= 0 || size > IndexNowBatch {
		size = IndexNowBatch
	}
	var out []IndexNowResult
	for i := 0; i < len(urls); i += size {
		batch := urls[i:min(i+size, len(urls))]
		r, err := n.post(ctx, batch)
		out = append(out, r)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (n *IndexNow) post(ctx context.Context, batch []string) (IndexNowResult, error) {
	body, err := json.Marshal(map[string]any{
		"host":        n.Host,
		"key":         n.Key,
		"keyLocation": "https://" + n.Host + "/" + n.Key + ".txt",
		"urlList":     batch,
	})
	if err != nil {
		return IndexNowResult{}, err
	}
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	wait := n.Wait
	if wait == nil {
		wait = time.Sleep
	}
	r := IndexNowResult{URLs: len(batch)}
	for delay := 30 * time.Second; ; delay *= 2 {
		r.Tries++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.Endpoint, bytes.NewReader(body))
		if err != nil {
			return r, err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		resp, err := client.Do(req)
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			r.Status, r.Body = resp.StatusCode, strings.TrimSpace(string(b))
			// 200 OK, 202 accepted while the key file is checked.
			if r.Status == http.StatusOK || r.Status == http.StatusAccepted {
				return r, nil
			}
			if r.Status != http.StatusTooManyRequests && r.Status < 500 {
				return r, fmt.Errorf("indexnow: status %d: %s", r.Status, r.Body)
			}
		}
		if r.Tries == 4 {
			if err != nil {
				return r, fmt.Errorf("indexnow: %w", err)
			}
			return r, fmt.Errorf("indexnow: status %d after %d tries: %s", r.Status, r.Tries, r.Body)
		}
		wait(delay)
	}
}

// IndexNowURLs turns site paths into the absolute URLs to submit: changed
// pages that a sitemap lists (so noindex pages and pages with another
// canonical stay out), then removed pages, which now answer 404 or redirect.
// A path is sent once, at most max URLs in all (0 means no limit).
func IndexNowURLs(base string, changed, removed []string, inSitemap map[string]bool, max int) []string {
	base = strings.TrimRight(base, "/")
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || !strings.HasPrefix(p, "/") || (max > 0 && len(out) >= max) {
			return
		}
		u := base + p
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, p := range changed {
		if inSitemap[base+strings.TrimSpace(p)] {
			add(p)
		}
	}
	for _, p := range removed {
		add(p)
	}
	return out
}

// SitemapURLs reads a sitemap index and every sitemap it lists, in order,
// and returns the page URLs. get fetches one URL.
func SitemapURLs(ctx context.Context, index string, get func(context.Context, string) ([]byte, error)) ([]string, error) {
	b, err := get(ctx, index)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, loc := range Locs(b) {
		if !strings.HasSuffix(loc, ".xml") {
			out = append(out, loc)
			continue
		}
		sb, err := get(ctx, loc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", loc, err)
		}
		out = append(out, Locs(sb)...)
	}
	return out, nil
}

// HostOf is the host of an absolute URL, for IndexNow.Host.
func HostOf(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("not an absolute URL: %q", base)
	}
	return u.Host, nil
}
