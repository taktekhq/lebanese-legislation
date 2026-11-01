// Package hfhub is a small client for the Hugging Face Hub HTTP API: repository
// heads, file listings, downloads and commits, including large files through
// the Git LFS batch API (basic and multipart uploads). It depends only on the
// standard library, so it can move to its own repository unchanged.
package hfhub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the public Hub.
const DefaultEndpoint = "https://huggingface.co"

// RepoType is "model", "dataset" or "space".
type RepoType string

const (
	Model   RepoType = "model"
	Dataset RepoType = "dataset"
	Space   RepoType = "space"
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidRepoID reports whether id looks like "owner/name".
func ValidRepoID(id string) bool { return repoPattern.MatchString(id) }

// Client talks to one Hub endpoint. The zero value is not usable: use New.
type Client struct {
	Endpoint string       // e.g. DefaultEndpoint, without a trailing slash
	Token    string       // empty for anonymous access
	HTTP     *http.Client // used for every request
	// Retries is how many times an idempotent request (GET, HEAD, an LFS part
	// PUT) is retried after a network error, 429 or 5xx. Commits are never retried.
	Retries int
	// Backoff is the first wait between retries; it doubles each time.
	Backoff time.Duration
}

// New returns a client for endpoint (DefaultEndpoint when empty) with token
// (empty for anonymous).
func New(endpoint, token string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{Endpoint: strings.TrimRight(endpoint, "/"), Token: token,
		HTTP: &http.Client{Timeout: 0}, Retries: 3, Backoff: 2 * time.Second}
}

// Error is a Hub response with an unexpected status. It never holds the
// token; Body is truncated.
type Error struct {
	Method, URL string
	Status      int
	Body        string
}

func (e *Error) Error() string {
	return fmt.Sprintf("hub: %s %s: HTTP %d %s", e.Method, e.URL, e.Status, e.Body)
}

// IsNotFound reports whether err is a 404 from the Hub.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) api(t RepoType, repo string, parts ...string) string {
	u := c.Endpoint + "/api/" + string(t) + "s/" + repo
	for _, p := range parts {
		u += "/" + p
	}
	return u
}

// ResolveURL is where a file's content is served at revision.
func (c *Client) ResolveURL(t RepoType, repo, revision, path string) string {
	prefix := ""
	if t != Model {
		prefix = "/" + string(t) + "s"
	}
	return c.Endpoint + prefix + "/" + repo + "/resolve/" + url.PathEscape(revision) + "/" + escapePath(path)
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (c *Client) newRequest(ctx context.Context, method, u string, body io.Reader, auth bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if auth && c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("User-Agent", "hfhub-go/1")
	return req, nil
}

func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// do sends a request with the token, retrying idempotent requests: getBody
// returns a fresh body for each attempt. The caller closes the response body.
func (c *Client) do(ctx context.Context, method, u string, header http.Header, getBody func() io.Reader, idempotent bool) (*http.Response, error) {
	return c.send(ctx, method, u, header, getBody, idempotent, true)
}

// send is do, with the token only when auth is set: presigned storage URLs
// must not receive it.
func (c *Client) send(ctx context.Context, method, u string, header http.Header, getBody func() io.Reader, idempotent, auth bool) (*http.Response, error) {
	attempts := 1
	if idempotent {
		attempts += max(c.Retries, 0)
	}
	wait := c.Backoff
	var last error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}
		var body io.Reader
		if getBody != nil {
			body = getBody()
		}
		req, err := c.newRequest(ctx, method, u, body, auth)
		if err != nil {
			return nil, err
		}
		for k, v := range header {
			if k == "Content-Length" {
				// Go sends ContentLength, not the header; without it a
				// streamed body goes out chunked, which storage refuses.
				req.ContentLength, _ = strconv.ParseInt(v[0], 10, 64)
				continue
			}
			req.Header[k] = v
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = err
			continue
		}
		if retryable(res.StatusCode) && i+1 < attempts {
			_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
			_ = res.Body.Close()
			last = &Error{Method: method, URL: redact(u), Status: res.StatusCode}
			continue
		}
		return res, nil
	}
	return nil, last
}

// redact drops the query string, which may carry signed parameters.
func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func check(res *http.Response, method, u string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	return &Error{Method: method, URL: redact(u), Status: res.StatusCode, Body: strings.TrimSpace(string(b))}
}

func (c *Client) getJSON(ctx context.Context, u string, out any) (http.Header, error) {
	res, err := c.do(ctx, http.MethodGet, u, nil, nil, true)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if err := check(res, http.MethodGet, u); err != nil {
		return nil, err
	}
	return res.Header, json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(out)
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Revision resolves a branch, tag or commit to its 40-hex commit.
func (c *Client) Revision(ctx context.Context, t RepoType, repo, revision string) (string, error) {
	if !ValidRepoID(repo) {
		return "", errors.New("hub: invalid repository id")
	}
	if shaPattern.MatchString(revision) {
		return revision, nil
	}
	var info struct {
		SHA string `json:"sha"`
	}
	if _, err := c.getJSON(ctx, c.api(t, repo, "revision", url.PathEscape(revision)), &info); err != nil {
		return "", err
	}
	if !shaPattern.MatchString(info.SHA) {
		return "", errors.New("hub: revision did not resolve to a commit")
	}
	return info.SHA, nil
}

// LFSInfo describes a file stored with Git LFS.
type LFSInfo struct {
	SHA256 string `json:"oid"`
	Size   int64  `json:"size"`
}

// Entry is one file or directory in a listing.
type Entry struct {
	Type string   `json:"type"` // "file" or "directory"
	Path string   `json:"path"`
	Size int64    `json:"size"`
	OID  string   `json:"oid"`
	LFS  *LFSInfo `json:"lfs,omitempty"`
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// List returns every file under dir ("" for the root) at revision, recursively.
func (c *Client) List(ctx context.Context, t RepoType, repo, revision, dir string) ([]Entry, error) {
	u := c.api(t, repo, "tree", url.PathEscape(revision))
	if dir != "" {
		u += "/" + escapePath(dir)
	}
	u += "?recursive=true&expand=false"
	var out []Entry
	for u != "" {
		var page []Entry
		h, err := c.getJSON(ctx, u, &page)
		if err != nil {
			return nil, err
		}
		for _, e := range page {
			if e.Type == "file" {
				out = append(out, e)
			}
		}
		u = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			u = m[1]
		}
	}
	return out, nil
}

// PathsInfo returns what the Hub knows about the given paths at revision.
// Paths that do not exist are left out.
func (c *Client) PathsInfo(ctx context.Context, t RepoType, repo, revision string, paths []string) ([]Entry, error) {
	form := url.Values{"expand": {"false"}}
	for _, p := range paths {
		form.Add("paths", p)
	}
	u := c.api(t, repo, "paths-info", url.PathEscape(revision))
	enc := form.Encode()
	res, err := c.do(ctx, http.MethodPost, u, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		func() io.Reader { return strings.NewReader(enc) }, true)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if err := check(res, http.MethodPost, u); err != nil {
		return nil, err
	}
	var out []Entry
	return out, json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out)
}

// Open streams a file's content at revision. rangeHeader, when set, is sent
// as the Range header (e.g. "bytes=0-65535"). The caller closes the body.
func (c *Client) Open(ctx context.Context, t RepoType, repo, revision, path, rangeHeader string) (*http.Response, error) {
	u := c.ResolveURL(t, repo, revision, path)
	h := http.Header{}
	if rangeHeader != "" {
		h.Set("Range", rangeHeader)
	}
	res, err := c.do(ctx, http.MethodGet, u, h, nil, true)
	if err != nil {
		return nil, err
	}
	if err := check(res, http.MethodGet, u); err != nil {
		res.Body.Close()
		return nil, err
	}
	return res, nil
}

// Read returns a whole file of at most limit bytes.
func (c *Client) Read(ctx context.Context, t RepoType, repo, revision, path string, limit int64) ([]byte, error) {
	res, err := c.Open(ctx, t, repo, revision, path, "")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("hub: %s is larger than %d bytes", path, limit)
	}
	return b, nil
}

func jsonBody(v any) (func() io.Reader, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return func() io.Reader { return bytes.NewReader(b) }, nil
}
