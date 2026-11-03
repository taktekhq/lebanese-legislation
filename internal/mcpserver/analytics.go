package mcpserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Google Analytics 4 through the Measurement Protocol, and PostHog through
// its /batch/ capture endpoint. MCP clients run no JavaScript and the
// Hugging Face dataset card can't load gtag, so the server sends the events
// itself, after the response, and a failed send never touches the answer.
// Each is off unless configured (Config): tests and forks send nothing.
//
// GA4: no cookies and no stored IP. client_id is a hash of IP + User-Agent +
// the UTC day, so one visitor counts as one user per day and can't be
// followed across days. Never give each request a fresh random id: GA4
// and PostHog then count every MCP call as a new user.
//
// PostHog keeps the visitor's IP (it resolves the location from it) and a
// stable id per machine, an HMAC of IP + User-Agent with a secret salt, so
// one machine is one person across days and across the MCP and the badges,
// and the id can't be reversed into an IP.

const (
	gaEndpoint     = "https://www.google-analytics.com/mp/collect"
	gaMaxValue     = 100 // GA4's limit on an event parameter value
	phDefaultHost  = "https://eu.i.posthog.com"
	phLib          = "lebaneselegislation-worker"
	phMaxString    = 10_000 // keeps an oversized argument from getting the event dropped
	analyticsAfter = 10 * time.Second
)

// gaStream is one GA4 data stream: its measurement ID and Measurement
// Protocol API secret. A stream missing either sends nothing.
type gaStream struct {
	measurementID, apiSecret string
}

func (g gaStream) on() bool { return g.measurementID != "" && g.apiSecret != "" }

// gaEvent is one GA4 event; params holds string or number values, a value
// never set being undefined.
type gaEvent struct {
	name   string
	params obj
}

// location is where a request came from, from Cloudflare's visitor location
// headers when the server sits behind Cloudflare.
type location struct {
	country, city, region string
}

func requestLocation(r *http.Request) location {
	return location{country: r.Header.Get("CF-IPCountry"), city: r.Header.Get("CF-IPCity"), region: r.Header.Get("CF-Region-Code")}
}

// clip trims a parameter value to max characters; GA drops longer ones whole.
func clip(v string, max int) string {
	if jsLen(v) > max {
		return jsSlice(v, max)
	}
	return v
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// visitorIDs derives the per-day client_id ("123.456", gtag's shape) and a
// numeric session_id from IP + User-Agent + day.
func visitorIDs(ip, ua, day string) (clientID, sessionID string) {
	h := sha256Hex(ip + "|" + ua + "|" + day)
	a, _ := strconv.ParseUint(h[0:8], 16, 64)
	b, _ := strconv.ParseUint(h[8:16], 16, 64)
	s, _ := strconv.ParseUint(h[16:24], 16, 64)
	return fmt.Sprintf("%d.%d", a, b), strconv.FormatUint(s, 10)
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// gaPayload builds the Measurement Protocol body. Every event carries the
// session and a token engagement time, without which GA4 counts no active
// users or sessions for Measurement Protocol traffic.
func gaPayload(clientID, sessionID string, loc location, events []gaEvent) obj {
	evs := make([]any, len(events))
	for i, e := range events {
		params := obj{{"session_id", sessionID}, {"engagement_time_msec", 1}}
		for _, p := range e.params {
			switch v := p.V.(type) {
			case nil:
				continue
			case string:
				if v == "" {
					continue
				}
				params.set(p.K, clip(v, gaMaxValue))
			default:
				params.set(p.K, v)
			}
		}
		evs[i] = obj{{"name", e.name}, {"params", params}}
	}
	body := obj{{"client_id", clientID}, {"events", evs}}
	if countryCode.MatchString(loc.country) {
		l := obj{{"country_id", loc.country}}
		if loc.city != "" {
			l.set("city", loc.city)
		}
		if loc.region != "" {
			l.set("region_id", loc.country+"-"+loc.region)
		}
		body.set("user_location", l)
	}
	return body
}

// track sends events for this request to stream in the background.
func (s *Server) track(stream gaStream, r *http.Request, events []gaEvent) {
	if !stream.on() || len(events) == 0 {
		return
	}
	ip, ua := analyticsIP(r), r.Header.Get("User-Agent")
	loc := requestLocation(r)
	day := s.now().UTC().Format("2006-01-02")
	s.background(func(ctx context.Context) {
		clientID, sessionID := visitorIDs(ip, ua, day)
		u := gaEndpoint + "?measurement_id=" + url.QueryEscape(stream.measurementID) + "&api_secret=" + url.QueryEscape(stream.apiSecret)
		if err := s.post(ctx, u, gaPayload(clientID, sessionID, loc, events)); err != nil {
			s.logf("ga: %v", err)
		}
	})
}

// ---------------------------------------------------------------- PostHog

type phEvent struct {
	event      string
	properties obj
	distinctID string
	timestamp  string
}

// clipProps trims every string (at any depth) to max characters.
func clipProps(p obj, max int) obj {
	out := make(obj, 0, len(p))
	for _, e := range p {
		if e.V == nil {
			continue // undefined: left out, as JSON.stringify does
		}
		out = append(out, kv{e.K, clipValue(e.V, max)})
	}
	return out
}

func clipValue(v any, max int) any {
	switch x := v.(type) {
	case string:
		return clip(x, max)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clipValue(e, max)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clip(e, max)
		}
		return out
	case obj:
		return clipProps(x, max)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clipValue(e, max)
		}
		return out
	case args:
		return clipValue(map[string]any(x), max)
	}
	return v
}

// visitorID is HMAC-SHA256(salt, ip|ua), or plain SHA-256 when the salt is
// unset (tests, forks).
func visitorID(salt, ip, ua string) string {
	data := []byte(ip + "|" + ua)
	if salt == "" {
		return sha256Hex(string(data))
	}
	m := hmac.New(sha256.New, []byte(salt))
	m.Write(data)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) phOn() bool { return s.cfg.PostHogKey != "" }

// phCapture sends events in the background. r supplies the IP and
// User-Agent; without one (the daily stats), each event must carry its
// distinct id.
func (s *Server) phCapture(r *http.Request, events []phEvent) {
	if !s.phOn() || len(events) == 0 {
		return
	}
	ip, ua := "", ""
	if r != nil {
		ip, ua = analyticsIP(r), r.Header.Get("User-Agent")
	}
	now := s.now().UTC().Format("2006-01-02T15:04:05.000Z")
	id := "lebaneselegislation-worker"
	if r != nil {
		id = visitorID(s.cfg.PostHogIDSalt, ip, ua)
	}
	batch := make([]any, len(events))
	for i, e := range events {
		props := clipProps(e.properties, phMaxString)
		d := e.distinctID
		if d == "" {
			d = id
		}
		props.set("distinct_id", d)
		props.set("$lib", phLib)
		if ip != "" {
			props.set("$ip", ip)
		}
		ts := e.timestamp
		if ts == "" {
			ts = now
		}
		batch[i] = obj{{"event", e.event}, {"timestamp", ts}, {"properties", props}}
	}
	host := s.cfg.PostHogHost
	if host == "" {
		host = phDefaultHost
	}
	body := obj{{"api_key", s.cfg.PostHogKey}, {"batch", batch}}
	s.background(func(ctx context.Context) {
		if err := s.post(ctx, strings.TrimRight(host, "/")+"/batch/", body); err != nil {
			s.logf("posthog: %v", err)
		}
	})
}

// post sends a JSON body; a failure is logged by the caller, never returned
// to a client.
func (s *Server) post(ctx context.Context, u string, body any) error {
	b, err := marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("answered %d", res.StatusCode)
	}
	return nil
}

// background runs f after the response, with its own deadline; Flush waits
// for every such send.
func (s *Server) background(f func(ctx context.Context)) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), analyticsAfter)
		defer cancel()
		f(ctx)
	}()
}

// ---------------------------------------------------------------- Hugging Face

// HFDataset is the dataset whose counters the daily stats and the
// "last updated" badge read.
const HFDataset = "taktekhq/lebanese-legislation"

const hfUserAgent = "lebaneselegislation-worker (+https://lebaneselegislation.com)"

func (s *Server) hfGet(ctx context.Context, query string, v any) error {
	base := s.cfg.HuggingFaceAPI
	if base == "" {
		base = "https://huggingface.co/api"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/datasets/"+HFDataset+"?"+query, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", hfUserAgent)
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("hugging face api answered %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// SendHFStats reads the Hugging Face dataset's public counters (downloads
// over the last 30 days, all time, likes) and sends them to PostHog as one
// hf_dataset_stats event. The Worker ran it daily from a cron trigger; call
// it once a day. It does nothing when PostHog is off.
func (s *Server) SendHFStats(ctx context.Context) error {
	if !s.phOn() {
		return nil
	}
	var c struct {
		Downloads        float64 `json:"downloads"`
		DownloadsAllTime float64 `json:"downloadsAllTime"`
		Likes            float64 `json:"likes"`
	}
	if err := s.hfGet(ctx, "expand[]=downloads&expand[]=downloadsAllTime&expand[]=likes", &c); err != nil {
		return err
	}
	s.phCapture(nil, []phEvent{{
		event:      "hf_dataset_stats",
		distinctID: "lebaneselegislation-hf-stats",
		properties: obj{{"dataset", HFDataset}, {"downloads_30d", c.Downloads}, {"downloads_all_time", c.DownloadsAllTime}, {"likes", c.Likes}},
	}})
	return nil
}

// hfDownloads is the dataset's all-time download count, cached for an hour.
func (s *Server) hfDownloads(ctx context.Context) (int, error) {
	s.hfMu.Lock()
	defer s.hfMu.Unlock()
	if !s.hfDownloadsAt.IsZero() && time.Since(s.hfDownloadsAt) < time.Hour {
		return s.hfDownloadsN, nil
	}
	var v struct {
		DownloadsAllTime float64 `json:"downloadsAllTime"`
	}
	if err := s.hfGet(ctx, "expand[]=downloadsAllTime", &v); err != nil {
		return 0, err
	}
	s.hfDownloadsN, s.hfDownloadsAt = int(v.DownloadsAllTime), time.Now()
	return s.hfDownloadsN, nil
}

// hfLastModified is when the dataset last changed on Hugging Face, cached
// for an hour: uploads happen once a night at most.
func (s *Server) hfLastModified(ctx context.Context) (string, error) {
	s.hfMu.Lock()
	defer s.hfMu.Unlock()
	if s.hfModified != "" && time.Since(s.hfModifiedAt) < time.Hour {
		return s.hfModified, nil
	}
	var v struct {
		LastModified string `json:"lastModified"`
	}
	if err := s.hfGet(ctx, "expand[]=lastModified", &v); err != nil {
		return "", err
	}
	if _, err := time.Parse(time.RFC3339, v.LastModified); err != nil {
		return "", fmt.Errorf("hugging face api gave no lastModified")
	}
	s.hfModified, s.hfModifiedAt = v.LastModified, time.Now()
	return v.LastModified, nil
}
