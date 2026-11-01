package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Asker is a model that answers one prompt with a JSON object.
type Asker interface {
	Ask(ctx context.Context, system, prompt string, schema map[string]any, out any) (string, Usage, error)
}

// Agy asks Gemini through the Antigravity CLI (`agy -p`), on a Google AI
// subscription instead of the paid API. The schema goes into the prompt;
// the answer is the first JSON object in the reply.
type Agy struct {
	Bin     string // path to agy
	Model   string // e.g. "gemini-4-argon-medium"
	Dir     string // where agy runs (it may write scratch files)
	Remote  string // run agy on this ssh host instead (agy is signed in on one machine only); the prompt goes over stdin
	Timeout time.Duration
}

var firstObject = regexp.MustCompile(`(?s)\{.*\}`)

// fullPrompt is the one prompt the CLI backends (agy, claude) send: the
// system text, the item, and the answer schema spelled out in words.
func fullPrompt(system, prompt string, schema map[string]any) string {
	sch, _ := json.Marshal(schema)
	return system + "\n\n" + prompt + "\n\nDo not use any tool, run any command or write any file: answer from the text above alone. " +
		"Answer with ONLY one JSON object, no prose, matching this JSON schema: " + string(sch)
}

func (a *Agy) Ask(ctx context.Context, system, prompt string, schema map[string]any, out any) (string, Usage, error) {
	full := fullPrompt(system, prompt, schema)
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, a.Timeout)
		cmd := exec.CommandContext(cctx, a.Bin, "-p", full, "--model", a.Model, "--print-timeout", "0")
		cmd.Dir = a.Dir
		if a.Remote != "" {
			cmd = exec.CommandContext(cctx, "ssh", "-o", "BatchMode=yes", a.Remote,
				`cd /tmp && `+a.Bin+` -p "$(cat)" --model `+a.Model+` --print-timeout 0`)
			cmd.Stdin = strings.NewReader(full)
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		cancel()
		if err != nil {
			last = fmt.Errorf("agy: %v: %s", err, strings.TrimSpace(stderr.String()))
			continue
		}
		m := firstObject.Find(raw)
		if m == nil {
			last = fmt.Errorf("agy: no JSON in the answer (%d bytes): %s", len(raw), strings.TrimSpace(stderr.String()))
			continue
		}
		if err := json.Unmarshal(m, out); err != nil {
			last = fmt.Errorf("agy: %v", err)
			continue
		}
		return "agy:" + a.Model, Usage{}, nil
	}
	return "", Usage{}, last
}
