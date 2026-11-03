package staticexport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPathExchangeCancelsWhileAnotherShardIsMissing(t *testing.T) {
	r2 := fakeR2(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if strings.HasSuffix(r.URL.Path, "shard-0.json") || strings.HasSuffix(r.URL.Path, "shard-2.json") {
				w.Write([]byte("[]"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for _, tc := range []struct {
		name            string
		parentTimeout   time.Duration
		exchangeTimeout time.Duration
	}{
		{"parent cancellation", 100 * time.Millisecond, time.Minute},
		{"exchange timeout", time.Minute, 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.parentTimeout)
			defer cancel()
			_, _, err := r2.PathExchange("cancelled-run", 0, 4, tc.exchangeTimeout)(ctx, 0, []string{"/hidden/"})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v, want deadline exceeded", err)
			}
			if !strings.Contains(err.Error(), "missing shards: [1 3]") {
				t.Fatalf("got %v, want error naming missing shards 1 and 3", err)
			}
			if tc.name == "exchange timeout" && ctx.Err() != nil {
				t.Fatalf("exchange timeout cancelled rendering context: %v", ctx.Err())
			}
		})
	}
}

func TestShardedCrawlRequiresExchange(t *testing.T) {
	_, err := Run(context.Background(), fakeSite(site()), Options{Out: t.TempDir(), Crawl: true, Shards: 3})
	if err == nil {
		t.Fatal("a sharded crawl without an exchange could silently lose pages")
	}
}
