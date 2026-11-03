package staticexport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIndexNowURLsKeepsSitemapPagesAndRemovals(t *testing.T) {
	base := "https://lebaneselegislation.com"
	inSitemap := map[string]bool{base + "/law/1/": true, base + "/law/2/": true}
	changed := []string{"/law/1/", "/hidden/", "/law/2/", "/law/1/", ""}
	removed := []string{"/en/law/9/"}
	got := IndexNowURLs(base+"/", changed, removed, inSitemap, 0)
	want := []string{base + "/law/1/", base + "/law/2/", base + "/en/law/9/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (noindex /hidden/ out, duplicates once)", got, want)
	}
	if got := IndexNowURLs(base, changed, removed, inSitemap, 1); len(got) != 1 || got[0] != base+"/law/1/" {
		t.Errorf("max 1 = %v", got)
	}
}

func TestIndexNowSubmitBatchesAndRetries(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Host, Key, KeyLocation string
			URLList                []string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		if body.Host != "lebaneselegislation.com" || body.KeyLocation != "https://lebaneselegislation.com/k.txt" {
			t.Errorf("host %q key location %q", body.Host, body.KeyLocation)
		}
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 2 { // the second request is throttled once
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		sizes = append(sizes, len(body.URLList))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	var waited []time.Duration
	n := &IndexNow{Endpoint: srv.URL, Host: "lebaneselegislation.com", Key: "k", Batch: 2,
		Wait: func(d time.Duration) { waited = append(waited, d) }}
	res, err := n.Submit(context.Background(), []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sizes, []int{2, 2, 1}) || len(res) != 3 || res[1].Tries != 2 || len(waited) != 1 {
		t.Errorf("sizes %v results %+v waited %v", sizes, res, waited)
	}
}

func TestIndexNowStopsOnRefusal(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("key not valid"))
	}))
	defer srv.Close()
	n := &IndexNow{Endpoint: srv.URL, Host: "h", Key: "k", Batch: 1, Wait: func(time.Duration) {}}
	_, err := n.Submit(context.Background(), []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "403") || calls != 1 {
		t.Errorf("err %v after %d calls, want one 403", err, calls)
	}
}

func TestSitemapURLsReadsTheIndexInOrder(t *testing.T) {
	h := fakeSite(site())
	get := func(_ context.Context, u string) ([]byte, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		return rec.Body.Bytes(), nil
	}
	got, err := SitemapURLs(context.Background(), "https://lebaneselegislation.com/sitemap.xml", get)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "https://lebaneselegislation.com/law/1/" {
		t.Errorf("got %v", got)
	}
}
