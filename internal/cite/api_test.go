package cite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// fakeAPI stands in for api.anthropic.com: messages, batches (create,
// retrieve, results) and count_tokens. The tests never reach the real one.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	messages []map[string]any // bodies of POST /v1/messages
	creates  []map[string]any // bodies of POST /v1/messages/batches
	counts   int              // POST /v1/messages/count_tokens
	gets     int              // GET of a batch
	other    []string

	// reply decides the answer to one request: the stop reason, the text and
	// the usage. nil: every key read, one valid change each.
	reply func(keys []string, body map[string]any) (stop string, text string)
	// endAfter is how many looks at a batch find it still in progress.
	endAfter int
}

var keyLine = regexp.MustCompile(`### key: (\S+)`)

func userText(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		return ""
	}
	m, _ := msgs[len(msgs)-1].(map[string]any)
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, x := range c {
			if blk, ok := x.(map[string]any); ok {
				b.WriteString(fmt.Sprint(blk["text"]))
			}
		}
		return b.String()
	}
	return ""
}

func keysOf(body map[string]any) []string {
	var keys []string
	for _, m := range keyLine.FindAllStringSubmatch(userText(body), -1) {
		keys = append(keys, m[1])
	}
	return keys
}

const goodQuote = "المادة 73 من القانون رقم 328"

func answerFor(keys []string) string {
	var items []map[string]any
	for _, k := range keys {
		items = append(items, map[string]any{"key": k, "amends": false, "changes": []any{map[string]any{
			"action": "applies", "kind": "law", "number": "328", "date": "", "year": 0, "name": "", "self": false,
			"article": "73", "part": "", "quote": goodQuote, "why": "applied"}}})
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return string(b)
}

func message(stop, text string, in, out, cacheRead, cacheCreate int) map[string]any {
	return map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
		"content":     []any{map[string]any{"type": "text", "text": text}},
		"stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": in, "output_tokens": out, "cache_read_input_tokens": cacheRead,
			"cache_creation_input_tokens": cacheCreate, "cache_creation": map[string]any{}, "service_tier": "standard"}}
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) client() *anthropic.Client {
	c := anthropic.NewClient(option.WithBaseURL(f.srv.URL+"/"), option.WithAPIKey("test-key"), option.WithMaxRetries(0))
	return &c
}

func (f *fakeAPI) respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (f *fakeAPI) result(stop string, keys []string, body map[string]any) map[string]any {
	text, st := answerFor(keys), "end_turn"
	if f.reply != nil {
		st, text = f.reply(keys, body)
	}
	return message(st, text, 1000, 200, 0, 0)
}

func (f *fakeAPI) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	json.Unmarshal(raw, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Api-Key") != "test-key" {
		f.other = append(f.other, "bad key on "+r.URL.Path)
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/messages":
		f.messages = append(f.messages, body)
		f.respond(w, f.result("end_turn", keysOf(body), body))
	case r.Method == "POST" && r.URL.Path == "/v1/messages/count_tokens":
		f.counts++
		f.respond(w, map[string]any{"input_tokens": 4000})
	case r.Method == "POST" && r.URL.Path == "/v1/messages/batches":
		f.creates = append(f.creates, body)
		f.respond(w, f.batch("in_progress"))
	case r.Method == "GET" && r.URL.Path == "/v1/messages/batches/msgbatch_1":
		f.gets++
		if f.gets > f.endAfter {
			f.respond(w, f.batch("ended"))
		} else {
			f.respond(w, f.batch("in_progress"))
		}
	case r.Method == "GET" && r.URL.Path == "/v1/messages/batches/msgbatch_1/results":
		w.Header().Set("Content-Type", "application/x-jsonl")
		reqs, _ := f.creates[0]["requests"].([]any)
		for _, q := range reqs {
			qm := q.(map[string]any)
			params := qm["params"].(map[string]any)
			line, _ := json.Marshal(map[string]any{"custom_id": qm["custom_id"],
				"result": map[string]any{"type": "succeeded", "message": f.result("end_turn", keysOf(params), params)}})
			w.Write(append(line, '\n'))
		}
	default:
		f.other = append(f.other, r.Method+" "+r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) batch(status string) map[string]any {
	n := 0
	if len(f.creates) > 0 {
		n = len(f.creates[0]["requests"].([]any))
	}
	counts := map[string]any{"processing": n, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0}
	if status == "ended" {
		counts = map[string]any{"processing": 0, "succeeded": n, "errored": 0, "canceled": 0, "expired": 0}
	}
	b := map[string]any{"id": "msgbatch_1", "type": "message_batch", "processing_status": status, "request_counts": counts,
		"created_at": "2026-10-22T10:00:00Z", "expires_at": "2026-10-23T10:00:00Z", "ended_at": nil, "archived_at": nil,
		"cancel_initiated_at": nil, "results_url": nil}
	if status == "ended" {
		b["ended_at"] = "2026-10-22T10:05:00Z"
		b["results_url"] = f.srv.URL + "/v1/messages/batches/msgbatch_1/results"
	}
	return b
}

func testItems(n int) []Item {
	var out []Item
	for i := 1; i <= n; i++ {
		out = append(out, Item{Key: RulingKey(i), Input: fmt.Sprintf("h%d", i), LawID: i, Identity: "محكمة رقم 1/2020", Title: "حكم",
			Text: fmt.Sprintf("عملا باحكام %s وبناء عليه %d", goodQuote, i)})
	}
	return out
}

func testAPI(f *fakeAPI, dir string) *API {
	return &API{Client: f.client(), Model: DefaultAPIModel, Effort: "low", Workers: 2, Poll: time.Millisecond,
		UsageLog: filepath.Join(dir, "api-usage.jsonl"), BatchLog: filepath.Join(dir, "api-batches.jsonl")}
}

func quiet(logs *[]string) func(string, ...any) {
	var mu sync.Mutex
	return func(f string, a ...any) {
		mu.Lock()
		*logs = append(*logs, fmt.Sprintf(f, a...))
		mu.Unlock()
	}
}

func hasLog(logs []string, sub string) bool {
	for _, l := range logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestJSONSchemaForStructuredOutput(t *testing.T) {
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "object" {
				props, _ := x["properties"].(map[string]any)
				if x["additionalProperties"] != false {
					t.Errorf("%s: additionalProperties = %v", path, x["additionalProperties"])
				}
				var req []string
				switch r := x["required"].(type) {
				case []string:
					req = r
				case []any:
					for _, n := range r {
						req = append(req, n.(string))
					}
				}
				if len(req) != len(props) {
					t.Errorf("%s: required %v for %d properties", path, req, len(props))
				}
				for _, n := range req {
					if _, ok := props[n]; !ok {
						t.Errorf("%s: required %q is not a property", path, n)
					}
				}
			}
			if s, ok := x["type"].(string); ok && s != strings.ToLower(s) {
				t.Errorf("%s: type %q", path, s)
			}
			for k, c := range x {
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), c)
			}
		}
	}
	for _, src := range []Source{Articles, Rulings} {
		sch := apiSchema(src)
		walk(src.Name, sch)
		b, _ := json.Marshal(sch)
		var round map[string]any
		if err := json.Unmarshal(b, &round); err != nil {
			t.Fatal(err)
		}
		walk(src.Name+"(json)", round)
		item := round["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
		if _, ok := item["properties"].(map[string]any)["key"]; !ok {
			t.Errorf("%s: no key in an item", src.Name)
		}
		action := item["properties"].(map[string]any)["changes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)
		enum := action["enum"].([]any)
		if src.Name == "rulings" && len(enum) != 4 {
			t.Errorf("rulings actions = %v", enum)
		}
		if src.Name == "articles" && len(enum) != 14 {
			t.Errorf("articles actions = %v", enum)
		}
	}
	// The schema the other backends use is left alone.
	if schema["type"] != "OBJECT" {
		t.Error("the shared schema was changed")
	}
}

func TestCost(t *testing.T) {
	u := APIUsage{In: 1_000_000, Out: 1_000_000}
	for _, c := range []struct {
		model string
		batch bool
		want  float64
	}{
		{"claude-opus-5-5", false, 24}, {"claude-opus-5-5", true, 12}, {"claude-sonnet-5-5", false, 12}, {"claude-haiku-5-5", false, 0.6}, {"claude-haiku-5-5-20260101", true, 0.3},
	} {
		got, ok := u.Cost(c.model, c.batch)
		if !ok || got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s batch=%v: %v %v, want %v", c.model, c.batch, got, ok, c.want)
		}
	}
	if got, _ := (APIUsage{CacheRead: 1_000_000}).Cost("claude-opus-5-5", false); got != 0.4 {
		t.Errorf("cache reads cost %v, want 10%% of $4", got)
	}
	if _, ok := u.Cost("claude-unknown", false); ok {
		t.Error("a price for an unknown model")
	}
}

func TestRunAPIAsksAndRecords(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "rulings.jsonl")
	var logs []string
	st, err := RunAPI(context.Background(), testAPI(f, dir), Rulings, testItems(5), out, 2, 0, "", quiet(&logs))
	if err != nil {
		t.Fatal(err)
	}
	if st.Asked != 5 || st.Failed != 0 || st.Changes != 5 || st.Invalid != 0 {
		t.Errorf("stats = %+v", st)
	}
	if len(f.messages) != 3 { // 5 rulings, 2 to a call
		t.Fatalf("%d requests, want 3", len(f.messages))
	}
	for _, body := range f.messages {
		if body["model"] != "claude-opus-5-5" {
			t.Errorf("model = %v", body["model"])
		}
		if _, ok := body["thinking"]; ok {
			t.Error("thinking was sent")
		}
		sys := body["system"].([]any)[0].(map[string]any)
		if sys["text"] != Rulings.System || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
			t.Errorf("system = %v", sys)
		}
		oc := body["output_config"].(map[string]any)
		if oc["effort"] != "low" || oc["format"].(map[string]any)["type"] != "json_schema" || oc["format"].(map[string]any)["schema"] == nil {
			t.Errorf("output_config = %v", oc)
		}
		msgs := body["messages"].([]any)
		if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" { // no assistant prefill
			t.Errorf("messages = %v", msgs)
		}
		text := userText(body)
		if !strings.HasPrefix(text, "The rulings:") || !strings.HasSuffix(text, Rulings.Format) {
			t.Errorf("user turn = %.80q ...", text)
		}
		if body["max_tokens"].(float64) != 16000 {
			t.Errorf("max_tokens = %v", body["max_tokens"])
		}
	}
	have, _ := Load(out)
	if len(have) != 5 {
		t.Fatalf("%d citations saved", len(have))
	}
	c := have["ruling/3"]
	if c.Model != "api:claude-opus-5-5" || c.Prompt != RulingPrompt || len(c.Changes) != 1 || !c.Changes[0].Valid || c.Changes[0].Target.Article != "73" {
		t.Errorf("citation = %+v", c)
	}
	// One line of usage for each request, and a cost.
	data, _ := os.ReadFile(filepath.Join(dir, "api-usage.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("usage log has %d lines:\n%s", len(lines), data)
	}
	var u usageLine
	json.Unmarshal([]byte(lines[0]), &u)
	if u.In != 1000 || u.Out != 200 || u.Mode != "sync" || u.Model != "claude-opus-5-5" || len(u.Keys) == 0 {
		t.Errorf("usage line = %+v", u)
	}
	if !hasLog(logs, "3 requests, 3000 input tokens") || !hasLog(logs, "cost: $0.02") { // 3000*4/1e6 + 600*20/1e6 = 0.024
		t.Errorf("no cost summary in %v", logs)
	}
	if len(f.other) != 0 {
		t.Errorf("unexpected calls: %v", f.other)
	}
}

func TestRunAPIChecksQuotes(t *testing.T) {
	f := newFake(t)
	f.reply = func(keys []string, _ map[string]any) (string, string) {
		return "end_turn", strings.ReplaceAll(answerFor(keys), goodQuote, "عبارة ليست في النص")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "r.jsonl")
	var logs []string
	st, err := RunAPI(context.Background(), testAPI(f, dir), Rulings, testItems(2), out, 8, 0, "", quiet(&logs))
	if err != nil || st.Invalid != 2 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
	have, _ := Load(out)
	if have["ruling/1"].Changes[0].Valid {
		t.Error("a quote that is not in the text is valid")
	}
}

func TestRunAPIResumeSkipsAnsweredByAnyModel(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "r.jsonl")
	items := testItems(4)
	if err := Save(out, map[string]Citation{
		"ruling/1": {Key: "ruling/1", Input: items[0].Input, Prompt: RulingPrompt, Model: "agy:Gemini"},
		"ruling/2": {Key: "ruling/2", Input: items[1].Input, Prompt: RulingPrompt, Model: "claude:sonnet"},
		"ruling/3": {Key: "ruling/3", Input: "an older text", Prompt: RulingPrompt, Model: "agy:Gemini"}, // its text changed
	}); err != nil {
		t.Fatal(err)
	}
	var logs []string
	st, err := RunAPI(context.Background(), testAPI(f, dir), Rulings, items, out, 8, 0, "", quiet(&logs))
	if err != nil {
		t.Fatal(err)
	}
	if st.Skipped != 2 || st.Asked != 2 {
		t.Errorf("stats = %+v", st)
	}
	var asked []string
	for _, b := range f.messages {
		asked = append(asked, keysOf(b)...)
	}
	if strings.Join(asked, ",") != "ruling/3,ruling/4" {
		t.Errorf("asked %v", asked)
	}
	have, _ := Load(out)
	if have["ruling/1"].Model != "agy:Gemini" || have["ruling/3"].Model != "api:claude-opus-5-5" {
		t.Errorf("models: %q %q", have["ruling/1"].Model, have["ruling/3"].Model)
	}
	// -limit takes the first N of what is left.
	f2 := newFake(t)
	out2 := filepath.Join(dir, "r2.jsonl")
	st, _ = RunAPI(context.Background(), testAPI(f2, dir), Rulings, testItems(6), out2, 2, 3, "", quiet(&logs))
	if st.Asked != 3 {
		t.Errorf("limit 3 read %d", st.Asked)
	}
}

func TestRunAPIRefusalAndMaxTokensRetriedAlone(t *testing.T) {
	f := newFake(t)
	f.reply = func(keys []string, body map[string]any) (string, string) {
		switch {
		case len(keys) == 2 && keys[0] == "ruling/1": // the first pair is refused
			return "refusal", ""
		case len(keys) == 2 && keys[0] == "ruling/3": // the second is cut off
			return "max_tokens", `{"items":[{"key":"ruling/3"`
		case len(keys) == 1 && keys[0] == "ruling/2": // refused again alone: failed
			return "refusal", ""
		case len(keys) == 1 && keys[0] == "ruling/4":
			if body["max_tokens"].(float64) != 20000 {
				t.Errorf("a cut-off answer retried with max_tokens %v, want it raised to the 20000 a non-batch call may ask", body["max_tokens"])
			}
		}
		return "end_turn", answerFor(keys)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "r.jsonl")
	var logs []string
	a := testAPI(f, dir)
	a.Workers = 1
	st, err := RunAPI(context.Background(), a, Rulings, testItems(4), out, 2, 0, "", quiet(&logs))
	if err != nil {
		t.Fatal(err)
	}
	if st.Asked != 3 || st.Failed != 1 {
		t.Errorf("stats = %+v", st)
	}
	have, _ := Load(out)
	if _, ok := have["ruling/2"]; ok {
		t.Error("a refused ruling was kept")
	}
	for _, k := range []string{"ruling/1", "ruling/3", "ruling/4"} {
		if _, ok := have[k]; !ok {
			t.Errorf("%s was not read after the retry", k)
		}
	}
	if len(f.messages) != 2+4 { // two pairs, then four alone
		t.Errorf("%d requests", len(f.messages))
	}
	if !hasLog(logs, "the model refused; asking again alone") || !hasLog(logs, "ran past max_tokens") {
		t.Errorf("logs: %v", logs)
	}
}

func TestRunAPIStopsOnAuthError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	defer srv.Close()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL+"/"), option.WithAPIKey("bad"), option.WithMaxRetries(0))
	dir := t.TempDir()
	a := &API{Client: &c, Model: DefaultAPIModel, Effort: "low", Workers: 1}
	var logs []string
	_, err := RunAPI(context.Background(), a, Rulings, testItems(6), filepath.Join(dir, "r.jsonl"), 1, 0, "", quiet(&logs))
	if err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("err = %v", err)
	}
	if calls > 2 {
		t.Errorf("%d calls after a 401", calls)
	}
}

func TestRunAPIBatch(t *testing.T) {
	f := newFake(t)
	f.endAfter = 2
	dir := t.TempDir()
	out := filepath.Join(dir, "r.jsonl")
	a := testAPI(f, dir)
	a.Batch = true
	var logs []string
	st, err := RunAPI(context.Background(), a, Rulings, testItems(5), out, 2, 0, "", quiet(&logs))
	if err != nil {
		t.Fatal(err)
	}
	if st.Asked != 5 || st.Failed != 0 {
		t.Errorf("stats = %+v", st)
	}
	if len(f.messages) != 0 || len(f.creates) != 1 {
		t.Fatalf("%d direct requests, %d batches", len(f.messages), len(f.creates))
	}
	if f.gets < 3 {
		t.Errorf("batch looked at %d times, want it polled until ended", f.gets)
	}
	reqs := f.creates[0]["requests"].([]any)
	if len(reqs) != 3 {
		t.Fatalf("%d requests in the batch", len(reqs))
	}
	seen := map[string]bool{}
	for _, q := range reqs {
		qm := q.(map[string]any)
		id := qm["custom_id"].(string)
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(id) || seen[id] {
			t.Errorf("custom_id %q", id)
		}
		seen[id] = true
		p := qm["params"].(map[string]any)
		if _, ok := p["thinking"]; ok || p["output_config"].(map[string]any)["effort"] != "low" ||
			p["system"].([]any)[0].(map[string]any)["cache_control"] == nil {
			t.Errorf("params = %v", p)
		}
	}
	have, _ := Load(out)
	if len(have) != 5 || have["ruling/5"].Model != "api:claude-opus-5-5" {
		t.Errorf("%d citations; ruling/5 by %q", len(have), have["ruling/5"].Model)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "api-usage.jsonl"))
	if !strings.Contains(string(data), `"mode":"batch"`) || !strings.Contains(string(data), `"batch_id":"msgbatch_1"`) {
		t.Errorf("usage log:\n%s", data)
	}
	if !hasLog(logs, "(batch price)") || !hasLog(logs, "cost: $0.01") { // 3000 in, 600 out: $0.024 halved
		t.Errorf("logs: %v", logs)
	}
	rec, err := findBatch(a.BatchLog, "msgbatch_1")
	if err != nil || len(rec.Requests) != 3 {
		t.Errorf("batch log: %+v %v", rec, err)
	}

	// A run cut short collects the batch later by id, without submitting again.
	out2 := filepath.Join(dir, "r2.jsonl")
	b := testAPI(f, dir)
	b.Batch, b.BatchID = true, "msgbatch_1"
	st, err = RunAPI(context.Background(), b, Rulings, testItems(5), out2, 2, 0, "", quiet(&logs))
	if err != nil || st.Asked != 5 || len(f.creates) != 1 {
		t.Errorf("collect: st=%+v err=%v batches=%d", st, err, len(f.creates))
	}
}

func TestEstimateCountsAndAsksNothing(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	a := testAPI(f, dir)
	a.Estimate = true
	var logs []string
	if _, err := RunAPI(context.Background(), a, Rulings, testItems(100), filepath.Join(dir, "r.jsonl"), 2, 0, "", quiet(&logs)); err != nil {
		t.Fatal(err)
	}
	if f.counts != 20 || len(f.messages) != 0 || len(f.creates) != 0 {
		t.Errorf("count_tokens %d, messages %d, batches %d", f.counts, len(f.messages), len(f.creates))
	}
	if !hasLog(logs, "50 requests, 100 items") || !hasLog(logs, "4000 input tokens each") || !hasLog(logs, "200000 tokens") {
		t.Errorf("logs: %v", logs)
	}
	if _, err := os.Stat(filepath.Join(dir, "r.jsonl")); err == nil {
		t.Error("an estimate wrote the output file")
	}

	// Without a key: characters only, no call at all.
	f2 := newFake(t)
	a = testAPI(f2, dir)
	a.Estimate, a.Client = true, nil
	logs = nil
	if _, err := RunAPI(context.Background(), a, Rulings, testItems(10), filepath.Join(dir, "r.jsonl"), 2, 0, "", quiet(&logs)); err != nil {
		t.Fatal(err)
	}
	if f2.counts != 0 || !hasLog(logs, "characters only") || !hasLog(logs, "characters to send") {
		t.Errorf("counts=%d logs=%v", f2.counts, logs)
	}
}

func TestUserPromptMatchesCLIPrompt(t *testing.T) {
	items := testItems(2)
	for _, src := range []Source{Articles, Rulings} {
		want := "Ignore all earlier messages in this conversation; answer only this one.\n\n" + src.System + "\n\n" + src.user(items)
		if got := src.prompt(items); got != want {
			t.Errorf("%s: the CLI prompt is no longer system + user", src.Name)
		}
	}
}
