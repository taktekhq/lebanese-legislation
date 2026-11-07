package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/agentcli"
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
	Dir     string // parent of the private empty folder each local call runs in ("" is the system temp folder)
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

// runDir makes the private empty folder one local CLI call runs in, under
// parent ("" is the system temp folder). A remote call makes its own there.
func runDir(remote, parent, pattern string) (string, error) {
	if remote != "" {
		return "", nil
	}
	return os.MkdirTemp(parent, pattern)
}

func removeDir(dir string) {
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// agyFlags follow the prompt: no slash commands, and agentcli's deny rules
// (no commands, writes, web or MCP). No folder is granted, so file tools
// outside the empty run folder need a permission -p mode cannot give.
func agyFlags(model string) []string {
	return []string{"--model", model, "--print-timeout", "0", "--disable-slash-commands", "--settings", agentcli.AgySettings()}
}

// command builds one agy run. The prompt holds scraped third-party text, so
// locally agy runs in dir, a private empty folder, with a minimal
// environment. Over ssh it runs in a new mktemp -d folder there; the remote
// agy (1.3 on the Mac) has no --settings flag yet, so only the folder and the
// prompt limit it.
func (a *Agy) command(ctx context.Context, full, dir string) *exec.Cmd {
	if a.Remote != "" {
		cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", a.Remote,
			agentcli.RemoteCd+a.Bin+` -p "$(cat)" --model `+shellQuote(a.Model)+` --print-timeout 0`)
		cmd.Stdin = strings.NewReader(full)
		return cmd
	}
	cmd := exec.CommandContext(ctx, a.Bin, append([]string{"-p", full}, agyFlags(a.Model)...)...)
	cmd.Dir = dir
	cmd.Env = agentcli.Env(os.Environ())
	return cmd
}

func (a *Agy) Ask(ctx context.Context, system, prompt string, schema map[string]any, out any) (string, Usage, error) {
	full := fullPrompt(system, prompt, schema)
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, a.Timeout)
		dir, err := runDir(a.Remote, a.Dir, "judge-agy-")
		if err != nil {
			cancel()
			return "", Usage{}, err
		}
		cmd := a.command(cctx, full, dir)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		raw, err := cmd.Output()
		cancel()
		removeDir(dir)
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
