package staticexport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestPathExchangeCancelsWhileAnotherShardIsMissing(t *testing.T) {
	r2 := fakeR2(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := r2.PathExchange("cancelled-run", 0, 2)(ctx, 0, []string{"/hidden/"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestShardedCrawlRequiresExchange(t *testing.T) {
	_, err := Run(context.Background(), fakeSite(site()), Options{Out: t.TempDir(), Crawl: true, Shards: 3})
	if err == nil {
		t.Fatal("a sharded crawl without an exchange could silently lose pages")
	}
}
