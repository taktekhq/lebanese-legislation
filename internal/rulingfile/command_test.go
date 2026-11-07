package rulingfile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/agentcli"
)

func TestReaderCommand(t *testing.T) {
	t.Setenv("GH_TOKEN", "secret")
	t.Setenv("PGPASSWORD", "secret")
	t.Setenv("HOME", "/home/op")
	dir := t.TempDir()
	r := &Reader{Agy: "/bin/agy", Model: "gemini-3.8-flash-low"}
	cmd := r.command(context.Background(), dir, "the prompt")
	want := []string{"/bin/agy", "-p", "the prompt", "--model", "gemini-3.8-flash-low", "--print-timeout", "0",
		"--output-format", "json", "--disable-slash-commands", "--settings", agentcli.AgySettings(dir)}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q", cmd.Args)
	}
	for _, a := range cmd.Args {
		if a == "--dangerously-skip-permissions" {
			t.Error("the reader must not skip permissions")
		}
	}
	if !strings.Contains(cmd.Args[len(cmd.Args)-1], `"read_file(`+dir+`)"`) {
		t.Errorf("settings do not grant the page folder: %s", cmd.Args[len(cmd.Args)-1])
	}
	if cmd.Dir != dir {
		t.Errorf("dir = %q, want %q", cmd.Dir, dir)
	}
	if cmd.Env == nil {
		t.Fatal("env is inherited whole")
	}
	env := strings.Join(cmd.Env, "\n")
	if strings.Contains(env, "GH_TOKEN") || strings.Contains(env, "PGPASSWORD") {
		t.Errorf("env leaks secrets: %q", cmd.Env)
	}
	if !strings.Contains(env, "HOME=/home/op") {
		t.Errorf("env lacks HOME")
	}
}

func TestClaudeReadCommand(t *testing.T) {
	cmd := claudeReadCommand(context.Background(), "/bin/claude", "sonnet", "the prompt", "/tmp/claude-ruling-1")
	want := []string{"/bin/claude", "-p", "the prompt", "--model", "sonnet", "--allowedTools", "Read",
		"--strict-mcp-config", "--setting-sources", "", "--output-format", "json"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q", cmd.Args)
	}
	if cmd.Dir != "/tmp/claude-ruling-1" {
		t.Errorf("dir = %q", cmd.Dir)
	}
}

func TestLinkOrCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "p-1.jpg")
	if err := os.WriteFile(src, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "p-1.jpg")
	if err := linkOrCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "jpg" {
		t.Errorf("dst = %q", b)
	}
	if err := linkOrCopy(filepath.Join(t.TempDir(), "missing.jpg"), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("a missing page must fail")
	}
}
