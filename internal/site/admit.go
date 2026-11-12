package site

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Admission control for the pages. The database pool holds 8 connections
// (cmd/lebleg), and a page needs one for a few milliseconds to a second. When
// more requests arrive than the pool can serve, the rest used to queue inside
// the pool until the 15 s request deadline and then answer 500: on 25 Oct
// 2026 at 08:10 to 08:30 UTC, 4,995 pages did that in twenty minutes, most of
// them /en/text/N/ asked by browsers-looking crawlers (Cloudflare "bypass",
// so nothing came from the edge cache), and visitors waited the full 15 s
// for the error.
//
// Now at most pageSlots pages run at once (the rest of the pool is left for
// the sitemap build and background work), a request waits up to admitWait for
// a slot, and when none opens it is turned away at once with 503 and
// Retry-After. A crawler that gets a 503 backs off and comes back; a 500 after
// 15 s is slower for everyone and reads as a broken site.

const (
	pageSlots  = 6
	heavySlots = 3 // of those, for the routes below
	admitWait  = 2 * time.Second
	busyAfter  = "10" // seconds a turned-away client is told to wait
)

// heavyRoute: a text's whole page, one of its parts, or its feed. They read the
// whole text and render every article: 35 to 470 ms of CPU each on the Code of
// Obligations (1 Nov 2026), against 3 to 12 ms for an article or a list page.
// At most heavySlots of them run at once, so a crawl of big texts cannot take
// every slot from the cheap pages.
var heavyRoute = regexp.MustCompile(`^/(en/)?(law|decree|text)/[0-9]+/(part/[0-9]+/|feed\.xml)?$`)

// admitExempt: what never touches the database, or has its own control.
func admitExempt(r *http.Request) bool {
	p := r.URL.Path
	switch p {
	case "/healthz", "/robots.txt", "/sitemap.xml", "/blog-sitemap.xml",
		"/styles.css", "/legislation.css", "/main.js", "/legislation.js", "/language.js", "/webmcp.js", "/ga.js", "/ph.js":
		return true
	}
	return strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/sitemaps/") || strings.HasPrefix(p, "/.well-known/")
}

var (
	shedCount atomic.Int64
	shedLast  atomic.Int64 // unix seconds of the last log line
)

// admit takes a page slot, or answers 503 itself. It reports whether the
// caller may go on; release is nil when no slot was taken.
func (s *Server) admit(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	if s.pageSem == nil || s.isStatic || admitExempt(r) {
		return func() {}, true
	}
	wait := s.admitWait
	if wait == 0 {
		wait = admitWait
	}
	var releaseHeavy func()
	if s.heavySem != nil && heavyRoute.MatchString(r.URL.Path) {
		if releaseHeavy, ok = s.heavySem.acquire(r.Context(), wait); !ok {
			s.shed(w, r)
			return nil, false
		}
	}
	rel, ok := s.pageSem.acquire(r.Context(), wait)
	if ok {
		if releaseHeavy == nil {
			return rel, true
		}
		return func() { rel(); releaseHeavy() }, true
	}
	if releaseHeavy != nil {
		releaseHeavy()
	}
	s.shed(w, r)
	return nil, false
}

// shed turns a request away with 503 and Retry-After, and says so in the log
// at most every ten seconds.
func (s *Server) shed(w http.ResponseWriter, r *http.Request) {
	shedCount.Add(1)
	if now, last := time.Now().Unix(), shedLast.Load(); now-last >= 10 && shedLast.CompareAndSwap(last, now) {
		log.Printf("busy: turned away %d requests since the last line, e.g. %s", shedCount.Swap(0), r.URL.RequestURI())
	}
	busy(w, busyAfter)
}

// busy answers 503 with Retry-After, never cached.
func busy(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("The site is busy for a moment. Please try again shortly.\n"))
}

// overloaded says an error is the database being too busy or away (a request
// that ran out of time, the pool or the server full, Postgres restarting in a
// swap), not a bug: those answer 503 with Retry-After, not 500.
func overloaded(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "53300", "57P01", "57P02", "57P03": // too many connections, server shutting down, crash, starting up
			return true
		}
	}
	return false
}
