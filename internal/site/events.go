package site

// Events: an anonymous record of how the site is used, kept to answer one
// question, whether people want AI answers about Lebanese law. What people
// search for, which searches find nothing, which texts they read.
//
// Nothing about the visitor is written: no IP address, no user agent, no
// cookie, no referrer. A line holds the time (to the second), what happened,
// and whether the request came from a known crawler (so robots can be
// counted apart). One file per day, <dir>/events/YYYY-MM-DD.jsonl; files older
// than 90 days are deleted. cmd/usage-stats adds a day up.

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const eventsKeep = 90 * 24 * time.Hour

// Events appends usage lines to the day's file. A nil *Events records nothing.
type Events struct {
	dir string
	mu  sync.Mutex
	day string
	f   *os.File
}

// OpenEvents records into dir, creating it.
func OpenEvents(dir string) (*Events, error) {
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o750); err != nil {
		return nil, err
	}
	return &Events{dir: dir}, nil
}

// Log writes one event. ev gets "t" (UTC, seconds) added.
func (e *Events) Log(ev map[string]any) {
	if e == nil {
		return
	}
	now := time.Now().UTC()
	ev["t"] = now.Format(time.RFC3339)
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	day := now.Format("2006-01-02")
	if e.f == nil || e.day != day {
		if e.f != nil {
			e.f.Close()
		}
		e.f, err = os.OpenFile(filepath.Join(e.dir, "events", day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			log.Printf("events: %v", err)
			e.f = nil
			return
		}
		e.day = day
		go e.prune(now)
	}
	e.f.Write(append(b, '\n'))
}

func (e *Events) prune(now time.Time) {
	files, _ := filepath.Glob(filepath.Join(e.dir, "events", "*.jsonl"))
	for _, f := range files {
		d, err := time.Parse("2006-01-02", strings.TrimSuffix(filepath.Base(f), ".jsonl"))
		if err == nil && now.Sub(d) > eventsKeep {
			os.Remove(f)
		}
	}
}

var errTooMany = errors.New("too many sign-ups this minute")

var botUA = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|fetch|preview|scan|monitor|curl|wget|python|go-http|headless|lighthouse|httpclient|java/|okhttp|axios|node-fetch|facebookexternalhit|bingpreview|gptbot|chatgpt|claude|perplexity|bytespider|semrush|ahrefs|mj12|dotbot|petalbot|yandex|baidu|duckduck|applebot|amazonbot|ccbot|diffbot|meta-external`)

// IsBot says whether a request comes from a crawler or a script, by its user
// agent (read here, never stored). No user agent at all counts as a bot.
func IsBot(r *http.Request) bool {
	ua := r.UserAgent()
	return ua == "" || botUA.MatchString(ua)
}

// track records an event for a page request; HEAD requests are not reads.
func (s *Server) track(r *http.Request, ev map[string]any) {
	if s.ev == nil || r.Method != http.MethodGet {
		return
	}
	ev["bot"] = IsBot(r)
	s.ev.Log(ev)
}

// NormQuery is how a search is counted: trimmed, spaces collapsed, Latin
// letters lower-cased. Arabic is left as typed.
func NormQuery(q string) string {
	return strings.ToLower(strings.Join(strings.Fields(q), " "))
}

// ---------------------------------------------------------------- the day's sums

// Count is one row of a top list.
type Count struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
	N     int    `json:"n"`
}

// DayStats is one day added up. People and robots are counted apart; the
// top lists are people only.
type DayStats struct {
	Day            string  `json:"day"`
	Searches       int     `json:"searches"`
	SearchesBots   int     `json:"searches_bots"`
	UniqueQueries  int     `json:"unique_queries"`
	ZeroResults    int     `json:"zero_result_searches"`
	ZeroRate       float64 `json:"zero_result_rate"`
	TopQueries     []Count `json:"top_queries"`
	TopZeroQueries []Count `json:"top_zero_queries"`
	Reads          int     `json:"reads"`
	ReadsBots      int     `json:"reads_bots"`
	ArticleReads   int     `json:"article_reads"`
	TopLaws        []Count `json:"top_laws"`
	TopKinds       []Count `json:"top_kinds"`
	TopCodes       []Count `json:"top_codes"`
	AIPageViews    int     `json:"ai_page_views"`
}

// UsageStats adds up one day (YYYY-MM-DD, UTC) of the events kept in dir.
func UsageStats(dir, day string) (*DayStats, error) {
	st := &DayStats{Day: day}
	// Read line by line: a day of the record reached 270 MB on 22 Oct 2026
	// (crawlers), and reading it whole (twice, as bytes and as a string) took
	// the app past its 512 MB and got it killed, on every cold sitemap build.
	f, err := os.Open(filepath.Join(dir, "events", day+".jsonl"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	queries, zero, laws, kinds, codes := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	titles := map[string]string{}
	var lines *bufio.Scanner
	if f != nil {
		defer f.Close()
		lines = bufio.NewScanner(f)
		lines.Buffer(make([]byte, 64*1024), 4<<20)
	}
	for lines != nil && lines.Scan() {
		line := lines.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev struct {
			K       string `json:"k"`
			Bot     bool   `json:"bot"`
			Q       string `json:"q"`
			N       int    `json:"n"`
			Law     string `json:"law"`
			Title   string `json:"title"`
			Kind    string `json:"kind"`
			Code    string `json:"code"`
			Article string `json:"article"`
			Path    string `json:"path"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch ev.K {
		case "search":
			if ev.Bot {
				st.SearchesBots++
				continue
			}
			st.Searches++
			queries[ev.Q]++
			if ev.N == 0 {
				st.ZeroResults++
				zero[ev.Q]++
			}
		case "read":
			if ev.Bot {
				st.ReadsBots++
				continue
			}
			st.Reads++
			if ev.Article != "" {
				st.ArticleReads++
			}
			laws[ev.Law]++
			titles[ev.Law] = ev.Title
			if ev.Kind != "" {
				kinds[ev.Kind]++
			}
			if ev.Code != "" {
				codes[ev.Code]++
			}
		case "page":
			if !ev.Bot && ev.Path == "/ai/" {
				st.AIPageViews++
			}
		}
	}
	if lines != nil && lines.Err() != nil {
		return nil, lines.Err()
	}
	st.UniqueQueries = len(queries)
	if st.Searches > 0 {
		st.ZeroRate = float64(int(float64(st.ZeroResults)/float64(st.Searches)*1000+0.5)) / 1000
	}
	st.TopQueries, st.TopZeroQueries = top(queries, nil, 20), top(zero, nil, 20)
	st.TopLaws, st.TopKinds, st.TopCodes = top(laws, titles, 15), top(kinds, nil, 10), top(codes, nil, 10)
	return st, nil
}

func top(m map[string]int, titles map[string]string, n int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{Key: k, Title: titles[k], N: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// trackRead records a law (article "") or one of its articles being read.
func (s *Server) trackRead(r *http.Request, l *Law, article string) {
	ev := map[string]any{"k": "read", "law": strconv.Itoa(l.ID), "title": cut(l.Title, 80), "kind": l.Kind}
	if l.KindEn != nil {
		ev["kind"] = *l.KindEn
	}
	if l.Code != nil {
		ev["code"] = *l.Code
	}
	if article != "" {
		ev["article"] = article
	}
	s.track(r, ev)
}
