package main

import (
	"context"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
)

func TestBackendAgyByDefault(t *testing.T) {
	a := &cite.Agy{Bin: "agy"}
	be, cleanup, err := backend(context.Background(), "", "", "Gemini 3.8 Flash (Low)", time.Minute, a)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if be.Name() != "agy:Gemini 3.8 Flash (Low)" {
		t.Errorf("name = %q", be.Name())
	}
}

func TestBackendClaudeWhenSet(t *testing.T) {
	be, cleanup, err := backend(context.Background(), "/usr/bin/claude", "builder", "", time.Minute, &cite.Agy{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	c, ok := be.(*cite.Claude)
	if !ok {
		t.Fatalf("backend is %T, want claude", be)
	}
	if c.C.Model != "sonnet" || c.C.Remote != "builder" || c.C.Bin != "/usr/bin/claude" || be.Name() != "claude:sonnet" {
		t.Errorf("claude = %+v, name %q", c.C, be.Name())
	}
}

func TestAPIBackend(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	a := apiBackend("", "low", true, true, 4, 16000, time.Second, "u.jsonl", "b.jsonl", "")
	if a.Model != "claude-opus-5-5" || a.Effort != "low" || !a.Batch || !a.Estimate || a.Client != nil || a.Name() != "api:claude-opus-5-5" {
		t.Errorf("api = %+v", a)
	}
	t.Setenv("ANTHROPIC_API_KEY", "k")
	a = apiBackend("claude-sonnet-5-5", "high", false, false, 4, 0, 0, "", "", "")
	if a.Model != "claude-sonnet-5-5" || a.Client == nil || a.Batch {
		t.Errorf("api = %+v", a)
	}
}
