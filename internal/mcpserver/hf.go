package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The Hugging Face dataset's public counters, for the downloads and "last
// updated" badges. The MCP server sends no analytics events (Nizar,
// 23 Nov 2026: no PostHog, no custom events).

// HFDataset is the dataset whose counters the badges read.
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
