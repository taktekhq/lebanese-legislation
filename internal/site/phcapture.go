package site

// Server errors to PostHog (project LebaneseLegislation): every 500 the app
// answers, from s.fail or from a recovered panic, becomes a $exception event
// in PostHog's error tracking using a shared service ID and generic error
// text. No request URL, visitor identifier or exception text is sent.
// Sent in the background with a short timeout; a PostHog outage
// never changes a response. Off unless SetPostHog gets a key.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

const phDefaultHost = "https://eu.i.posthog.com"

// A database outage turns every request (bots included, ~145k a day) into a
// 500; without a cap that would burn the month's free error allowance in a
// day. phBurst events at once, then one a second.
const phBurst = 5

type phClient struct {
	key, host string
	http      *http.Client

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// allow takes one token from the bucket.
func (c *phClient) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.last.IsZero() {
		c.tokens = phBurst
	} else {
		c.tokens = min(phBurst, c.tokens+now.Sub(c.last).Seconds())
	}
	c.last = now
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

// SetPostHog turns on server-side error capture. An empty key leaves it off;
// an empty host means PostHog's EU cloud.
func (s *Server) SetPostHog(key, host string) {
	if key == "" {
		s.ph = nil
		return
	}
	if host == "" {
		host = phDefaultHost
	}
	s.ph = &phClient{key: key, host: host, http: &http.Client{Timeout: 3 * time.Second}}
}

// capture sends one event about request r in the background.
func (s *Server) capture(event string, r *http.Request, props map[string]any) {
	c := s.ph
	if c == nil || !c.allow() {
		return
	}
	p := map[string]any{
		"$lib":                    "lebaneselegislation-server",
		"$process_person_profile": false,
		"$geoip_disable":          true,
		"distinct_id":             "lebaneselegislation-server",
	}
	for k, v := range props {
		p[k] = v
	}
	body, err := json.Marshal(map[string]any{
		"api_key": c.key,
		"batch":   []map[string]any{{"event": event, "properties": p, "timestamp": time.Now().UTC().Format(time.RFC3339Nano)}},
	})
	if err != nil {
		log.Printf("posthog: %v", err)
		return
	}
	go func() {
		res, err := c.http.Post(c.host+"/batch/", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("posthog: %v", err)
			return
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			log.Printf("posthog: capture answered %d", res.StatusCode)
		}
	}()
}

// captureError reports a 500 as a $exception. A client that went away
// (context canceled) is not an error on our side.
func (s *Server) captureError(r *http.Request, kind string, err any) {
	if e, ok := err.(error); ok && errors.Is(e, context.Canceled) {
		return
	}
	s.capture("$exception", r, map[string]any{
		"$exception_list": []map[string]any{{"type": kind, "value": "Server request failed", "mechanism": map[string]any{"handled": kind != "panic", "synthetic": false}}},
		"status":          http.StatusInternalServerError,
	})
}
