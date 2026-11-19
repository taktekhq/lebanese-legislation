package site

// Product events to PostHog (project LebaneseLegislation), sent by the
// server in the background with a short timeout; a PostHog outage never
// changes a response. Off unless SetPostHog gets a key.
//
// Server errors are not sent to PostHog (stopped 19 Nov 2026). logError
// writes them to the server log, which is the only place they are seen.

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

const phDefaultHost = "https://eu.i.posthog.com"

type phClient struct {
	key, host string
	http      *http.Client
}

// SetPostHog turns on server-side events. An empty key leaves it off;
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

// captureEvent sends a product event. Its callers are capped already (AI
// questions by the hourly and daily limits). props may set distinct_id; persons are never created.
func (s *Server) captureEvent(event string, props map[string]any) {
	if c := s.ph; c != nil {
		s.send(c, event, props)
	}
}

func (s *Server) send(c *phClient, event string, props map[string]any) {
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

// logError writes a failed request to the server log: what failed (kind),
// the HTTP method, the route pattern when the mux matched one, the address
// asked for, and the error text. The container log is where errors are read.
func logError(r *http.Request, kind string, err any) {
	if r.Pattern != "" {
		log.Printf("%s %s %s (route %s): %v", kind, r.Method, r.URL.RequestURI(), r.Pattern, err)
		return
	}
	log.Printf("%s %s %s: %v", kind, r.Method, r.URL.RequestURI(), err)
}
