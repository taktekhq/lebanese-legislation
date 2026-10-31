package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ServiceError exposes only an HTTP status, never provider bodies or headers.
type ServiceError struct{ Status int }

func (e *ServiceError) Error() string {
	return fmt.Sprintf("embedding service returned HTTP %d", e.Status)
}

type Embedder interface {
	Embed(context.Context, []string, bool) ([][]float32, error)
}

type Gemini struct {
	key, model, endpoint string
	client               *http.Client
}

var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func NewGemini(key, model string) (*Gemini, error) {
	if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("embedding API key is required")
	}
	if model == "" {
		model = DefaultModel
	}
	model = strings.TrimPrefix(model, "models/")
	if !modelPattern.MatchString(model) {
		return nil, errors.New("invalid embedding model")
	}
	return &Gemini{key: key, model: model, endpoint: "https://generativelanguage.googleapis.com/v1beta/models/" + model + ":batchEmbedContents", client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (g *Gemini) Model() string { return g.model }

func (g *Gemini) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, errors.New("embedding input is empty")
	}
	if len(texts) > 100 {
		return nil, errors.New("embedding batch exceeds 100 texts")
	}
	task := "RETRIEVAL_DOCUMENT"
	if query {
		task = "RETRIEVAL_QUERY"
	}
	requests := make([]any, 0, len(texts))
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("embedding text is empty")
		}
		requests = append(requests, map[string]any{"model": "models/" + g.model, "content": map[string]any{"parts": []any{map[string]string{"text": text}}}, "taskType": task, "outputDimensionality": Dimensions})
	}
	body, _ := json.Marshal(map[string]any{"requests": requests})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("embedding request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.key)
	res, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("embedding request failed")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &ServiceError{Status: res.StatusCode}
	}
	var decoded struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, errors.New("invalid embedding response")
	}
	if len(decoded.Embeddings) != len(texts) {
		return nil, errors.New("embedding response count mismatch")
	}
	out := make([][]float32, len(texts))
	for i, e := range decoded.Embeddings {
		out[i], err = normalize(e.Values)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func normalize(v []float32) ([]float32, error) {
	if len(v) != Dimensions {
		return nil, errors.New("embedding dimension mismatch")
	}
	var sum float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return nil, errors.New("embedding contains non-finite values")
		}
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return nil, errors.New("embedding is zero")
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out, nil
}
