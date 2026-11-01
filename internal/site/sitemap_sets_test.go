package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShardWriterCutsFilesAndKeepsTheEnglishTwin(t *testing.T) {
	w := newShardWriter(3, true)
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	for i := 1; i <= 7; i++ {
		w.add("/law/"+strings.Repeat("1", i)+"/", day(i))
	}
	got := w.finish()
	if len(got) != 3 || got[0].n != 3 || got[1].n != 3 || got[2].n != 1 {
		t.Fatalf("files %+v, want 3, 3 and 1 URLs", got)
	}
	if !got[0].mod.Equal(day(3)) || !got[2].mod.Equal(day(7)) {
		t.Errorf("newest change per file: %v, %v", got[0].mod, got[2].mod)
	}
	ar, en := parseSitemap(t, string(got[0].ar)), parseSitemap(t, string(got[0].en))
	if len(ar.URLs) != 3 || len(en.URLs) != 3 {
		t.Fatalf("file 1 holds %d Arabic and %d English URLs", len(ar.URLs), len(en.URLs))
	}
	for i, u := range ar.URLs {
		if want := strings.Replace(u.Loc, Base+"/", Base+"/en/", 1); en.URLs[i].Loc != want || en.URLs[i].Lastmod != u.Lastmod {
			t.Errorf("entry %d: %s, want %s", i, en.URLs[i].Loc, want)
		}
	}
	if got[0].ar[0] != '<' || !strings.HasSuffix(string(got[2].ar), "</urlset>\n") {
		t.Error("a file does not open and close as a urlset")
	}
	if none := newShardWriter(3, false).finish(); len(none) != 0 {
		t.Errorf("no rows gave %d files", len(none))
	}
	if only := newShardWriter(3, false); true {
		only.add("/ruling/1/", time.Time{})
		if f := only.finish(); len(f) != 1 || f[0].en != nil || strings.Contains(string(f[0].ar), "<lastmod>") {
			t.Errorf("a family without English files, or a page of unknown date, got %+v", f)
		}
	}
}

// A test server whose families come from a counted fake, not Postgres.
func fakeSets(build func(context.Context, sitemapSet) ([]sitemapShard, error)) (*Server, *atomic.Pointer[string]) {
	var ver atomic.Pointer[string]
	v := "1"
	ver.Store(&v)
	s := &Server{buildSet: build, versionFn: func(context.Context) string { return *ver.Load() }}
	return s, &ver
}

func shardsOf(tag string) []sitemapShard {
	return []sitemapShard{{n: 1, ar: []byte(tag)}}
}

func TestSetShardsBuildsOncePerLoadAndSharesTheFirstBuild(t *testing.T) {
	var builds atomic.Int32
	release := make(chan struct{})
	s, _ := fakeSets(func(context.Context, sitemapSet) ([]sitemapShard, error) {
		builds.Add(1)
		<-release
		return shardsOf("a"), nil
	})
	set := sitemapSet{name: "laws"}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.setShards(context.Background(), set)
			if err != nil || string(got[0].ar) != "a" {
				t.Errorf("got %v, %v", got, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	for i := 0; i < 5; i++ {
		_, _ = s.setShards(context.Background(), set)
	}
	if n := builds.Load(); n != 1 {
		t.Errorf("%d builds for 25 requests, want 1", n)
	}
}

func TestSetShardsServesTheOldFilesWhileTheNewOnesBuild(t *testing.T) {
	var builds atomic.Int32
	gate := make(chan struct{})
	s, ver := fakeSets(func(context.Context, sitemapSet) ([]sitemapShard, error) {
		if builds.Add(1) == 1 {
			return shardsOf("old"), nil
		}
		<-gate
		return shardsOf("new"), nil
	})
	set := sitemapSet{name: "articles"}
	if got, _ := s.setShards(context.Background(), set); string(got[0].ar) != "old" {
		t.Fatal("first build")
	}
	nv := "2" // a data load
	ver.Store(&nv)
	s.sets.vat = time.Time{} // the version is asked at most once a minute
	start := time.Now()
	got, err := s.setShards(context.Background(), set)
	if err != nil || string(got[0].ar) != "old" || time.Since(start) > time.Second {
		t.Fatalf("during the rebuild got %q, %v after %v; want the old files at once", got[0].ar, err, time.Since(start))
	}
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := s.setShards(context.Background(), set); string(got[0].ar) == "new" {
			if n := builds.Load(); n != 2 {
				t.Errorf("%d builds, want 2", n)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the new files never replaced the old")
}

func TestSetShardsDoesNotRetryAFailedBuildAtOnce(t *testing.T) {
	var builds atomic.Int32
	s, _ := fakeSets(func(context.Context, sitemapSet) ([]sitemapShard, error) {
		if builds.Add(1) == 1 {
			return nil, errors.New("database away")
		}
		return shardsOf("ok"), nil
	})
	set := sitemapSet{name: "rulings"}
	if _, err := s.setShards(context.Background(), set); err == nil {
		t.Fatal("want the build's error")
	}
	if _, err := s.setShards(context.Background(), set); err == nil || builds.Load() != 1 {
		t.Fatalf("second ask: err %v, %d builds; want the same error and no new build", err, builds.Load())
	}
	s.sets.m["rulings"].failedAt = time.Now().Add(-2 * setRetryAfter)
	if got, err := s.setShards(context.Background(), set); err != nil || string(got[0].ar) != "ok" {
		t.Fatalf("after the wait: %v, %v", got, err)
	}
}

func TestSetShardsBuildSurvivesAClientThatGivesUp(t *testing.T) {
	release := make(chan struct{})
	s, _ := fakeSets(func(context.Context, sitemapSet) ([]sitemapShard, error) {
		<-release
		return shardsOf("done"), nil
	})
	set := sitemapSet{name: "gazette"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.setShards(ctx, set); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v, want the client's deadline", err)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if got, err := s.setShards(context.Background(), set); err != nil || string(got[0].ar) != "done" {
		t.Fatalf("the build should have finished on its own: %v, %v", got, err)
	}
}

func TestOneFamilyIsBuiltAtATime(t *testing.T) {
	var running, peak atomic.Int32
	s, _ := fakeSets(func(context.Context, sitemapSet) ([]sitemapShard, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return shardsOf("x"), nil
	})
	var wg sync.WaitGroup
	for _, name := range []string{"laws", "articles", "rulings", "gazette", "documents"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.setShards(context.Background(), sitemapSet{name: name})
		}()
	}
	wg.Wait()
	if p := peak.Load(); p != 1 {
		t.Errorf("%d families built at once, want 1", p)
	}
}

func TestSitemapNotReadyIs503WithRetryAfter(t *testing.T) {
	s := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, c := range map[string]struct {
		r   *http.Request
		err error
	}{
		"client gave up":    {httptest.NewRequest("GET", "/sitemap.xml", nil).WithContext(ctx), errors.New("x")},
		"ran out of time":   {httptest.NewRequest("GET", "/sitemap.xml", nil), context.DeadlineExceeded},
		"database too full": {httptest.NewRequest("GET", "/sitemap.xml", nil), &pgErr{"53300"}},
	} {
		w := httptest.NewRecorder()
		s.sitemapFail(w, c.r, c.err)
		if w.Code != 503 || w.Header().Get("Retry-After") != sitemapRetryAfter || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d, Retry-After %q, Cache-Control %q", name, w.Code, w.Header().Get("Retry-After"), w.Header().Get("Cache-Control"))
		}
	}
}
