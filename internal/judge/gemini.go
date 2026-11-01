package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Gemini calls the Gemini API's generateContent with a JSON answer schema.
type Gemini struct {
	Key   string
	Model string // "gemini-pro-latest"
	HTTP  *http.Client
}

// Usage is what a call cost, in tokens.
type Usage struct {
	In, Out, Thinking int
}

// Ask sends one prompt and decodes the JSON answer into out. It returns the
// model version that answered.
func (g *Gemini) Ask(ctx context.Context, system, prompt string, schema map[string]any, out any) (string, Usage, error) {
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": system}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": prompt}}}},
		"generationConfig": map[string]any{
			"temperature":      0,
			"responseMimeType": "application/json",
			"responseSchema":   schema,
		},
	})
	url := "https://generativelanguage.googleapis.com/v1beta/models/" + g.Model + ":generateContent"
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", Usage{}, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * 2 * time.Second):
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", g.Key)
		resp, err := g.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			last = fmt.Errorf("gemini: %s", resp.Status)
			continue
		}
		if resp.StatusCode != 200 {
			return "", Usage{}, fmt.Errorf("gemini: %s: %.300s", resp.Status, raw)
		}
		var r struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			ModelVersion  string `json:"modelVersion"`
			UsageMetadata struct {
				Prompt   int `json:"promptTokenCount"`
				Answer   int `json:"candidatesTokenCount"`
				Thoughts int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return "", Usage{}, fmt.Errorf("gemini: %w", err)
		}
		u := Usage{r.UsageMetadata.Prompt, r.UsageMetadata.Answer, r.UsageMetadata.Thoughts}
		if len(r.Candidates) == 0 {
			last = fmt.Errorf("gemini: no answer")
			continue
		}
		text := ""
		for _, p := range r.Candidates[0].Content.Parts {
			if !p.Thought {
				text += p.Text
			}
		}
		if err := json.Unmarshal([]byte(text), out); err != nil {
			last = fmt.Errorf("gemini: answer is not the JSON asked for (%s): %w", r.Candidates[0].FinishReason, err)
			continue
		}
		return r.ModelVersion, u, nil
	}
	return "", Usage{}, last
}
