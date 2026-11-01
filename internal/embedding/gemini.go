// Package embedding makes the document vectors of a release build with
// Gemini, behind local spending guards: a per-process call limit, a cap on
// concurrent requests, and durable token reservations (budget.go) made
// before every paid request.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The only model and size a release accepts.
const (
	Model      = "gemini-embedding-001"
	Dimensions = 768
)

// ErrProvider is wrapped by every provider failure. Messages never carry
// the key, the response body or headers.
var ErrProvider = errors.New("embedding provider")

func providerErr(msg string) error { return fmt.Errorf("%w: %s", ErrProvider, msg) }

// Gemini embeds documents and counts tokens with the Gemini API.
type Gemini struct {
	key      string
	Endpoint string // base URL, e.g. https://generativelanguage.googleapis.com/v1beta
	client   *http.Client
	slots    chan struct{}
	// SlotWait is how long a request waits for a free slot before failing.
	SlotWait time.Duration
	// Retries is how many times a request that got 429 or 5xx, or no
	// answer, is sent again; each attempt counts against the call limit.
	Retries int
	Backoff time.Duration
	now     func() time.Time

	mu       sync.Mutex
	maxCalls int
	day      int64
	calls    int
}

// Config bounds a Gemini client. Zero values mean the environment
// (GEMINI_MAX_CALLS_PER_DAY, GEMINI_CONCURRENCY) or the defaults (500, 2).
type Config struct {
	MaxCallsPerDay *int
	Concurrency    int
}

func envInt(name string, def, lo, hi int) (int, error) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		raw = strconv.Itoa(def)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || strings.TrimSpace(raw) != raw || strings.HasPrefix(raw, "+") || n < lo || n > hi {
		return 0, providerErr("invalid " + name + " configuration")
	}
	return n, nil
}

// NewGemini checks the key and the guards. No request is made.
func NewGemini(key string, c Config) (*Gemini, error) {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, providerErr("Gemini API key is required")
	}
	maxCalls := 0
	if c.MaxCallsPerDay != nil {
		maxCalls = *c.MaxCallsPerDay
		if maxCalls < 0 || maxCalls > 1000000 {
			return nil, providerErr("invalid GEMINI_MAX_CALLS_PER_DAY configuration")
		}
	} else {
		var err error
		if maxCalls, err = envInt("GEMINI_MAX_CALLS_PER_DAY", 500, 0, 1000000); err != nil {
			return nil, err
		}
	}
	concurrency := c.Concurrency
	if concurrency == 0 {
		var err error
		if concurrency, err = envInt("GEMINI_CONCURRENCY", 2, 1, 16); err != nil {
			return nil, err
		}
	} else if concurrency < 1 || concurrency > 16 {
		return nil, providerErr("invalid GEMINI_CONCURRENCY configuration")
	}
	g := &Gemini{
		key:      key,
		Endpoint: "https://generativelanguage.googleapis.com/v1beta",
		client: &http.Client{Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		slots:    make(chan struct{}, concurrency),
		SlotWait: 5 * time.Second,
		Retries:  2,
		Backoff:  2 * time.Second,
		now:      time.Now,
		maxCalls: maxCalls,
	}
	g.day = g.now().Unix() / 86400
	return g, nil
}

func (g *Gemini) reserveCall() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if day := g.now().Unix() / 86400; day > g.day {
		g.day, g.calls = day, 0
	}
	if g.calls >= g.maxCalls {
		return providerErr("daily provider call limit reached")
	}
	g.calls++
	return nil
}

func (g *Gemini) post(ctx context.Context, method string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	select {
	case g.slots <- struct{}{}:
	case <-time.After(g.SlotWait):
		return providerErr("provider busy")
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-g.slots }()
	wait := g.Backoff
	for attempt := 0; ; attempt++ {
		if err := g.reserveCall(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Endpoint+"/models/"+Model+":"+method, bytes.NewReader(payload))
		if err != nil {
			return providerErr("provider request failed")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", g.key)
		res, err := g.client.Do(req)
		status := 0
		if err == nil {
			status = res.StatusCode
			if status == http.StatusOK {
				raw, rerr := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
				res.Body.Close()
				if rerr != nil {
					return providerErr("provider request failed")
				}
				if len(raw) > 8<<20 {
					return providerErr("provider response too large")
				}
				if json.Unmarshal(raw, out) != nil {
					return providerErr("invalid provider response")
				}
				return nil
			}
			res.Body.Close()
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= g.Retries || (status != 0 && status != http.StatusTooManyRequests && status < 500) {
			if status != 0 {
				return providerErr(fmt.Sprintf("provider returned HTTP %d", status))
			}
			return providerErr("provider request failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
}

func checkTexts(texts []string) bool {
	if len(texts) == 0 || len(texts) > 100 {
		return false
	}
	for _, t := range texts {
		if strings.TrimSpace(t) == "" {
			return false
		}
	}
	return true
}

// Normalize checks a vector (768 finite values, not all zero) and scales it
// to unit length.
func Normalize(v []float64) ([]float32, error) {
	if len(v) != Dimensions {
		return nil, providerErr("embedding dimension mismatch")
	}
	var sum float64
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, providerErr("invalid embedding vector")
		}
		sum += x * x
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsInf(norm, 0) {
		return nil, providerErr("invalid embedding vector")
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x / norm)
	}
	return out, nil
}

// Embed returns unit-length RETRIEVAL_DOCUMENT vectors for 1 to 100 texts.
func (g *Gemini) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !checkTexts(texts) {
		return nil, providerErr("invalid embedding input")
	}
	requests := make([]any, len(texts))
	for i, t := range texts {
		requests[i] = map[string]any{"model": "models/" + Model, "content": map[string]any{"parts": []any{map[string]string{"text": t}}},
			"taskType": "RETRIEVAL_DOCUMENT", "outputDimensionality": Dimensions}
	}
	var res struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := g.post(ctx, "batchEmbedContents", map[string]any{"requests": requests}, &res); err != nil {
		return nil, err
	}
	if len(res.Embeddings) != len(texts) {
		return nil, providerErr("invalid embedding response")
	}
	out := make([][]float32, len(texts))
	for i, e := range res.Embeddings {
		v, err := Normalize(e.Values)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// CountTokens counts one group of 1 to 100 texts without embedding them.
func (g *Gemini) CountTokens(ctx context.Context, texts []string) (int64, error) {
	if !checkTexts(texts) {
		return 0, providerErr("invalid token count input")
	}
	parts := make([]any, len(texts))
	for i, t := range texts {
		parts[i] = map[string]string{"text": t}
	}
	var res struct {
		TotalTokens *json.Number `json:"totalTokens"`
	}
	if err := g.post(ctx, "countTokens", map[string]any{"contents": []any{map[string]any{"parts": parts}}}, &res); err != nil {
		return 0, err
	}
	if res.TotalTokens == nil {
		return 0, providerErr("invalid token count response")
	}
	n, err := res.TotalTokens.Int64()
	if err != nil || n <= 0 {
		return 0, providerErr("invalid token count response")
	}
	return n, nil
}
