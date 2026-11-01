package judge

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeCommandLocal(t *testing.T) {
	c := &Claude{Bin: "/bin/claude", Model: "sonnet", Dir: "/tmp"}
	cmd := c.command(context.Background(), "the prompt")
	want := []string{"/bin/claude", "-p", "the prompt", "--model", "sonnet", "--output-format", "text",
		"--setting-sources", "project", "--strict-mcp-config", "--tools", ""}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q", cmd.Args)
	}
	if cmd.Dir != "/tmp" {
		t.Errorf("dir = %q", cmd.Dir)
	}
}

func TestClaudeCommandRemote(t *testing.T) {
	c := &Claude{Bin: "/home/x/.local/bin/claude", Model: "sonnet", Remote: "xps"}
	cmd := c.command(context.Background(), "the prompt")
	if cmd.Args[0] != "ssh" || cmd.Args[3] != "xps" {
		t.Fatalf("args = %q", cmd.Args)
	}
	remote := cmd.Args[len(cmd.Args)-1]
	want := `cd /tmp && '/home/x/.local/bin/claude' -p "$(cat)" '--model' 'sonnet' '--output-format' 'text' '--setting-sources' 'project' '--strict-mcp-config' '--tools' ''`
	if remote != want {
		t.Errorf("remote command = %s", remote)
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
