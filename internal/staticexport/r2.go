package staticexport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// R2 is a bucket on Cloudflare R2, or any S3-compatible store, written with
// AWS Signature Version 4 and path-style requests. The signer is the one in
// github.com/taktekhq/cfkeep (MIT, same authors), kept here so this module
// has no dependency on another repository. It uses no SDK: the exporter needs
// three calls (PUT, GET, DELETE) and a signature.
type R2 struct {
	Endpoint  *url.URL
	Bucket    string
	Region    string // "auto" for R2
	AccessKey string
	SecretKey string
	HTTP      *http.Client
	Now       func() time.Time
	// Delays are the waits before the second, third, ... attempt of a request
	// that failed in a way worth repeating.
	Delays []time.Duration
}

// ErrNotFound is what Get returns for a key that is not there.
var ErrNotFound = errors.New("object not found")

// NewR2 reads the bucket's credentials from the environment: R2_ACCESS_KEY_ID
// and R2_SECRET_ACCESS_KEY. endpoint is https://<account-id>.r2.cloudflarestorage.com.
func NewR2(endpoint, bucket string, getenv func(string) string) (*R2, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("bad R2 endpoint %q (want https://<account-id>.r2.cloudflarestorage.com)", endpoint)
	}
	if bucket == "" {
		return nil, errors.New("no R2 bucket")
	}
	key, secret := getenv("R2_ACCESS_KEY_ID"), getenv("R2_SECRET_ACCESS_KEY")
	if key == "" || secret == "" {
		return nil, errors.New("set R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY (a token scoped to this one bucket)")
	}
	return &R2{Endpoint: u, Bucket: bucket, Region: "auto", AccessKey: key, SecretKey: secret,
		HTTP: &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{
			MaxIdleConns: 256, MaxIdleConnsPerHost: 256, IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: true}},
		Now:    time.Now,
		Delays: []time.Duration{500 * time.Millisecond, 2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second}}, nil
}

// PutOptions are the headers stored with an object.
type PutOptions struct {
	ContentType  string
	CacheControl string
	SHA256       string // hex; sent as the signed payload hash and kept as metadata
}

// Put stores body under key, trying again on a network error, 429 or 5xx.
func (r *R2) Put(ctx context.Context, key string, body []byte, o PutOptions) error {
	if o.SHA256 == "" {
		sum := sha256.Sum256(body)
		o.SHA256 = hex.EncodeToString(sum[:])
	}
	return r.do(ctx, http.MethodPut, key, func() (*http.Request, error) {
		req, err := r.request(ctx, http.MethodPut, key, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.ContentLength = int64(len(body))
		if o.ContentType != "" {
			req.Header.Set("Content-Type", o.ContentType)
		}
		if o.CacheControl != "" {
			req.Header.Set("Cache-Control", o.CacheControl)
		}
		req.Header.Set("X-Amz-Meta-Sha256", o.SHA256)
		r.sign(req, o.SHA256)
		return req, nil
	}, nil)
}

// Get reads an object; ErrNotFound when it is not there.
func (r *R2) Get(ctx context.Context, key string) ([]byte, error) {
	var out []byte
	err := r.do(ctx, http.MethodGet, key, func() (*http.Request, error) {
		req, err := r.request(ctx, http.MethodGet, key, nil)
		if err != nil {
			return nil, err
		}
		r.sign(req, emptySHA)
		return req, nil
	}, func(resp *http.Response) error {
		var err error
		out, err = io.ReadAll(resp.Body)
		return err
	})
	return out, err
}

// Delete removes an object; a key that is already gone is not an error.
func (r *R2) Delete(ctx context.Context, key string) error {
	err := r.do(ctx, http.MethodDelete, key, func() (*http.Request, error) {
		req, err := r.request(ctx, http.MethodDelete, key, nil)
		if err != nil {
			return nil, err
		}
		r.sign(req, emptySHA)
		return req, nil
	}, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func (r *R2) request(ctx context.Context, method, key string, body io.Reader) (*http.Request, error) {
	u := *r.Endpoint
	u.Path = strings.TrimRight(u.Path, "/") + "/" + r.Bucket + "/" + key // a key holds literal "%": URL.Path is the unescaped form
	u.RawPath = ""
	return http.NewRequestWithContext(ctx, method, u.String(), body)
}

func (r *R2) sign(req *http.Request, payloadHash string) {
	signV4(req, payloadHash, r.AccessKey, r.SecretKey, r.Region, "s3", r.Now().UTC())
}

// temporary marks a failure worth another try.
type temporary struct{ err error }

func (t temporary) Error() string { return t.err.Error() }
func (t temporary) Unwrap() error { return t.err }

func (r *R2) do(ctx context.Context, method, key string, build func() (*http.Request, error), read func(*http.Response) error) error {
	for attempt := 0; ; attempt++ {
		err := func() error {
			req, err := build()
			if err != nil {
				return err
			}
			resp, err := r.HTTP.Do(req)
			if err != nil {
				return temporary{err}
			}
			defer resp.Body.Close()
			switch {
			case resp.StatusCode/100 == 2:
				if read != nil {
					return read(resp)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			case resp.StatusCode == http.StatusNotFound:
				return ErrNotFound
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			e := fmt.Errorf("%s %s: HTTP %d: %s", method, key, resp.StatusCode, strings.TrimSpace(string(b)))
			if resp.StatusCode == 429 || resp.StatusCode >= 500 {
				if s := resp.Header.Get("Retry-After"); s != "" {
					if n, err := strconv.Atoi(s); err == nil && n > 0 && n < 120 {
						time.Sleep(time.Duration(n) * time.Second)
					}
				}
				return temporary{e}
			}
			return e
		}()
		var t temporary
		if err == nil || !errors.As(err, &t) || attempt >= len(r.Delays) {
			return err
		}
		d := r.Delays[attempt]
		d += time.Duration(rand.Int64N(int64(d)/2 + 1)) // jitter, so 100 retries do not arrive together
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// ---------------------------------------------------------------- uploads

// Uploader puts pages into R2 with many requests in flight. It is a bounded
// queue in front of Workers goroutines: a page waits in memory only until a
// slot is free, so the exporter never holds more than Queue pages, and
// nothing is written to the runner's disk. The first failure stops further
// uploads and is returned by Wait.
type Uploader struct {
	R2     *R2
	Prefix string // key prefix, "" for the bucket root
	DryRun bool   // count what would be uploaded, send nothing

	Uploaded atomic.Int64
	Bytes    atomic.Int64
	Failed   atomic.Int64

	once  sync.Once
	queue chan upload
	wg    sync.WaitGroup
	mu    sync.Mutex
	err   error
	stop  chan struct{}
}

type upload struct {
	key  string
	body []byte
	opts PutOptions
}

// NewUploader starts workers (default 96) and a queue of 2*workers pages.
func NewUploader(ctx context.Context, r2 *R2, workers int) *Uploader {
	if workers < 1 {
		workers = 96
	}
	u := &Uploader{R2: r2, queue: make(chan upload, 2*workers), stop: make(chan struct{})}
	for i := 0; i < workers; i++ {
		u.wg.Add(1)
		go func() {
			defer u.wg.Done()
			for j := range u.queue {
				if u.failed() {
					continue // drain
				}
				if u.DryRun {
					u.Uploaded.Add(1)
					u.Bytes.Add(int64(len(j.body)))
					continue
				}
				if err := u.R2.Put(ctx, u.Prefix+j.key, j.body, j.opts); err != nil {
					u.Failed.Add(1)
					u.fail(err)
					continue
				}
				u.Uploaded.Add(1)
				u.Bytes.Add(int64(len(j.body)))
			}
		}()
	}
	return u
}

func (u *Uploader) failed() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err != nil
}

func (u *Uploader) fail(err error) {
	u.mu.Lock()
	if u.err == nil {
		u.err = err
		close(u.stop)
	}
	u.mu.Unlock()
}

// Put queues one object; it blocks while the queue is full and returns the
// first upload error seen so far.
func (u *Uploader) Put(ctx context.Context, key string, body []byte, o PutOptions) error {
	if err := u.firstErr(); err != nil {
		return err
	}
	select {
	case u.queue <- upload{key, body, o}:
		return nil
	case <-u.stop:
		return u.firstErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (u *Uploader) firstErr() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.err
}

// Wait finishes the queue and returns the first error.
func (u *Uploader) Wait() error {
	u.once.Do(func() { close(u.queue) })
	u.wg.Wait()
	return u.firstErr()
}

// Sink adapts the uploader to Options.Sink.
func (u *Uploader) Sink(ctx context.Context) func(Entry, []byte) error {
	return func(e Entry, body []byte) error {
		return u.Put(ctx, e.File, body, PutOptions{ContentType: e.Type, CacheControl: pageCache(e), SHA256: e.Hash})
	}
}

// pageCache is the Cache-Control stored with a page: the live site's own
// value for pages, except that the search page (no-store on the live site
// because every query differs) is a plain static page here.
func pageCache(e Entry) string {
	if e.Cache == "" || strings.Contains(e.Cache, "no-store") {
		return "public, max-age=300, s-maxage=43200, stale-while-revalidate=300"
	}
	return e.Cache
}

// ---------------------------------------------------------------- SigV4

// signV4 adds the x-amz-date, x-amz-content-sha256 and Authorization headers.
// It signs the host and every header already on the request.
func signV4(req *http.Request, payloadHash, accessKey, secret, region, service string, t time.Time) {
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	headers := map[string]string{"host": req.Host}
	if req.Host == "" {
		headers["host"] = req.URL.Host
	}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + headers[k] + "\n")
	}
	signed := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		uriEncodePath(req.URL.EscapedPath()),
		canonicalQuery(req.URL.Query()),
		canonHeaders.String(),
		signed,
		payloadHash,
	}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	hashed := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(hashed[:])

	k := hmacSHA256([]byte("AWS4"+secret), day)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signed, sig))
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// uriEncodePath re-encodes an escaped path the way S3 expects: every byte but
// unreserved characters and "/" percent-encoded, once.
func uriEncodePath(escaped string) string {
	raw, err := url.PathUnescape(escaped)
	if err != nil {
		raw = escaped
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '/' || isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~'
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, strings.ReplaceAll(url.QueryEscape(k), "+", "%20")+"="+strings.ReplaceAll(url.QueryEscape(v), "+", "%20"))
		}
	}
	return strings.Join(parts, "&")
}
