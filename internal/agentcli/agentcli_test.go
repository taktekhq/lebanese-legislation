package agentcli

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvKeepsOnlyTheAllowlist(t *testing.T) {
	in := []string{"PATH=/bin", "HOME=/home/op", "USER=op", "LANG=C.UTF-8", "TMPDIR=/tmp",
		"GH_TOKEN=x", "PGPASSWORD=x", "LEBLEG_DSN=x", "CLOUDFLARE_TOKEN_CMD=x", "SSH_AUTH_SOCK=/s",
		"ANTHROPIC_AUTH_TOKEN=x", "AGY_ACCOUNT=me", "broken"}
	want := []string{"PATH=/bin", "HOME=/home/op", "USER=op", "LANG=C.UTF-8", "TMPDIR=/tmp", "AGY_ACCOUNT=me"}
	if got := Env(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Env = %q, want %q", got, want)
	}
	if got := Env(nil); got == nil || len(got) != 0 {
		t.Errorf("Env(nil) = %#v, want empty non-nil (nil would inherit everything)", got)
	}
}

func TestAgySettings(t *testing.T) {
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(AgySettings("/tmp/ruling-1")), &s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Permissions.Allow, []string{"read_file(/tmp/ruling-1)"}) {
		t.Errorf("allow = %q", s.Permissions.Allow)
	}
	for _, d := range []string{"command(*)", "write_file(*)", "read_url(*)", "execute_url(*)", "mcp(*)"} {
		found := false
		for _, x := range s.Permissions.Deny {
			found = found || x == d
		}
		if !found {
			t.Errorf("deny lacks %s: %q", d, s.Permissions.Deny)
		}
	}
	s.Permissions.Allow = nil
	if err := json.Unmarshal([]byte(AgySettings()), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Permissions.Allow) != 0 {
		t.Errorf("no folders given, allow = %q", s.Permissions.Allow)
	}
}
