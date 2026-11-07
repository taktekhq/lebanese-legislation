package judge

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/agentcli"
)

func TestClaudeCommandLocal(t *testing.T) {
	c := &Claude{Bin: "/bin/claude", Model: "sonnet", Dir: "/tmp"}
	cmd := c.command(context.Background(), "the prompt", "/tmp/judge-claude-123")
	want := []string{"/bin/claude", "-p", "the prompt", "--model", "sonnet", "--output-format", "text",
		"--setting-sources", "", "--strict-mcp-config", "--tools", ""}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q", cmd.Args)
	}
	if cmd.Dir != "/tmp/judge-claude-123" {
		t.Errorf("dir = %q, want the private run folder", cmd.Dir)
	}
}

func TestClaudeCommandRemote(t *testing.T) {
	c := &Claude{Bin: "/home/x/.local/bin/claude", Model: "sonnet", Remote: "xps"}
	cmd := c.command(context.Background(), "the prompt", "")
	if cmd.Args[0] != "ssh" || cmd.Args[3] != "xps" {
		t.Fatalf("args = %q", cmd.Args)
	}
	remote := cmd.Args[len(cmd.Args)-1]
	want := `cd "$(mktemp -d)" && '/home/x/.local/bin/claude' -p "$(cat)" '--model' 'sonnet' '--output-format' 'text' '--setting-sources' '' '--strict-mcp-config' '--tools' ''`
	if remote != want {
		t.Errorf("remote command = %s", remote)
	}
	if strings.Contains(remote, "/tmp") {
		t.Error("the remote command must not run in the shared /tmp")
	}
	if strings.Contains(remote, "the prompt") {
		t.Error("the prompt must go over stdin, not the command line")
	}
	b := make([]byte, 20)
	n, _ := cmd.Stdin.Read(b)
	if string(b[:n]) != "the prompt" {
		t.Errorf("stdin = %q", b[:n])
	}
}

func TestRunDirIsPrivateAndEmpty(t *testing.T) {
	parent := t.TempDir()
	dir, err := runDir("", parent, "judge-claude-")
	if err != nil {
		t.Fatal(err)
	}
	defer removeDir(dir)
	if filepath.Dir(dir) != parent || dir == parent {
		t.Errorf("dir = %q, want a new folder under %q", dir, parent)
	}
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, %v; want 0700", fi.Mode().Perm(), err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("dir holds %d entries", len(ents))
	}
	other, _ := runDir("", parent, "judge-claude-")
	defer removeDir(other)
	if other == dir {
		t.Error("each call needs its own folder")
	}
	if d, err := runDir("xps", parent, "judge-claude-"); d != "" || err != nil {
		t.Errorf("remote run made a local folder %q (%v)", d, err)
	}
}

func TestAgyCommandLocal(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("PGPASSWORD", "secret")
	t.Setenv("HOME", "/home/op")
	a := &Agy{Bin: "/bin/agy", Model: "gemini-4-argon-medium"}
	cmd := a.command(context.Background(), "the prompt", "/tmp/judge-agy-1")
	want := []string{"/bin/agy", "-p", "the prompt", "--model", "gemini-4-argon-medium", "--print-timeout", "0",
		"--disable-slash-commands", "--settings", agentcli.AgySettings()}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q", cmd.Args)
	}
	for _, a := range cmd.Args {
		if a == "--dangerously-skip-permissions" {
			t.Error("judge agy must not skip permissions")
		}
	}
	if cmd.Dir != "/tmp/judge-agy-1" {
		t.Errorf("dir = %q", cmd.Dir)
	}
	if cmd.Env == nil {
		t.Fatal("env is inherited whole")
	}
	env := strings.Join(cmd.Env, "\n")
	if strings.Contains(env, "GH_TOKEN") || strings.Contains(env, "PGPASSWORD") {
		t.Errorf("env leaks secrets: %q", cmd.Env)
	}
	if !strings.Contains(env, "HOME=/home/op") {
		t.Errorf("env lacks HOME: %q", cmd.Env)
	}
}

func TestAgyCommandRemote(t *testing.T) {
	a := &Agy{Bin: "agy", Model: "gemini-4-argon-medium", Remote: "mac"}
	cmd := a.command(context.Background(), "the prompt", "")
	if cmd.Args[0] != "ssh" || cmd.Args[3] != "mac" {
		t.Fatalf("args = %q", cmd.Args)
	}
	remote := cmd.Args[len(cmd.Args)-1]
	want := `cd "$(mktemp -d)" && agy -p "$(cat)" --model 'gemini-4-argon-medium' --print-timeout 0`
	if remote != want {
		t.Errorf("remote command = %s", remote)
	}
	if strings.Contains(remote, "the prompt") {
		t.Error("the prompt must go over stdin")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("got %s", got)
	}
}

func TestParseClaude(t *testing.T) {
	var ans struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	for _, raw := range []string{
		`{"state":"includes","reason":"The text carries it."}`,
		"```json\n{\"state\":\"includes\",\"reason\":\"The text carries it.\"}\n```\n",
		"Here is my answer:\n{\"state\":\"includes\",\"reason\":\"The text carries it.\"}",
	} {
		ans.State, ans.Reason = "", ""
		if err := parseClaude([]byte(raw), &ans); err != nil {
			t.Errorf("%q: %v", raw, err)
			continue
		}
		if ans.State != "includes" || ans.Reason != "The text carries it." {
			t.Errorf("%q: got %+v", raw, ans)
		}
	}
	if err := parseClaude([]byte("You have hit your session limit"), &ans); err == nil {
		t.Error("a reply without JSON must fail")
	}
	if err := parseClaude([]byte(`{"state": }`), &ans); err == nil {
		t.Error("broken JSON must fail")
	}
}

func TestFullPromptHasSchema(t *testing.T) {
	p := fullPrompt("SYS", "ITEM", schemaState)
	for _, s := range []string{"SYS", "ITEM", "ONLY one JSON object", `"includes"`} {
		if !strings.Contains(p, s) {
			t.Errorf("prompt lacks %q", s)
		}
	}
}
