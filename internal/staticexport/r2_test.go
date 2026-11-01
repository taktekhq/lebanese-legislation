package staticexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The GET Object example from the AWS Signature Version 4 documentation.
func TestSignV4MatchesAWSExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	empty := sha256.Sum256(nil)
	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	signV4(req, hex.EncodeToString(empty[:]), "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", at)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization\n got %s\nwant %s", got, want)
	}
}

func fakeR2(t *testing.T, h http.HandlerFunc) *R2 {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &R2{Endpoint: u, Bucket: "site", Region: "auto", AccessKey: "AK", SecretKey: "SK", HTTP: srv.Client(),
		Now:    func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) },
		Delays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}
}

func TestPutSignsKeepsLiteralPercentAndRetries(t *testing.T) {
	var tries int32
	var got struct {
		path, rawPath, ctype, cache, meta, auth, hash string
		body                                          string
	}
	r := fakeR2(t, func(w http.ResponseWriter, req *http.Request) {
		if atomic.AddInt32(&tries, 1) < 3 {
			http.Error(w, "SlowDown", http.StatusServiceUnavailable)
			return
		}
		b, _ := io.ReadAll(req.Body)
		got.path, got.rawPath = req.URL.Path, req.URL.EscapedPath()
		got.ctype, got.cache, got.meta = req.Header.Get("Content-Type"), req.Header.Get("Cache-Control"), req.Header.Get("X-Amz-Meta-Sha256")
		got.auth, got.hash, got.body = req.Header.Get("Authorization"), req.Header.Get("X-Amz-Content-Sha256"), string(b)
	})
	sum := sha256.Sum256([]byte("<html>"))
	h := hex.EncodeToString(sum[:])
	err := r.Put(context.Background(), "decree/2/article/1-%D8%A7/index.html", []byte("<html>"),
		PutOptions{ContentType: "text/html; charset=utf-8", CacheControl: "public, max-age=300", SHA256: h})
	if err != nil {
		t.Fatal(err)
	}
	if tries != 3 {
		t.Errorf("tries = %d, want 2 failures then success", tries)
	}
	if got.path != "/site/decree/2/article/1-%D8%A7/index.html" {
		t.Errorf("key on the wire (unescaped) = %q: the literal percent must reach the bucket", got.path)
	}
	if !strings.Contains(got.rawPath, "%25D8%25A7") {
		t.Errorf("escaped path = %q, want the percent escaped once", got.rawPath)
	}
	if got.ctype != "text/html; charset=utf-8" || got.cache != "public, max-age=300" || got.meta != h || got.hash != h || got.body != "<html>" {
		t.Errorf("headers or body wrong: %+v", got)
	}
	if !strings.HasPrefix(got.auth, "AWS4-HMAC-SHA256 Credential=AK/20261101/auto/s3/aws4_request") {
		t.Errorf("Authorization = %q", got.auth)
	}
}

func TestPutDoesNotRetryAClientError(t *testing.T) {
	var tries int32
	r := fakeR2(t, func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&tries, 1)
		http.Error(w, "AccessDenied", http.StatusForbidden)
	})
	if err := r.Put(context.Background(), "a/index.html", []byte("x"), PutOptions{}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want a 403", err)
	}
	if tries != 1 {
		t.Errorf("tries = %d, want 1: a 403 is not temporary", tries)
	}
}

func TestGetAndDelete(t *testing.T) {
	store := map[string]string{"/site/_export/manifest.jsonl": "line\n"}
	var mu sync.Mutex
	r := fakeR2(t, func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := store[req.URL.Path]
		switch {
		case req.Method == http.MethodGet && ok:
			io.WriteString(w, v)
		case req.Method == http.MethodDelete:
			delete(store, req.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, req)
		}
	})
	b, err := r.Get(context.Background(), "_export/manifest.jsonl")
	if err != nil || string(b) != "line\n" {
		t.Fatalf("Get = %q, %v", b, err)
	}
	if _, err := r.Get(context.Background(), "_export/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: %v, want ErrNotFound", err)
	}
	if err := r.Delete(context.Background(), "_export/manifest.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), "_export/manifest.jsonl"); err != nil {
		t.Errorf("deleting a gone key: %v, want nil", err)
	}
}

func TestUploaderRunsManyAtOnceAndStopsOnFirstFailure(t *testing.T) {
	var inflight, peak, puts int32
	r := fakeR2(t, func(w http.ResponseWriter, req *http.Request) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&puts, 1)
		atomic.AddInt32(&inflight, -1)
	})
	u := NewUploader(context.Background(), r, 32)
	for i := 0; i < 200; i++ {
		if err := u.Put(context.Background(), "p/"+string(rune('a'+i%26))+"/"+strconv.Itoa(i)+"/index.html", []byte("x"), PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Wait(); err != nil {
		t.Fatal(err)
	}
	if u.Uploaded.Load() != 200 || puts != 200 {
		t.Errorf("uploaded %d, server saw %d, want 200", u.Uploaded.Load(), puts)
	}
	if peak < 16 {
		t.Errorf("peak concurrency %d, want many requests in flight (32 workers)", peak)
	}

	bad := fakeR2(t, func(w http.ResponseWriter, req *http.Request) { http.Error(w, "nope", http.StatusForbidden) })
	u2 := NewUploader(context.Background(), bad, 4)
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		err = u2.Put(context.Background(), "k"+strconv.Itoa(i)+"/index.html", []byte("x"), PutOptions{})
	}
	if werr := u2.Wait(); werr == nil || !strings.Contains(werr.Error(), "403") {
		t.Errorf("Wait = %v, want the first failure", werr)
	}
}

func TestDryRunUploaderSendsNothing(t *testing.T) {
	r := fakeR2(t, func(w http.ResponseWriter, req *http.Request) { t.Error("a dry run sent a request") })
	u := NewUploader(context.Background(), r, 4)
	u.DryRun = true
	for i := 0; i < 10; i++ {
		_ = u.Put(context.Background(), "a/index.html", []byte("abc"), PutOptions{})
	}
	if err := u.Wait(); err != nil || u.Uploaded.Load() != 10 || u.Bytes.Load() != 30 {
		t.Errorf("err=%v uploaded=%d bytes=%d", err, u.Uploaded.Load(), u.Bytes.Load())
	}
}

func TestSinkRunUploadsOnlyChangedPagesAndWritesNoPages(t *testing.T) {
	pages := site()
	var mu sync.Mutex
	sent := map[string]string{}
	sink := func(e Entry, body []byte) error {
		mu.Lock()
		defer mu.Unlock()
		sent[e.File] = string(body)
		return nil
	}
	out := t.TempDir()
	_, err := Run(context.Background(), fakeSite(pages), Options{Out: out, Crawl: true, Extra: []string{"/"}, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	if sent["law/1/index.html"] == "" || sent["hidden/index.html"] == "" || sent["sitemap.xml"] == "" {
		t.Fatalf("sink missed pages: %v", keys(sent))
	}
	if _, err := os.Stat(filepath.Join(out, "law")); err == nil {
		t.Error("a sink run wrote a page file to disk")
	}
	// the next run, with the first run's manifest as the previous state
	prev, err := ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	pages["/hidden/"] = `<html><body>edited</body></html>`
	delete(pages, "/en/law/1/")
	sent2 := map[string]string{}
	sink2 := func(e Entry, body []byte) error { sent2[e.File] = string(body); return nil }
	st, err := Run(context.Background(), fakeSite(pages), Options{Out: t.TempDir(), Crawl: true, Extra: []string{"/"}, Sink: sink2, PrevEntries: prev})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent2) != 1 || !strings.Contains(sent2["hidden/index.html"], "edited") {
		t.Errorf("second run sent %v, want only the edited page", keys(sent2))
	}
	if len(st.RemovedKeys) != 1 || st.RemovedKeys[0] != "en/law/1/index.html" {
		t.Errorf("RemovedKeys = %v, want the English law page", st.RemovedKeys)
	}
}

func keys(m map[string]string) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}
