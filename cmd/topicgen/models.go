package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// A family is a model provider the tool can call through its command line.
type family struct {
	name  string
	model string // what the command is told to use
	call  func(ctx context.Context, model, prompt string) (string, error)
}

var families = map[string]*family{
	"claude": {"claude", "sonnet", callClaude},
	"openai": {"openai", "gpt-6-luna", callCodex},
	"gemini": {"gemini", "gemini-4-argon-medium", callGemini},
}

// run starts a command in an empty directory (so no project files, memory
// or settings load) with the prompt on its stdin.
func run(ctx context.Context, prompt string, name string, args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "topicgen-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %.400s", name, err, errb.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("%s: empty answer: %.400s", name, errb.String())
	}
	return out.String(), nil
}

// callClaude: no tools, no connectors, no project settings (they add tens of
// thousands of tokens to every call), prompt on stdin.
func callClaude(ctx context.Context, model, prompt string) (string, error) {
	return run(ctx, prompt, "claude", "-p", "--model", model, "--tools", "", "--strict-mcp-config", "--disable-slash-commands",
		"--setting-sources", "project", "--no-session-persistence", "--system-prompt", "You write and judge Lebanese-law explainers. Reply with exactly what is asked and nothing else.")
}

func callCodex(ctx context.Context, model, prompt string) (string, error) {
	return run(ctx, prompt, "codex", "exec", "--skip-git-repo-check", "--ephemeral", "-s", "read-only", "-m", model, "-")
}

// callGemini sends the prompt as a stream-json user message on stdin: a
// large prompt does not fit in one command-line argument.
func callGemini(ctx context.Context, model, prompt string) (string, error) {
	msg, _ := json.Marshal(map[string]any{"event": "user", "message": map[string]string{
		"content": prompt + "\n\nDo not use any tool, run any command or write any file: answer from the text above alone."}})
	out, err := run(ctx, string(msg)+"\n", "agy", "--model", model, "--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "-p=")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		var ev struct {
			Event  string `json:"event"`
			Result struct {
				Status   string `json:"status"`
				Response string `json:"response"`
				Error    string `json:"error"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "result" {
			if ev.Result.Status != "SUCCESS" || strings.TrimSpace(ev.Result.Response) == "" {
				return "", fmt.Errorf("agy: %s: %s", ev.Result.Status, ev.Result.Error)
			}
			return ev.Result.Response, nil
		}
	}
	return "", fmt.Errorf("agy: no result event")
}

// callFamily retries a call twice: these CLIs now and then return nothing.
func callFamily(ctx context.Context, f *family, prompt string) (string, error) {
	var err error
	for try := 0; try < 3; try++ {
		var out string
		if out, err = f.call(ctx, f.model, prompt); err == nil {
			return out, nil
		}
		time.Sleep(time.Duration(try+1) * 5 * time.Second)
	}
	return "", err
}
