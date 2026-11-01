package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIValidatesBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "index DSN is required"},
		{[]string{"-index-dsn", "postgres://localhost/index"}, "corpus DSN is required"},
		{[]string{"-index-dsn", "postgres://localhost/index", "-corpus-dsn", "postgres://localhost/corpus"}, "GEMINI_API_KEY"},
		{[]string{"-limit", "-1"}, "bounds"},
		{[]string{"-group", "0"}, "bounds"},
		{[]string{"-group", "1001"}, "bounds"},
	} {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")
		t.Setenv("DATABASE_URL", "")
		t.Setenv("SEARCH_DATABASE_URL", "")
		var out bytes.Buffer
		err := run(context.Background(), tc.args, &out)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v => %v", tc.args, err)
		}
	}
}

func TestUsageDoesNotPrintEnvironmentCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://reader:do-not-print-corpus@localhost/db")
	t.Setenv("SEARCH_DATABASE_URL", "postgres://writer:do-not-print-index@localhost/db")
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-unknown-option"}, &out); err == nil {
		t.Fatal("unknown flag accepted")
	}
	if strings.Contains(out.String(), "do-not-print") {
		t.Fatal("usage exposed environment credentials")
	}
}
