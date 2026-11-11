package nlsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every request to a model is kept as a Generation, for the cost report:
// model, tokens, time, HTTP status. Never the question or the answer.
func TestGenerationsRecordEveryModelCall(t *testing.T) {
	const question = "Can I be dismissed without notice?"
	for _, provider := range []string{"openai", "gemini", "claude"} {
		t.Run("rewrite "+provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch provider {
				case "openai":
					_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"queries\":[\"قانون العمل\"]}"}]}],"usage":{"input_tokens":20,"output_tokens":10}}`)
				case "gemini":
					_, _ = fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{\"queries\":[\"قانون العمل\"]}"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":7,"thoughtsTokenCount":3}}`)
				case "claude":
					_, _ = fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"{\"queries\":[\"قانون العمل\"]}"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":10}}`)
				}
			}))
			defer srv.Close()
			c, _ := New(Config{Provider: provider, Model: "model-a", Key: "k", Endpoint: srv.URL})
			ctx, u := WithUsage(context.Background())
			if _, err := c.Rewrite(ctx, question); err != nil {
				t.Fatal(err)
			}
			gens := u.Generations()
			if len(gens) != 1 {
				t.Fatalf("generations: %+v", gens)
			}
			g := gens[0]
			if g.Kind != "rewrite" || g.Provider != provider || g.Model != "model-a" || g.In != 20 || g.Out != 10 || g.Status != 200 || g.Failed || g.Latency <= 0 {
				t.Errorf("generation: %+v", g)
			}
			if strings.Contains(fmt.Sprintf("%+v", g), "dismissed") {
				t.Errorf("a generation holds the question: %+v", g)
			}
		})
		t.Run("refused "+provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
			}))
			defer srv.Close()
			c, _ := New(Config{Provider: provider, Model: "model-a", Key: "k", Endpoint: srv.URL})
			ctx, u := WithUsage(context.Background())
			if _, err := c.Rewrite(ctx, question); err == nil {
				t.Fatal("a 429 was accepted")
			}
			gens := u.Generations()
			if len(gens) != 1 || gens[0].Status != 429 || !gens[0].Failed || gens[0].In != 0 || gens[0].Out != 0 {
				t.Errorf("generations: %+v", gens)
			}
			// A failed call is a generation, not a completed call.
			if calls, _, _ := u.Totals(); calls != 0 {
				t.Errorf("completed calls: %d", calls)
			}
		})
	}

	t.Run("no response", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		c, _ := New(Config{Provider: "gemini", Model: "model-a", Key: "k", Endpoint: srv.URL})
		ctx, u := WithUsage(context.Background())
		if _, err := c.Rewrite(ctx, question); err == nil {
			t.Fatal("a dead endpoint answered")
		}
		if gens := u.Generations(); len(gens) != 1 || gens[0].Status != 0 || !gens[0].Failed {
			t.Errorf("generations: %+v", gens)
		}
	})

	t.Run("expand and compose", func(t *testing.T) {
		reply := `{"queries":["مهلة الادعاء المدني"]}`
		usage := map[string]any{"promptTokenCount": 300, "candidatesTokenCount": 40, "thoughtsTokenCount": 5}
		finish := "STOP"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"usageMetadata": usage,
				"candidates": []any{map[string]any{"finishReason": finish, "content": map[string]any{"parts": []any{map[string]any{"text": reply}}}}}})
		}))
		defer srv.Close()
		c, _ := New(Config{Provider: "gemini", Model: "small", SelectModel: "large", Key: "k", Endpoint: srv.URL})
		ctx, u := WithUsage(context.Background())
		if _, err := c.Expand(ctx, "q", nil, nil); err != nil {
			t.Fatal(err)
		}
		reply = `{"paragraphs":[{"text":"نعم.","quotes":[{"source":"s1","quote":"نص المادة كاملا"}]}]}`
		passages := []Passage{{ID: "s1", Text: "نص المادة كاملا هنا"}}
		if _, err := c.Compose(ctx, "هل؟", passages); err != nil {
			t.Fatal(err)
		}
		// Cut short by the token limit: paid for, but not an answer.
		finish = "MAX_TOKENS"
		if _, err := c.Compose(ctx, "هل؟", passages); err == nil {
			t.Fatal("an unfinished answer was accepted")
		}
		gens := u.Generations()
		if len(gens) != 3 {
			t.Fatalf("generations: %+v", gens)
		}
		// The phrases come from the rewriting model, the answer from the answer model.
		if g := gens[0]; g.Kind != "expand" || g.Model != "small" || g.In != 300 || g.Out != 45 || g.Status != 200 || g.Failed {
			t.Errorf("expand: %+v", g)
		}
		if g := gens[1]; g.Kind != "compose" || g.Model != "large" || g.In != 300 || g.Out != 45 || g.Status != 200 || g.Failed {
			t.Errorf("compose: %+v", g)
		}
		if g := gens[2]; g.Kind != "compose" || g.Status != 200 || !g.Failed {
			t.Errorf("unfinished compose: %+v", g)
		}
		if calls, in, out := u.Totals(); calls != 2 || in != 600 || out != 90 {
			t.Errorf("totals changed: %d calls, %d in, %d out", calls, in, out)
		}
	})

	// Without a Usage on the context nothing is kept and nothing breaks.
	g := startGeneration(context.Background(), "rewrite", "gemini", "m")
	g.end()
}
