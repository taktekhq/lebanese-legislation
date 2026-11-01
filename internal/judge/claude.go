package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Claude asks a Claude model through the Claude Code CLI (`claude -p`), on a
// subscription instead of the paid API. It takes the same prompt as Agy and
// parses the answer the same way, so Gemini and Claude judgements are
// interchangeable; the model is recorded as "claude:<model>" in each one.
type Claude struct {
	Bin     string // path to claude
	Model   string // alias or full name, e.g. "sonnet"
	Dir     string // where claude runs
	Remote  string // run claude on this ssh host instead; the prompt goes over stdin
	Timeout time.Duration
}

// claudeFlags follow the prompt, which must come right after -p: a variadic
// flag placed before it would swallow the prompt. No tools, no MCP servers.
func claudeFlags(model string) []string {
	return []string{"--model", model, "--output-format", "text", "--setting-sources", "project", "--strict-mcp-config", "--tools", ""}
}

// shellQuote single-quotes s for a remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// command builds the process to run for one prompt. Locally the prompt is an
// argument; over ssh it goes on stdin and the remote shell reads it back with
// $(cat), which keeps it clear of quoting.
func (c *Claude) command(ctx context.Context, full string) *exec.Cmd {
	flags := claudeFlags(c.Model)
	if c.Remote == "" {
		cmd := exec.CommandContext(ctx, c.Bin, append([]string{"-p", full}, flags...)...)
		cmd.Dir = c.Dir
		return cmd
	}
	q := make([]string, len(flags))
	for i, f := range flags {
		q[i] = shellQuote(f)
	}
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", c.Remote,
		"cd /tmp && "+shellQuote(c.Bin)+` -p "$(cat)" `+strings.Join(q, " "))
	cmd.Stdin = strings.NewReader(full)
	return cmd
}

// parseClaude decodes the first JSON object in a claude reply into out.
func parseClaude(raw []byte, out any) error {
	m := firstObject.Find(raw)
	if m == nil {
		return fmt.Errorf("no JSON in the answer (%d bytes): %.200s", len(raw), strings.TrimSpace(string(raw)))
	}
	return json.Unmarshal(m, out)
}

func (c *Claude) Ask(ctx context.Context, system, prompt string, schema map[string]any, out any) (string, Usage, error) {
	model, err := c.Raw(ctx, fullPrompt(system, prompt, schema), out)
	return model, Usage{}, err
}

// Raw sends one finished prompt and decodes the first JSON object of the
// reply into out, trying up to three times. It returns "claude:<model>".
func (c *Claude) Raw(ctx context.Context, full string, out any) (string, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, c.Timeout)
		cmd := c.command(cctx, full)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		cancel()
		if err != nil {
			// A limit message comes on stdout with exit 1; keep both so the log shows it.
			last = fmt.Errorf("claude: %v: %s %s", err, strings.TrimSpace(stderr.String()), strings.TrimSpace(string(raw)))
			continue
		}
		if err := parseClaude(raw, out); err != nil {
			last = fmt.Errorf("claude: %v", err)
			continue
		}
		return "claude:" + c.Model, nil
	}
	return "", last
}
