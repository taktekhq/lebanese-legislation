package vectorize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChunkID(t *testing.T) {
	if got := ChunkID("m", "law", 12, "p", "ar", "h", 0); got != "m:law:12:p:ar:h:0" {
		t.Fatal(got)
	}
	joined := "model:law:12:p:ar:" + strings.Repeat("a", 64) + ":780"
	sum := sha256.Sum256([]byte(joined))
	if got := ChunkID("model", "law", 12, "p", "ar", strings.Repeat("a", 64), 780); got != hex.EncodeToString(sum[:16]) {
		t.Fatal(got)
	}
	// Byte length, not rune count, determines the API limit.
	if len(ChunkID(strings.Repeat("م", 40), "law", 1, "p", "ar", "h", 0)) != 32 {
		t.Fatal("oversized UTF-8 ID")
	}
}

func TestQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/a/vectorize/v2/indexes/i/query" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("bad request: %s", r.URL)
		}
		var body struct {
			Vector []float32 `json:"vector"`
			TopK   int       `json:"topK"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TopK != 100 || len(body.Vector) != 2 {
			t.Error(body, err)
		}
		fmt.Fprint(w, `{"success":true,"result":{"matches":[{"id":"b","score":0.9},{"id":"a","score":0.8}]}}`)
	}))
	defer server.Close()
	c, _ := New("a", "i", "token")
	c.baseURL = server.URL
	matches, err := c.Query(context.Background(), []float32{1, 2}, 200)
	if err != nil || len(matches) != 2 || matches[0].ID != "b" || matches[1].Score != 0.8 {
		t.Fatal(matches, err)
	}
}

func TestMutationBatches(t *testing.T) {
	var upserts, deletes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/upsert"):
			if r.Header.Get("Content-Type") != "application/x-ndjson" {
				t.Error("not NDJSON")
			}
			n := 0
			dec := json.NewDecoder(r.Body)
			for dec.More() {
				var v Vector
				if err := dec.Decode(&v); err != nil {
					t.Error(err)
					break
				}
				n++
			}
			upserts = append(upserts, n)
		case strings.HasSuffix(r.URL.Path, "/delete_by_ids"):
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			deletes = append(deletes, len(body.IDs))
		default:
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"success":true,"result":{"mutationId":"x"}}`)
	}))
	defer server.Close()
	c, _ := New("a", "i", "t")
	c.baseURL = server.URL
	vectors := make([]Vector, 1001)
	ids := make([]string, 1001)
	for i := range vectors {
		ids[i] = fmt.Sprint(i)
		vectors[i] = Vector{ID: ids[i], Values: []float32{1}}
	}
	if err := c.Upsert(context.Background(), vectors); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteByIDs(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	// Cloudflare takes 1,000 vectors per upsert but only 100 ids per delete.
	if fmt.Sprint(upserts) != "[1000 1]" || fmt.Sprint(deletes) != "[100 100 100 100 100 100 100 100 100 100 1]" {
		t.Fatal(upserts, deletes)
	}
}

func TestRetriesAndErrors(t *testing.T) {
	for _, status := range []int{429, 500, 400, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, `{"success":false,"errors":[{"code":1000,"message":"failed"}]}`)
			}))
			defer server.Close()
			c, _ := New("a", "i", "t")
			c.baseURL = server.URL
			c.backoff = time.Microsecond
			_, err := c.Query(context.Background(), []float32{1}, 1)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "1000 failed") {
				t.Fatalf("the error does not carry Cloudflare's message: %v", err)
			}
			want := 1
			if status == 429 || status == 500 {
				want = 6
			}
			if calls != want {
				t.Fatal(calls, want)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer server.Close()
	c, _ := New("a", "i", "t")
	c.baseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Query(ctx, []float32{1}, 1); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestRetryThenSuccess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(429)
			return
		}
		if calls == 2 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"success":true,"result":{"matches":[{"id":"ok","score":1}]}}`)
	}))
	defer server.Close()
	c, _ := New("a", "i", "t")
	c.baseURL = server.URL
	c.backoff = time.Microsecond
	matches, err := c.Query(context.Background(), []float32{1}, 1)
	if err != nil || len(matches) != 1 || matches[0].ID != "ok" || calls != 3 {
		t.Fatal(matches, err, calls)
	}
}

func TestInvalidResponses(t *testing.T) {
	for _, body := range []string{`{`, `{"success":true,"result":{"matches":"wrong"}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			c, _ := New("a", "i", "t")
			c.baseURL = server.URL
			if _, err := c.Query(context.Background(), []float32{1}, 1); err == nil {
				t.Fatal("expected invalid response error")
			}
		})
	}
}
