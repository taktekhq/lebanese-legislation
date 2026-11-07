// Package agentcli holds the limits every local model CLI run gets (agy, the
// Antigravity CLI, and claude, the Claude Code CLI). These CLIs read text and
// images that come from third-party sites, so each run starts in a private
// empty folder, with only the environment it needs to start and sign in, and
// with its tools turned off or narrowed by a CLI setting, not by the prompt.
package agentcli

import (
	"encoding/json"
	"strings"
)

// envKeep are the variables a CLI needs to start, find its login under HOME,
// reach the network and print UTF-8. GEMINI_API_KEY and
// GOOGLE_GEMINI_BASE_URL are agy's documented API-key sign-in (agy
// changelog); AGY_* are agy's own switches (account, gateway).
var envKeep = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TZ": true, "TMPDIR": true,
	"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_RUNTIME_DIR": true,
	"HTTPS_PROXY": true, "HTTP_PROXY": true, "NO_PROXY": true, "https_proxy": true, "http_proxy": true, "no_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	"GEMINI_API_KEY": true, "GOOGLE_GEMINI_BASE_URL": true,
}

var envKeepPrefix = []string{"AGY_"}

// Env keeps from environ (os.Environ() form) only the variables in the
// allowlist, so tokens, database passwords and other secrets of the
// operator's shell never reach the agent.
func Env(environ []string) []string {
	out := []string{}
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		keep := envKeep[k]
		for _, p := range envKeepPrefix {
			keep = keep || strings.HasPrefix(k, p)
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

// AgyDeny are the agy permission rules every run denies: no commands, no
// file writes, no web, no MCP tools. agy honours explicit deny rules in
// headless -p runs, even under --dangerously-skip-permissions (agy 1.5
// changelog).
var AgyDeny = []string{"command(*)", "write_file(*)", "read_url(*)", "execute_url(*)", "mcp(*)"}

// AgySettings is the value of agy's --settings flag (session-only settings,
// never saved): AgyDeny, and read access to the given folders only. In -p
// mode a tool that needs a permission nobody granted is denied, since there
// is no one to ask.
func AgySettings(readDirs ...string) string {
	perm := map[string][]string{"deny": AgyDeny}
	for _, d := range readDirs {
		perm["allow"] = append(perm["allow"], "read_file("+d+")")
	}
	b, _ := json.Marshal(map[string]any{"permissions": perm})
	return string(b)
}

// RemoteCd is the shell prefix that moves a remote command into a new
// private empty folder (mktemp -d makes it 0700), never a shared one like /tmp.
const RemoteCd = `cd "$(mktemp -d)" && `
