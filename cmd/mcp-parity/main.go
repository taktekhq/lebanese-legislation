// Command mcp-parity runs MCP tool calls against the Go server
// (internal/mcpserver) over a Postgres corpus and prints each answer, and
// with -remote also sends them to another MCP endpoint (the Worker) and
// reports where the two answers differ.
//
//	go run ./cmd/mcp-parity -dsn "$DATABASE_URL" -calls calls.jsonl [-remote https://lebaneselegislation.com/mcp] [-out dir]
//
// calls.jsonl holds one call per line: {"name": "get_law", "arguments": {"id": 244565}}.
// Every remote call is a real request (and is counted in that server's
// analytics): keep the list short.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/mcpserver"
)

type call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "Postgres connection string (connected read-only)")
	callsPath := flag.String("calls", "", "file of calls, one JSON object per line")
	remote := flag.String("remote", "", "another MCP endpoint to compare with")
	outDir := flag.String("out", "", "directory to write each answer to (local-N.json, remote-N.json)")
	slow := flag.Duration("slow", 0, "print every query slower than this")
	flag.Parse()
	if *dsn == "" || *callsPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	calls, err := readCalls(*callsPath)
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		fail(err)
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	if *slow > 0 {
		cfg.ConnConfig.Tracer = slowLog{*slow}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fail(err)
	}
	defer pool.Close()
	h := mcpserver.New(pool, mcpserver.Config{RateLimitPerMinute: -1})
	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			fail(err)
		}
	}
	same := 0
	for i, c := range calls {
		body := request(i, c)
		started := time.Now()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))
		local := toolText(rec.Body.Bytes())
		took := time.Since(started).Round(time.Millisecond)
		save(*outDir, fmt.Sprintf("local-%02d.json", i), local)
		if *remote == "" {
			fmt.Printf("== %d %s %s (%s)\n%s\n", i, c.Name, c.Arguments, took, local)
			continue
		}
		rb, err := post(*remote, body)
		if err != nil {
			fmt.Printf("== %d %s %s: remote: %v\n", i, c.Name, c.Arguments, err)
			continue
		}
		remoteText := toolText(rb)
		save(*outDir, fmt.Sprintf("remote-%02d.json", i), remoteText)
		diffs := compare(local, remoteText)
		if len(diffs) == 0 {
			same++
			fmt.Printf("== %d %s %s (%s): same\n", i, c.Name, c.Arguments, took)
			continue
		}
		fmt.Printf("== %d %s %s (%s): %d differences\n", i, c.Name, c.Arguments, took, len(diffs))
		for j, d := range diffs {
			if j == 15 {
				fmt.Printf("   ... and %d more\n", len(diffs)-j)
				break
			}
			fmt.Println("   " + d)
		}
	}
	if *remote != "" {
		fmt.Printf("%d of %d calls answered the same\n", same, len(calls))
	}
}

// slowLog prints the queries that took longer than its threshold.
type slowLog struct{ min time.Duration }

type traceKey struct{}

type traceStart struct {
	sql   string
	start time.Time
}

func (l slowLog) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, traceKey{}, traceStart{d.SQL, time.Now()})
}

func (l slowLog) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if t, ok := ctx.Value(traceKey{}).(traceStart); ok {
		if took := time.Since(t.start); took >= l.min {
			q := strings.Join(strings.Fields(t.sql), " ")
			if r := []rune(q); len(r) > 300 {
				q = string(r[:120]) + " ... " + string(r[len(r)-180:])
			}
			fmt.Fprintf(os.Stderr, "   slow query %s: %s\n", took.Round(time.Millisecond), q)
		}
	}
}

func readCalls(path string) ([]call, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var calls []call
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var c call
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("%s: %w", line, err)
		}
		if len(c.Arguments) == 0 {
			c.Arguments = json.RawMessage("{}")
		}
		calls = append(calls, c)
	}
	return calls, sc.Err()
}

func request(i int, c call) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
		"params": map[string]any{"name": c.Name, "arguments": c.Arguments}})
	return b
}

func post(u string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lebaneselegislation-mcp-parity/1.0")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, b)
	}
	return b, err
}

// toolText is the tool answer's text (itself JSON), or the whole JSON-RPC
// response when it is an error.
func toolText(b []byte) string {
	var r struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &r) == nil && len(r.Result.Content) > 0 {
		return r.Result.Content[0].Text
	}
	return string(b)
}

func save(dir, name, text string) {
	if dir == "" {
		return
	}
	var v any
	out := []byte(text)
	if json.Unmarshal([]byte(text), &v) == nil {
		out, _ = json.MarshalIndent(v, "", "  ")
	}
	_ = os.WriteFile(filepath.Join(dir, name), out, 0o644)
}

// compare lists the paths where two JSON texts differ, and says when only
// the order of keys differs.
func compare(a, b string) []string {
	var va, vb any
	if json.Unmarshal([]byte(a), &va) != nil || json.Unmarshal([]byte(b), &vb) != nil {
		if a == b {
			return nil
		}
		return []string{"not JSON, and the texts differ"}
	}
	var out []string
	walk("", va, vb, &out)
	if len(out) == 0 && a != b {
		out = append(out, "same values, different key order or spacing")
	}
	return out
}

func walk(path string, a, b any, out *[]string) {
	if reflect.DeepEqual(a, b) {
		return
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: %s vs %s", path, short(a), short(b)))
			return
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			va, ina := x[k]
			vb, inb := y[k]
			switch {
			case !ina:
				*out = append(*out, fmt.Sprintf("%s.%s: only remote: %s", path, k, short(vb)))
			case !inb:
				*out = append(*out, fmt.Sprintf("%s.%s: only local: %s", path, k, short(va)))
			default:
				walk(path+"."+k, va, vb, out)
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: %s vs %s", path, short(a), short(b)))
			return
		}
		if len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: %d items locally, %d remotely", path, len(x), len(y)))
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			walk(fmt.Sprintf("%s[%d]", path, i), x[i], y[i], out)
		}
	default:
		*out = append(*out, fmt.Sprintf("%s: %s vs %s", path, short(a), short(b)))
	}
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120]) + "..."
	}
	return s
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mcp-parity:", err)
	os.Exit(1)
}
