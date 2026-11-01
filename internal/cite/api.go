package cite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// The Claude API backend. It reads the same items with the same prompt text,
// answer schema and quote check as the CLI backends (agy, claude -p), but
// through the Messages API, optionally as a Message Batch (half price, up to
// a day). The instructions go in `system` with a cache breakpoint so every
// request shares the prefix; the items and the answer format go in the user
// turn; the answer is structured output (output_config.format), so there is
// no JSON to guess at. The model is recorded as "api:<model>".

// DefaultAPIModel is the model -api reads with unless told otherwise.
const DefaultAPIModel = "claude-opus-5-5"

// Prices in US dollars per million tokens (input, output). Cache reads cost
// 10% of the input price, cache writes 125% of it (the 5-minute rate); a
// batch halves all of them.
var apiPrices = map[string][2]float64{
	"claude-opus-5-5":   {4, 20},
	"claude-sonnet-5-5": {2, 10},
	"claude-haiku-5-5":  {0.10, 0.50},
}

func priceOf(model string) ([2]float64, bool) {
	best, found := "", false
	for name := range apiPrices {
		if strings.HasPrefix(model, name) && len(name) > len(best) {
			best, found = name, true
		}
	}
	return apiPrices[best], found
}

// APIUsage is what a run used, in tokens.
type APIUsage struct {
	Requests, In, Out, CacheRead, CacheCreate int64
}

func (u *APIUsage) add(o APIUsage) {
	u.Requests += o.Requests
	u.In += o.In
	u.Out += o.Out
	u.CacheRead += o.CacheRead
	u.CacheCreate += o.CacheCreate
}

// Cost is the price of the usage in dollars; false when the model's prices are not known.
func (u APIUsage) Cost(model string, batch bool) (float64, bool) {
	p, ok := priceOf(model)
	if !ok {
		return 0, false
	}
	usd := (float64(u.In)*p[0] + float64(u.CacheRead)*p[0]*0.1 + float64(u.CacheCreate)*p[0]*1.25 + float64(u.Out)*p[1]) / 1e6
	if batch {
		usd /= 2
	}
	return usd, true
}

// API reads through the Claude API.
type API struct {
	Client    *anthropic.Client
	Model     string        // e.g. "claude-opus-5-5"
	Effort    string        // output_config.effort: low, medium, high, xhigh, max
	MaxTokens int64         // per request (default 16000)
	Batch     bool          // submit as Message Batches and poll
	Workers   int           // requests in flight at once, when not a batch
	Poll      time.Duration // between looks at a batch (default 30s)
	UsageLog  string        // one JSON line per request ("" for none)
	BatchLog  string        // where submitted batches are kept so a run can be collected later ("" for none)
	BatchID   string        // collect this batch instead of submitting one (needs BatchLog)
	Estimate  bool          // count tokens, ask nothing
}

// Name is the model as recorded in each answer.
func (a *API) Name() string { return "api:" + a.Model }

func (a *API) maxTokens() int64 {
	if a.MaxTokens > 0 {
		return a.MaxTokens
	}
	return 16000
}

// jsonSchema turns the answer schema (written for Gemini: upper-case types,
// a few required fields) into JSON Schema for structured output: lower-case
// types, every property required, no property beyond those listed.
func jsonSchema(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			switch k {
			case "required":
				continue // recomputed below
			case "type":
				if s, ok := val.(string); ok {
					out[k] = strings.ToLower(s)
					continue
				}
			case "properties":
				if props, ok := val.(map[string]any); ok {
					ps := map[string]any{}
					for pk, pv := range props {
						ps[pk] = jsonSchema(pv)
					}
					out[k] = ps
					continue
				}
			}
			out[k] = jsonSchema(val)
		}
		if props, ok := out["properties"].(map[string]any); ok && out["type"] == "object" {
			names := make([]string, 0, len(props))
			for n := range props {
				names = append(names, n)
			}
			sort.Strings(names)
			out["required"] = names
			out["additionalProperties"] = false
		}
		return out
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = jsonSchema(x[i])
		}
		return out
	}
	return v
}

// apiSchema is the schema of a whole answer: {"items":[{"key", "amends",
// "changes":[...]}]}, the shape the CLI backends are asked for.
func apiSchema(src Source) map[string]any {
	item := jsonSchema(schema).(map[string]any)
	props := item["properties"].(map[string]any)
	props["key"] = map[string]any{"type": "string"}
	item["required"] = append([]string{"key"}, item["required"].([]string)...)
	sort.Strings(item["required"].([]string))
	if src.Actions != nil {
		action := props["changes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)
		action["enum"] = append([]string(nil), src.Actions...)
	}
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"items": map[string]any{"type": "array", "items": item}},
		"required":             []string{"items"},
		"additionalProperties": false,
	}
}

// params builds one request: the instructions as the cached system prompt,
// the items and the answer format as the one user turn. No prefill, and no
// `thinking` field (claude-opus-5-5 refuses thinking disabled); effort is
// output_config.effort.
func (a *API) params(src Source, items []Item, maxTokens int64) anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:     anthropic.Model(a.Model),
		MaxTokens: maxTokens,
		System:    []anthropic.TextBlockParam{{Text: src.System, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(src.user(items)))},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(a.Effort),
			Format: anthropic.JSONOutputFormatParam{Schema: apiSchema(src)},
		},
	}
}

func batchParams(p anthropic.MessageNewParams) anthropic.MessageBatchNewParamsRequestParams {
	return anthropic.MessageBatchNewParamsRequestParams{
		Model: p.Model, MaxTokens: p.MaxTokens, System: p.System, Messages: p.Messages, OutputConfig: p.OutputConfig,
	}
}

// apiReq is one request: items read together, under an id that identifies it
// in a batch (custom_id: letters, digits, - and _).
type apiReq struct {
	ID        string
	Items     []Item
	MaxTokens int64
}

// apiOut is what came back for a request.
type apiOut struct {
	req     apiReq
	msg     *anthropic.Message
	err     error
	batchID string
}

// usageLine is one line of the usage log.
type usageLine struct {
	At          string   `json:"at"`
	Model       string   `json:"model"`
	Mode        string   `json:"mode"` // sync or batch
	ID          string   `json:"custom_id"`
	BatchID     string   `json:"batch_id,omitempty"`
	Keys        []string `json:"keys"`
	In          int64    `json:"input_tokens"`
	Out         int64    `json:"output_tokens"`
	CacheRead   int64    `json:"cache_read_input_tokens"`
	CacheCreate int64    `json:"cache_creation_input_tokens"`
	Stop        string   `json:"stop_reason,omitempty"`
	Err         string   `json:"error,omitempty"`
}

// runner holds what one RunAPI keeps while it reads.
type runner struct {
	a      *API
	src    Source
	have   map[string]Citation
	path   string
	st     *Stats
	log    func(string, ...any)
	usage  APIUsage
	logf   *os.File
	dirty  int
	lastSv time.Time
	fatal  error
	cancel context.CancelFunc
}

func (r *runner) mode() string {
	if r.a.Batch {
		return "batch"
	}
	return "sync"
}

// save writes the citations when enough has come in since the last write, or
// when force is set, so a long run keeps what it has.
func (r *runner) save(force bool) {
	if r.dirty == 0 || (!force && r.dirty < 20 && time.Since(r.lastSv) < 5*time.Second) {
		return
	}
	if err := Save(r.path, r.have); err != nil {
		r.log("save: %v", err)
		return
	}
	r.dirty, r.lastSv = 0, time.Now()
}

func (r *runner) writeUsage(o apiOut) {
	if r.a.UsageLog == "" {
		return
	}
	if r.logf == nil {
		if err := os.MkdirAll(filepath.Dir(r.a.UsageLog), 0o755); err != nil {
			r.log("usage log: %v", err)
			return
		}
		f, err := os.OpenFile(r.a.UsageLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			r.log("usage log: %v", err)
			return
		}
		r.logf = f
	}
	l := usageLine{At: time.Now().UTC().Format(time.RFC3339), Model: r.a.Model, Mode: r.mode(), ID: o.req.ID, BatchID: o.batchID}
	for _, it := range o.req.Items {
		l.Keys = append(l.Keys, it.Key)
	}
	if o.msg != nil {
		u := o.msg.Usage
		l.In, l.Out, l.CacheRead, l.CacheCreate = u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens
		l.Stop = string(o.msg.StopReason)
	}
	if o.err != nil {
		l.Err = o.err.Error()
	}
	b, _ := json.Marshal(l)
	r.logf.Write(append(b, '\n'))
}

// text is the model's answer: its text blocks joined.
func messageText(m *anthropic.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// handle takes one request's outcome: usage is counted, answers are kept,
// and the items that got none (an error, a refusal, a cut-off answer, a
// missing key) come back as requests of one item each when retry is set,
// else they are counted as failed.
func (r *runner) handle(o apiOut, retry bool) []apiReq {
	r.writeUsage(o)
	var why string
	var got map[string]answer
	switch {
	case o.err != nil:
		why = o.err.Error()
		var ae *anthropic.Error
		if errors.As(o.err, &ae) && (ae.StatusCode == 401 || ae.StatusCode == 403) {
			r.fatal = fmt.Errorf("the API refused the key: %v", o.err)
			r.cancel()
		}
	case o.msg == nil:
		why = "no message"
	default:
		r.usage.add(APIUsage{Requests: 1, In: o.msg.Usage.InputTokens, Out: o.msg.Usage.OutputTokens,
			CacheRead: o.msg.Usage.CacheReadInputTokens, CacheCreate: o.msg.Usage.CacheCreationInputTokens})
		switch o.msg.StopReason {
		case anthropic.StopReasonRefusal:
			why = "the model refused"
		case anthropic.StopReasonMaxTokens:
			why = fmt.Sprintf("the answer ran past max_tokens (%d)", o.req.MaxTokens)
		default:
			var err error
			if got, err = parseBatch(messageText(o.msg)); err != nil {
				why = err.Error()
			}
		}
	}
	var again []apiReq
	for i, it := range o.req.Items {
		if ans, ok := got[it.Key]; ok {
			r.st.record(r.have, r.src, it, r.a.Name(), ans)
			r.dirty++
			continue
		}
		w := why
		if w == "" {
			w = "the answer has no entry for it"
		}
		if r.fatal != nil || !retry {
			r.st.Failed++
			r.log("%s (%s): failed: %s", it.Key, o.req.ID, w)
			continue
		}
		mt := o.req.MaxTokens
		if o.msg != nil && o.msg.StopReason == anthropic.StopReasonMaxTokens {
			mt = min(mt*2, r.a.retryCap())
		}
		r.log("%s (%s): %s; asking again alone", it.Key, o.req.ID, w)
		again = append(again, apiReq{ID: fmt.Sprintf("%s-%d", o.req.ID, i), Items: []Item{it}, MaxTokens: mt})
	}
	r.save(false)
	return again
}

// retryCap is the most max_tokens a second try may ask for: a request that is
// not a batch must finish inside the SDK's limit for non-streaming calls.
func (a *API) retryCap() int64 {
	if a.Batch {
		return 64000
	}
	return 20000
}

// runSync sends the requests a few at a time.
func (r *runner) runSync(ctx context.Context, reqs []apiReq, out chan<- apiOut) {
	jobs := make(chan apiReq)
	var wg sync.WaitGroup
	for w := 0; w < max(r.a.Workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for q := range jobs {
				msg, err := r.a.Client.Messages.New(ctx, r.a.params(r.src, q.Items, q.MaxTokens))
				out <- apiOut{req: q, msg: msg, err: err}
			}
		}()
	}
	for _, q := range reqs {
		select {
		case jobs <- q:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
}

// maxBatchRequests keeps a batch well inside the API's limit of 100,000
// requests.
const maxBatchRequests = 50000

// batchRecord is a submitted batch, kept so its answers can be collected by a
// later run if this one is cut short.
type batchRecord struct {
	BatchID  string              `json:"batch_id"`
	Model    string              `json:"model"`
	At       string              `json:"at"`
	Requests map[string][]string `json:"requests"` // custom_id -> the items' keys
}

func (r *runner) rememberBatch(rec batchRecord) {
	if r.a.BatchLog == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.a.BatchLog), 0o755); err != nil {
		r.log("batch log: %v", err)
		return
	}
	f, err := os.OpenFile(r.a.BatchLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		r.log("batch log: %v", err)
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
}

func findBatch(path, id string) (batchRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return batchRecord{}, err
	}
	for _, l := range strings.Split(string(data), "\n") {
		var rec batchRecord
		if json.Unmarshal([]byte(l), &rec) == nil && rec.BatchID == id {
			return rec, nil
		}
	}
	return batchRecord{}, fmt.Errorf("batch %s is not in %s", id, path)
}

// runBatch submits the requests as Message Batches, waits for each to end and
// sends every result out as it is read.
func (r *runner) runBatch(ctx context.Context, reqs []apiReq, out chan<- apiOut) error {
	for len(reqs) > 0 {
		n := min(len(reqs), maxBatchRequests)
		chunk := reqs[:n]
		reqs = reqs[n:]
		var body []anthropic.MessageBatchNewParamsRequest
		rec := batchRecord{Model: r.a.Model, At: time.Now().UTC().Format(time.RFC3339), Requests: map[string][]string{}}
		for _, q := range chunk {
			body = append(body, anthropic.MessageBatchNewParamsRequest{CustomID: q.ID, Params: batchParams(r.a.params(r.src, q.Items, q.MaxTokens))})
			for _, it := range q.Items {
				rec.Requests[q.ID] = append(rec.Requests[q.ID], it.Key)
			}
		}
		b, err := r.a.Client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: body})
		if err != nil {
			return fmt.Errorf("submit batch: %w", err)
		}
		rec.BatchID = b.ID
		r.rememberBatch(rec)
		r.log("batch %s submitted: %d requests", b.ID, len(chunk))
		byID := map[string]apiReq{}
		for _, q := range chunk {
			byID[q.ID] = q
		}
		if err := r.collect(ctx, b.ID, byID, out); err != nil {
			return err
		}
	}
	return nil
}

// collect waits for a batch to end, then reads its results into out.
func (r *runner) collect(ctx context.Context, id string, byID map[string]apiReq, out chan<- apiOut) error {
	poll := r.a.Poll
	if poll <= 0 {
		poll = 30 * time.Second
	}
	for {
		b, err := r.a.Client.Messages.Batches.Get(ctx, id, anthropic.MessageBatchGetParams{})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.log("batch %s: %v; looking again", id, err)
		} else {
			c := b.RequestCounts
			r.log("batch %s %s: %d processing, %d succeeded, %d errored, %d canceled, %d expired", id, b.ProcessingStatus, c.Processing, c.Succeeded, c.Errored, c.Canceled, c.Expired)
			if b.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded {
				break
			}
		}
		select {
		case <-time.After(poll):
		case <-ctx.Done():
			return fmt.Errorf("stopped while batch %s was running; collect it later with -batch-id %s", id, id)
		}
	}
	stream := r.a.Client.Messages.Batches.ResultsStreaming(ctx, id, anthropic.MessageBatchResultsParams{})
	defer stream.Close()
	seen := map[string]bool{}
	for stream.Next() {
		res := stream.Current()
		q, ok := byID[res.CustomID]
		if !ok {
			r.log("batch %s: a result for unknown custom_id %q", id, res.CustomID)
			continue
		}
		seen[res.CustomID] = true
		o := apiOut{req: q, batchID: id}
		switch res.Result.Type {
		case "succeeded":
			m := res.Result.AsSucceeded().Message
			o.msg = &m
		case "errored":
			e := res.Result.AsErrored().Error
			o.err = fmt.Errorf("batch request errored: %s", e.RawJSON())
		default:
			o.err = fmt.Errorf("batch request %s", res.Result.Type)
		}
		out <- o
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("batch %s results: %w", id, err)
	}
	for cid, q := range byID {
		if !seen[cid] {
			out <- apiOut{req: q, batchID: id, err: errors.New("no result in the batch")}
		}
	}
	return nil
}

// RunAPI reads, through the Claude API, the items not yet read with the
// source's prompt and input (an answer by any model counts; redo asks again
// about those a model whose name contains it answered). Answers are saved to
// path as they arrive. The items go several to a call (batch), and what
// fails, is refused or is cut off is asked again once, alone. It prints a
// cost summary at the end.
func RunAPI(ctx context.Context, a *API, src Source, items []Item, path string, batch, limit int, redo string, log func(string, ...any)) (Stats, error) {
	st := Stats{Keep: strings.HasPrefix(redo, "keep:")}
	have, err := Load(path)
	if err != nil {
		return st, err
	}
	todo := selectTodo(have, items, src, redo, limit, &st)
	mt := a.maxTokens()
	var reqs []apiReq
	for i, b := range batches(todo, max(batch, 1), src.MaxBytes, src.Part) {
		reqs = append(reqs, apiReq{ID: fmt.Sprintf("r%06d", i+1), Items: b, MaxTokens: mt})
	}
	log("%d %s to read through %s in %d requests (%s), %d already read", len(todo), src.Name, a.Name(), len(reqs), map[bool]string{true: "as a batch", false: "one at a time"}[a.Batch], st.Skipped)
	if a.Estimate {
		return st, a.estimate(ctx, src, reqs, log)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &runner{a: a, src: src, have: have, path: path, st: &st, log: log, cancel: cancel, lastSv: time.Now()}
	defer func() {
		if r.logf != nil {
			r.logf.Close()
		}
	}()
	start := time.Now()
	round := reqs
	for attempt := 0; attempt < 2 && len(round) > 0 && r.fatal == nil; attempt++ {
		out := make(chan apiOut)
		var execErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer close(out)
			if a.Batch {
				if attempt == 0 && a.BatchID != "" {
					execErr = r.collectKnown(ctx, round, out)
				} else {
					execErr = r.runBatch(ctx, round, out)
				}
			} else {
				r.runSync(ctx, round, out)
			}
		}()
		var next []apiReq
		for o := range out {
			next = append(next, r.handle(o, attempt == 0)...)
			st.Calls++
			log("%d read, %d failed, %d changes (%d with a quote not in the text)", st.Asked, st.Failed, st.Changes, st.Invalid)
		}
		<-done
		if execErr != nil {
			r.save(true)
			r.summary(time.Since(start))
			return st, execErr
		}
		round = next
	}
	r.save(true)
	r.summary(time.Since(start))
	return st, r.fatal
}

// collectKnown reads the results of a batch submitted earlier (-batch-id):
// the requests are the ones the batch log recorded, matched to the items now
// selected by key.
func (r *runner) collectKnown(ctx context.Context, current []apiReq, out chan<- apiOut) error {
	rec, err := findBatch(r.a.BatchLog, r.a.BatchID)
	if err != nil {
		return err
	}
	byKey := map[string]Item{}
	for _, q := range current {
		for _, it := range q.Items {
			byKey[it.Key] = it
		}
	}
	byID := map[string]apiReq{}
	for cid, keys := range rec.Requests {
		q := apiReq{ID: cid, MaxTokens: r.a.maxTokens()}
		for _, k := range keys {
			if it, ok := byKey[k]; ok {
				q.Items = append(q.Items, it)
			}
		}
		if len(q.Items) > 0 {
			byID[cid] = q
		}
	}
	r.log("collecting batch %s: %d requests of %s", rec.BatchID, len(byID), rec.At)
	return r.collect(ctx, rec.BatchID, byID, out)
}

// summary prints the tokens used and what they cost.
func (r *runner) summary(took time.Duration) {
	u := r.usage
	r.log("%d requests, %d input tokens (%d read from the cache, %d written to it), %d output tokens, %s",
		u.Requests, u.In, u.CacheRead, u.CacheCreate, u.Out, took.Round(time.Second))
	if usd, ok := u.Cost(r.a.Model, r.a.Batch); ok {
		r.log("cost: $%.2f%s", usd, map[bool]string{true: " (batch price)", false: ""}[r.a.Batch])
	} else {
		r.log("cost: no prices known for %s", r.a.Model)
	}
}

// estimate counts the tokens of 20 requests spread over the selection with
// the free count_tokens endpoint and projects them to all of it. Without a
// key (or when counting fails) it prints the characters instead. Nothing is
// asked of the model.
func (a *API) estimate(ctx context.Context, src Source, reqs []apiReq, log func(string, ...any)) error {
	if len(reqs) == 0 {
		log("estimate: nothing to read")
		return nil
	}
	var items, chars int
	sysChars := len([]rune(src.System))
	for _, q := range reqs {
		items += len(q.Items)
		chars += sysChars + len([]rune(src.user(q.Items)))
	}
	log("estimate: %d requests, %d items, %d characters to send (instructions %d characters each time)", len(reqs), items, chars, sysChars)
	sample := sampleReqs(reqs, 20)
	var tokens, sampleChars int64
	var n int
	var lastErr error
	if a.Client != nil {
		for _, q := range sample {
			p := a.params(src, q.Items, q.MaxTokens)
			c, err := a.Client.Messages.CountTokens(ctx, anthropic.MessageCountTokensParams{
				Model: p.Model, Messages: p.Messages, OutputConfig: p.OutputConfig,
				System: anthropic.MessageCountTokensParamsSystemUnion{OfTextBlockArray: p.System},
			})
			if err != nil {
				lastErr = err
				break
			}
			tokens += c.InputTokens
			sampleChars += int64(sysChars + len([]rune(src.user(q.Items))))
			n++
		}
	}
	if n == 0 {
		if lastErr != nil {
			log("estimate: count_tokens failed (%v); characters only", lastErr)
		} else {
			log("estimate: no API key, so no token count; characters only")
		}
		return nil
	}
	perReq := float64(tokens) / float64(n)
	proj := perReq * float64(len(reqs))
	log("estimate: counted %d of %d requests, %.0f input tokens each (%.2f characters per token)", n, len(reqs), perReq, float64(sampleChars)/float64(tokens))
	log("estimate: projected input for the whole selection: %.0f tokens", math.Round(proj))
	if p, ok := priceOf(a.Model); ok {
		usd := proj * p[0] / 1e6
		log("estimate: input alone costs $%.2f, or $%.2f as a batch; the output is billed on top at $%.2f per million tokens ($%.2f as a batch)", usd, usd/2, p[1], p[1]/2)
	}
	return nil
}

// sampleReqs picks n requests spread evenly over reqs.
func sampleReqs(reqs []apiReq, n int) []apiReq {
	if len(reqs) <= n {
		return reqs
	}
	out := make([]apiReq, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, reqs[i*len(reqs)/n])
	}
	return out
}

// requestKey is a short stable name for a set of items, for logs.
func requestKey(items []Item) string {
	h := sha256.New()
	for _, it := range items {
		h.Write([]byte(it.Key + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil)[:4])
}

var _ = amend.FoldString
