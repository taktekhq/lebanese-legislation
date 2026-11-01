package site

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func pgErr_(code string) error { return &pgconn.PgError{Code: code} }

type pgErr struct{ code string }

func (e *pgErr) Error() string { return "pg " + e.code }
func (e *pgErr) Unwrap() error { return pgErr_(e.code) }

func TestOverloadedIsTheDatabaseBeingBusyNotABug(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{context.DeadlineExceeded, true},
		{errors.Join(errors.New("query"), context.DeadlineExceeded), true},
		{syscall.ECONNREFUSED, true},
		{pgErr_("53300"), true}, {pgErr_("57P03"), true}, {pgErr_("57P01"), true},
		{pgErr_("42P01"), false}, // a missing table is a bug
		{context.Canceled, false},
		{errors.New("template: no such page"), false},
	} {
		if got := overloaded(c.err); got != c.want {
			t.Errorf("overloaded(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestFailAnswers503WhenTheDatabaseIsBusyAnd500ForABug(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.fail(w, httptest.NewRequest("GET", "/law/1/", nil), context.DeadlineExceeded)
	if w.Code != 503 || w.Header().Get("Retry-After") != busyAfter || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("deadline: %d, Retry-After %q, Cache-Control %q", w.Code, w.Header().Get("Retry-After"), w.Header().Get("Cache-Control"))
	}
	w = httptest.NewRecorder()
	s.fail(w, httptest.NewRequest("GET", "/law/1/", nil), errors.New("bug"))
	if w.Code != 500 {
		t.Errorf("a bug answered %d, want 500", w.Code)
	}
}

func TestPagesBeyondTheSlotsAreTurnedAwayFastWith503(t *testing.T) {
	inside, release := make(chan struct{}), make(chan struct{})
	s := &Server{pageSem: newDBSemaphore(1), admitWait: 40 * time.Millisecond}
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/law/1/" {
			inside <- struct{}{}
			<-release
		}
		_, _ = w.Write([]byte("ok"))
	})
	done := make(chan int)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/law/1/", nil))
		done <- w.Code
	}()
	<-inside // the one slot is taken

	start := time.Now()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/law/2/", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != busyAfter || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("second page: %d, Retry-After %q, Cache-Control %q", w.Code, w.Header().Get("Retry-After"), w.Header().Get("Cache-Control"))
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("turned away after %v, want about the wait (40 ms)", d)
	}
	for _, p := range []string{"/healthz", "/robots.txt", "/sitemap.xml", "/sitemaps/ar-laws-1.xml", "/styles.css", "/assets/fonts/a.woff2"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Errorf("%s answered %d while the pages were full, want 200", p, w.Code)
		}
	}
	close(release)
	if code := <-done; code != 200 {
		t.Errorf("the page that held the slot answered %d", code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/law/3/", nil))
	if w.Code != 200 {
		t.Errorf("after the slot was freed: %d", w.Code)
	}
}

func TestAPageWaitsBrieflyForASlotBeforeGivingUp(t *testing.T) {
	s := &Server{pageSem: newDBSemaphore(1), admitWait: time.Second}
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	rel, _ := s.pageSem.acquire(context.Background(), 0)
	go func() { time.Sleep(60 * time.Millisecond); rel() }()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/law/1/", nil))
	if w.Code != 200 {
		t.Errorf("a slot freed within the wait, yet the page answered %d", w.Code)
	}
}

func TestHeavyRoutes(t *testing.T) {
	for p, want := range map[string]bool{
		"/law/244226/": true, "/en/decree/8/": true, "/text/9/part/2/": true, "/law/1/feed.xml": true, "/en/text/5/feed.xml": true,
		"/law/244226/article/221/": false, "/laws/year/2015/": false, "/ruling/5/": false, "/law/": false, "/law/x/": false,
		"/law/1/article/2/rulings/": false, "/changes/feed.xml": false,
	} {
		if got := heavyRoute.MatchString(p); got != want {
			t.Errorf("heavyRoute(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWholeTextPagesHaveTheirOwnSmallerCap(t *testing.T) {
	inside, release := make(chan struct{}), make(chan struct{})
	s := &Server{pageSem: newDBSemaphore(6), heavySem: newDBSemaphore(1), admitWait: 40 * time.Millisecond}
	s.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/law/1/" {
			inside <- struct{}{}
			<-release
		}
		_, _ = w.Write([]byte("ok"))
	})
	done := make(chan int)
	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/law/1/", nil))
		done <- w.Code
	}()
	<-inside
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/decree/2/", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") != busyAfter {
		t.Errorf("a second whole text answered %d, want 503 with Retry-After", w.Code)
	}
	for _, p := range []string{"/law/1/article/3/", "/laws/year/2015/", "/ruling/9/"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Errorf("%s answered %d while a whole text was rendering, want 200", p, w.Code)
		}
	}
	if len(s.pageSem.slots) != 1 {
		t.Errorf("%d page slots held, want 1 (the whole text still rendering)", len(s.pageSem.slots))
	}
	close(release)
	<-done
	if len(s.pageSem.slots) != 0 || len(s.heavySem.slots) != 0 {
		t.Errorf("slots left held: %d page, %d heavy", len(s.pageSem.slots), len(s.heavySem.slots))
	}
}
