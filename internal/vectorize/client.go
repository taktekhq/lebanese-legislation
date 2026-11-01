// Package vectorize provides a small Cloudflare Vectorize v2 REST client.
package vectorize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Match struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}
type Vector struct {
	ID     string    `json:"id"`
	Values []float32 `json:"values"`
}

// Client is safe for concurrent use. The caller supplies the API token.
type Client struct {
	baseURL, path, token string
	http                 *http.Client
	backoff              time.Duration
}

func New(account, index, token string) (*Client, error) {
	if account == "" || index == "" || token == "" {
		return nil, errors.New("Vectorize requires account, index and token")
	}
	return &Client{baseURL: "https://api.cloudflare.com/client/v4", path: "/accounts/" + url.PathEscape(account) + "/vectorize/v2/indexes/" + url.PathEscape(index), token: token, http: &http.Client{Timeout: 60 * time.Second}, backoff: time.Second}, nil
}

func (c *Client) Query(ctx context.Context, vector []float32, topK int) ([]Match, error) {
	if topK < 1 {
		return nil, errors.New("Vectorize topK must be positive")
	}
	body, err := json.Marshal(struct {
		Vector []float32 `json:"vector"`
		TopK   int       `json:"topK"`
	}{vector, min(topK, 100)})
	if err != nil {
		return nil, err
	}
	var result struct {
		Matches []Match `json:"matches"`
	}
	err = c.request(ctx, "query", "application/json", body, &result)
	return result.Matches, err
}

func (c *Client) Upsert(ctx context.Context, vectors []Vector) error {
	for len(vectors) > 0 {
		n := min(len(vectors), 1000)
		var body bytes.Buffer
		enc := json.NewEncoder(&body)
		for _, v := range vectors[:n] {
			if v.ID == "" || len(v.ID) > 64 {
				return errors.New("Vectorize id must be 1-64 bytes")
			}
			if err := enc.Encode(v); err != nil {
				return err
			}
		}
		if err := c.request(ctx, "upsert", "application/x-ndjson", body.Bytes(), nil); err != nil {
			return err
		}
		vectors = vectors[n:]
	}
	return nil
}

func (c *Client) DeleteByIDs(ctx context.Context, ids []string) error {
	for len(ids) > 0 {
		n := min(len(ids), 1000)
		body, err := json.Marshal(struct {
			IDs []string `json:"ids"`
		}{ids[:n]})
		if err != nil {
			return err
		}
		if err = c.request(ctx, "delete_by_ids", "application/json", body, nil); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

func (c *Client) request(ctx context.Context, operation, contentType string, body []byte, result any) error {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+c.path+"/"+operation, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", contentType)
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("Vectorize %s: %w", operation, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		retry := resp.StatusCode == 429 || resp.StatusCode >= 500
		if retry && attempt < 5 {
			timer := time.NewTimer(c.backoff * time.Duration(1<<attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Vectorize %s: HTTP %d", operation, resp.StatusCode)
		}
		var envelope struct {
			Success bool `json:"success"`
			Errors  []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
			Result json.RawMessage `json:"result"`
		}
		if err = json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("Vectorize %s response: %w", operation, err)
		}
		if !envelope.Success {
			return fmt.Errorf("Vectorize %s: API errors %v", operation, envelope.Errors)
		}
		if result != nil {
			return json.Unmarshal(envelope.Result, result)
		}
		return nil
	}
}
