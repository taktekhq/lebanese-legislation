package nlsearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	if p, err := parse(`{"queries":[]}`); err != nil || len(p.Queries) != 0 {
		t.Fatal("valid out-of-scope response rejected")
	}
	for _, raw := range []string{`{}`, `{"queries":null}`} {
		if _, err := parse(raw); err == nil {
			t.Errorf("accepted missing queries: %s", raw)
		}
	}
	for _, raw := range []string{`{"queries":["employment law"]}`, `{"queries":["قانون العمل"],"answer":"yes"}`, `{"queries":["قانون العمل"]} {}`, `{"queries":["<script>قانون</script>"]}`, `{"queries":["قانون\nالعمل"]}`, `{"queries":["قانون","عمل","تجارة","ضرائب"]}`} {
		if _, err := parse(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	p, err := parse(`{"queries":[" قانون العمل ","قانون العمل","الصرف التعسفي"]}`)
	if err != nil || len(p.Queries) != 2 || p.Queries[0] != "قانون العمل" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestProviderResponses(t *testing.T) {
	for _, provider := range []string{"openai", "gemini", "claude"} {
		t.Run(provider, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "POST" {
					t.Error("not POST")
				}
				switch provider {
				case "openai":
					_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"queries\":[\"قانون العمل\"]}"}]}],"usage":{"input_tokens":20,"output_tokens":10}}`)
				case "gemini":
					_, _ = fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{\"queries\":[\"قانون العمل\"]}"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":10}}`)
				case "claude":
					_, _ = fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"test","content":[{"type":"text","text":"{\"queries\":[\"قانون العمل\"]}"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":10}}`)
				}
			}))
			defer srv.Close()
			c, err := New(Config{Provider: provider, Model: "test", Key: "private-test", Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			p, err := c.Rewrite(context.Background(), "Can I be dismissed?")
			if err != nil || len(p.Queries) != 1 || p.InputTokens != 20 || p.OutputTokens != 10 {
				t.Fatalf("%+v %v", p, err)
			}
		})
	}
}

func TestProviderFailuresAreSafe(t *testing.T) {
	for _, body := range []string{`{"status":"incomplete","output":[]}`, `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
		c, _ := New(Config{Provider: "openai", Model: "test", Key: "SECRET", Endpoint: srv.URL})
		if _, err := c.Rewrite(context.Background(), "question"); err == nil {
			t.Error("accepted incomplete/refusal")
		}
		srv.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); _, _ = fmt.Fprint(w, "SECRET") }))
	defer srv.Close()
	c, _ := New(Config{Provider: "openai", Model: "test", Key: "SECRET", Endpoint: srv.URL})
	_, err := c.Rewrite(context.Background(), "question")
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe error: %v", err)
	}
}
