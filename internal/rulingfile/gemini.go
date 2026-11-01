package rulingfile

// The Gemini stage: handwritten and badly faded pages, and pages the Claude
// stage sent up, read by Gemini through the paid API in Batch mode (half the
// price of a normal call, answered within a day). Every page costs money, so
// the stage keeps a ledger and never submits work whose estimated cost would
// take the total past Budget.
//
// State lives in <dir>/gemini/: batches.json (every batch, its requests and
// state) and spend.tsv (the tokens and cost of each answered request).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const geminiAPI = "https://generativelanguage.googleapis.com"

const geminiPrompt = `This PDF holds pages %s of one Lebanese court judgment, in Arabic, often handwritten, in that order. Transcribe each of these pages exactly as written, line by line in reading order, including the header box and margin notes (prefix margin lines with [هامش]). Start each page with a line "=== صفحة N ===" using its page number. Write [؟] for any word you cannot read; never guess, correct, complete or summarize. Output only the transcription of these pages: no notes, no comments, nothing in English.`

// GeminiBatch is the Gemini stage.
type GeminiBatch struct {
	Dir, Cache string
	Key        string
	Model      string  // gemini-3.8-flash
	PriceIn    float64 // USD per million input tokens, batch
	PriceOut   float64 // USD per million output tokens, batch
	EstPerPage float64 // USD, to reserve budget for pages in flight
	Budget     float64 // USD, all batches together
	MaxPages   int     // pages per batch
	MaxFlight  int     // batches in flight at once
	MaxTries   int     // a page Gemini skipped this many times is left alone
	// MaxInlineBytes > 0 sends the requests inside the batch call (under the
	// API's 20 MB) instead of uploading a file: the Files API holds 20 GiB per
	// project, and its quota stayed full for hours after the files were
	// deleted (25 Oct 2026).
	MaxInlineBytes int
	ChunkPages     int
	Router         Router
	Meta           map[int]Meta
	Log            func(format string, args ...any)
	HTTP           *http.Client
}

// Batch is one submitted Batch job.
type Batch struct {
	Name string `json:"name"`
	// SafetyOff: sent with Gemini's safety filters off. Only these batches
	// count a page's tries: under the defaults Gemini refused 4,053 requests
	// of court rulings (blockReason OTHER), most of which it reads with them off.
	SafetyOff bool           `json:"safety_off,omitempty"`
	Upload    string         `json:"upload,omitempty"` // its input file on Google's Files API, deleted once merged
	Keys      []string       `json:"keys,omitempty"`   // an inline batch's requests in order, for answers that come back without their key
	Model     string         `json:"model,omitempty"`  // the Claude API stage keeps every model in one file
	Created   time.Time      `json:"created"`
	State     string         `json:"state"`
	Requests  map[string]int `json:"requests"` // key: pages in it
	Pages     int            `json:"pages"`
	EstUSD    float64        `json:"est_usd"`
	SpentUSD  float64        `json:"spent_usd"`
	Merged    bool           `json:"merged"`
}

func (g *GeminiBatch) stateDir() string { return filepath.Join(g.Dir, "gemini") }

func (g *GeminiBatch) load() ([]*Batch, error) {
	b, err := os.ReadFile(filepath.Join(g.stateDir(), "batches.json"))
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var bs []*Batch
	return bs, json.Unmarshal(b, &bs)
}

func (g *GeminiBatch) save(bs []*Batch) error {
	b, err := json.MarshalIndent(bs, "", " ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(g.stateDir(), "batches.json"), b)
}

func finished(state string) bool {
	switch state {
	case "JOB_STATE_SUCCEEDED", "JOB_STATE_FAILED", "JOB_STATE_CANCELLED", "JOB_STATE_EXPIRED",
		"BATCH_STATE_SUCCEEDED", "BATCH_STATE_FAILED", "BATCH_STATE_CANCELLED", "BATCH_STATE_EXPIRED":
		return true
	}
	return false
}

// Spent is what the answered requests cost, and Reserved what the batches
// still in flight are expected to.
func Spent(bs []*Batch) (spent, reserved float64) {
	for _, b := range bs {
		spent += b.SpentUSD
		if !b.Merged {
			reserved += b.EstUSD
		}
	}
	return
}

// Step polls the batches in flight, merges what came back, and submits new
// work while the budget allows.
func (g *GeminiBatch) Step(ctx context.Context) error {
	if err := os.MkdirAll(g.stateDir(), 0o755); err != nil {
		return err
	}
	bs, err := g.load()
	if err != nil {
		return err
	}
	inFlight := map[string]bool{} // "id:n" of pages in unmerged batches
	tries := map[string]int{}     // "id:n": batches that asked for it
	flying := 0
	for _, b := range bs {
		if !b.SafetyOff {
			continue
		}
		for k := range b.Requests {
			id, pages := parseKey(k)
			for _, n := range pages {
				tries[fmt.Sprintf("%d:%d", id, n)]++
			}
		}
	}
	for _, b := range bs {
		if b.Merged {
			continue
		}
		if err := g.poll(ctx, b); err != nil {
			g.Log("gemini %s: %v", b.Name, err)
		}
		if finished(b.State) {
			if err := g.merge(ctx, b); err != nil {
				g.Log("gemini %s: merge: %v", b.Name, err)
			} else {
				b.Merged = true
			}
			if err := g.save(bs); err != nil {
				return err
			}
			continue
		}
		flying++
		for k := range b.Requests {
			id, pages := parseKey(k)
			for _, n := range pages {
				inFlight[fmt.Sprintf("%d:%d", id, n)] = true
			}
		}
	}
	spent, reserved := Spent(bs)
	g.Log("gemini: spent $%.2f, reserved $%.2f, budget $%.2f, %d batches in flight", spent, reserved, g.Budget, flying)
	if flying >= g.MaxFlight {
		return nil
	}
	room := int((g.Budget - spent - reserved) / g.EstPerPage)
	if room <= 0 {
		g.Log("gemini: budget reached; nothing new submitted")
		return nil
	}
	reqs, err := g.pending(inFlight, tries, min(room, g.MaxPages))
	if err != nil || len(reqs) == 0 {
		return err
	}
	b, err := g.submit(ctx, reqs)
	if err != nil {
		return err
	}
	return g.save(append(bs, b))
}

type request struct {
	id    int
	pages []int
}

func (r request) key() string {
	s := make([]string, len(r.pages))
	for i, n := range r.pages {
		s[i] = strconv.Itoa(n)
	}
	return fmt.Sprintf("%d:%s", r.id, strings.Join(s, ","))
}

func parseKey(k string) (int, []int) {
	a, b, _ := strings.Cut(k, ":")
	id, _ := strconv.Atoi(a)
	var pages []int
	for _, s := range strings.Split(b, ",") {
		if n, err := strconv.Atoi(s); err == nil {
			pages = append(pages, n)
		}
	}
	return id, pages
}

// pending gathers up to max pages for Gemini that are not already in flight,
// rulings in id order, grouped per ruling in chunks.
func (g *GeminiBatch) pending(inFlight map[string]bool, tries map[string]int, max int) ([]request, error) {
	ids, err := Downloaded(g.Dir)
	if err != nil {
		return nil, err
	}
	g.Router.firstSort(ids)
	var out []request
	total := 0
	for _, id := range ids {
		t, err := LoadTriage(g.Dir, id)
		if err != nil || t == nil {
			continue
		}
		rd, _ := LoadReading(g.Dir, id)
		rs, _ := LoadReadings(g.Dir, id)
		var todo, alone []int // a page Gemini refused once is asked for on its own
		gaveUp := map[int]string{}
		for _, n := range g.Router.Missing(t, rd, rs, Gemini) {
			k := fmt.Sprintf("%d:%d", id, n)
			switch {
			case inFlight[k]:
			case tries[k] >= g.MaxTries: // asked enough: Claude reads it instead
				gaveUp[n] = "2"
			case rs[n][GeminiBlocked] != "":
				alone = append(alone, n)
			default:
				todo = append(todo, n)
			}
		}
		if len(gaveUp) > 0 {
			if err := g.Router.AddReadings(g.Dir, id, GeminiBlocked, gaveUp, t, g.Meta[id]); err != nil {
				return nil, err
			}
		}
		var chunks [][]int
		for lo := 0; lo < len(todo); lo += g.ChunkPages {
			chunks = append(chunks, todo[lo:min(lo+g.ChunkPages, len(todo))])
		}
		for _, n := range alone {
			chunks = append(chunks, []int{n})
		}
		for _, chunk := range chunks {
			if total+len(chunk) > max {
				return out, nil
			}
			out = append(out, request{id: id, pages: chunk})
			total += len(chunk)
		}
	}
	return out, nil
}

// geminiSafetyOff turns Gemini's content filters off: the pages are public
// court rulings, and criminal ones describe violence, drugs and sex offences.
// With the defaults, 21 of 30 refused pages read fine once the filters were off
// (24 Oct 2026); Google's own "OTHER" and recitation blocks still apply.
var geminiSafetyOff = func() []map[string]string {
	var out []map[string]string
	for _, c := range []string{"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT", "HARM_CATEGORY_CIVIC_INTEGRITY"} {
		out = append(out, map[string]string{"category": c, "threshold": "OFF"})
	}
	return out
}()

// line builds one JSONL request: the page images inline, then the prompt.
func (g *GeminiBatch) line(r request) ([]byte, error) {
	// The pages go as a PDF cut from the original scan, not as rendered
	// images: about a tenth of the bytes to upload and half the input tokens,
	// for the same reading (tested 21 Oct 2026 against Opus on handwriting).
	pdf, err := subPDF(Path(g.Dir, r.id), r.pages)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(r.pages))
	for i, n := range r.pages {
		names[i] = strconv.Itoa(n)
	}
	parts := []any{
		map[string]any{"inline_data": map[string]string{"mime_type": "application/pdf", "data": base64.StdEncoding.EncodeToString(pdf)}},
		map[string]any{"text": fmt.Sprintf(geminiPrompt, strings.Join(names, ", "))},
	}
	return json.Marshal(map[string]any{"key": r.key(), "request": map[string]any{
		"contents": []any{map[string]any{"parts": parts}},
		"generationConfig": map[string]any{"temperature": 0, "maxOutputTokens": 32768,
			"thinkingConfig": map[string]any{"thinkingBudget": 0}},
		"safetySettings": geminiSafetyOff,
	}})
}

// subPDF cuts the given pages out of a PDF, in that order, with poppler's
// pdfseparate and pdfunite. All pages in order: the file as it is.
func subPDF(path string, pages []int) ([]byte, error) {
	out, err := exec.Command("pdfinfo", path).Output()
	if err != nil {
		return nil, fmt.Errorf("pdfinfo: %v", err)
	}
	total := 0
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "Pages:" {
			total, _ = strconv.Atoi(f[1])
		}
	}
	whole := len(pages) == total
	for i, n := range pages {
		whole = whole && n == i+1
	}
	if whole {
		return os.ReadFile(path)
	}
	dir, err := os.MkdirTemp("", "subpdf-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	args := []string{}
	for _, n := range pages {
		one := filepath.Join(dir, fmt.Sprintf("p%d.pdf", n))
		if out, err := exec.Command("pdfseparate", "-f", strconv.Itoa(n), "-l", strconv.Itoa(n), path, one).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("pdfseparate page %d: %v %s", n, err, out)
		}
		args = append(args, one)
	}
	if len(args) == 1 {
		return os.ReadFile(args[0])
	}
	cut := filepath.Join(dir, "cut.pdf")
	if out, err := exec.Command("pdfunite", append(args, cut)...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdfunite: %v %s", err, out)
	}
	return os.ReadFile(cut)
}

func (g *GeminiBatch) submit(ctx context.Context, reqs []request) (*Batch, error) {
	// The requests go to a file first, a line at a time: a batch of page
	// images is hundreds of MB, too much to hold in memory twice.
	b := &Batch{Created: time.Now().UTC(), Requests: map[string]int{}, SafetyOff: true}
	if g.MaxInlineBytes > 0 {
		return g.submitInline(ctx, b, reqs)
	}
	path := filepath.Join(g.stateDir(), "upload-"+b.Created.Format("20060102-150405")+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	w := bufio.NewWriter(f)
	for _, r := range reqs {
		l, err := g.line(r)
		if err != nil {
			f.Close()
			return nil, err
		}
		w.Write(l)
		w.WriteByte('\n')
		b.Requests[r.key()] = len(r.pages)
		b.Pages += len(r.pages)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	b.EstUSD = float64(b.Pages) * g.EstPerPage
	file, err := g.upload(ctx, path, fmt.Sprintf("rulings-%s", b.Created.Format("20060102-150405")))
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"batch": map[string]any{
		"display_name": "rulings-" + b.Created.Format("20060102-150405"),
		"input_config": map[string]any{"file_name": file}}})
	var out map[string]any
	if err := g.call(ctx, "POST", geminiAPI+"/v1beta/models/"+g.Model+":batchGenerateContent", body, &out); err != nil {
		return nil, err
	}
	b.Name, _ = out["name"].(string)
	b.Upload = file
	b.State = batchState(out)
	if b.Name == "" {
		return nil, fmt.Errorf("batch create: no name in %v", out)
	}
	g.Log("gemini: submitted %s, %d requests, %d pages, est $%.2f", b.Name, len(b.Requests), b.Pages, b.EstUSD)
	return b, nil
}

// submitInline puts as many requests as fit in MaxInlineBytes in the batch
// call itself; the rest wait for the next batch.
func (g *GeminiBatch) submitInline(ctx context.Context, b *Batch, reqs []request) (*Batch, error) {
	var items []json.RawMessage
	size := 0
	for _, r := range reqs {
		l, err := g.line(r)
		if err != nil {
			return nil, err
		}
		var x struct {
			Key     string          `json:"key"`
			Request json.RawMessage `json:"request"`
		}
		if err := json.Unmarshal(l, &x); err != nil {
			return nil, err
		}
		item, _ := json.Marshal(map[string]any{"request": x.Request, "metadata": map[string]string{"key": x.Key}})
		if len(item) > g.MaxInlineBytes {
			g.Log("gemini: request %s is %d MB, more than an inline batch holds; skipped", x.Key, len(item)>>20)
			continue
		}
		if size+len(item) > g.MaxInlineBytes {
			break
		}
		size += len(item)
		items = append(items, item)
		b.Keys = append(b.Keys, x.Key)
		b.Requests[r.key()] = len(r.pages)
		b.Pages += len(r.pages)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no request fits an inline batch")
	}
	b.EstUSD = float64(b.Pages) * g.EstPerPage
	body, err := json.Marshal(map[string]any{"batch": map[string]any{
		"display_name": "rulings-" + b.Created.Format("20060102-150405"),
		"input_config": map[string]any{"requests": map[string]any{"requests": items}}}})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := g.call(ctx, "POST", geminiAPI+"/v1beta/models/"+g.Model+":batchGenerateContent", body, &out); err != nil {
		return nil, err
	}
	b.Name, _ = out["name"].(string)
	b.State = batchState(out)
	if b.Name == "" {
		return nil, fmt.Errorf("batch create: no name in %v", out)
	}
	g.Log("gemini: submitted %s inline, %d requests, %d pages, %.0f MB, est $%.2f", b.Name, len(items), b.Pages, float64(size)/1e6, b.EstUSD)
	return b, nil
}

// inlinedLines turns an inline batch's answers into the lines a responses
// file holds ({"key", "response"} or {"key", "error"}), so merge reads both
// the same way. Every answer must carry the key of a request sent, once;
// only when none carries a key and the counts match are answers taken in
// request order. Anything else is an error, so no text lands on the wrong
// ruling. ok is false for a batch that has no inlined answers (a file batch).
func inlinedLines(m map[string]any, keys []string) (lines []byte, ok bool, err error) {
	var list []any
	for _, path := range [][]string{{"response", "inlinedResponses", "inlinedResponses"}, {"dest", "inlinedResponses", "inlinedResponses"},
		{"metadata", "output", "inlinedResponses", "inlinedResponses"}, {"output", "inlinedResponses", "inlinedResponses"}} {
		var cur any = m
		for _, k := range path {
			mm, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = mm[k]
		}
		if l, ok := cur.([]any); ok {
			list = l
			break
		}
	}
	if list == nil {
		return nil, false, nil
	}
	got := make([]string, len(list))
	keyless := 0
	for i, it := range list {
		item, _ := it.(map[string]any)
		if md, ok := item["metadata"].(map[string]any); ok {
			got[i], _ = md["key"].(string)
		}
		if got[i] == "" {
			keyless++
		}
	}
	switch {
	case keyless == len(list) && len(list) == len(keys):
		copy(got, keys)
	case keyless > 0:
		return nil, true, fmt.Errorf("%d of %d answers have no key", keyless, len(list))
	default:
		sent, seen := map[string]bool{}, map[string]bool{}
		for _, k := range keys {
			sent[k] = true
		}
		for _, k := range got {
			if !sent[k] || seen[k] {
				return nil, true, fmt.Errorf("answer key %q was not sent, or came twice", k)
			}
			seen[k] = true
		}
	}
	var out bytes.Buffer
	for i, it := range list {
		item, _ := it.(map[string]any)
		line := map[string]any{"key": got[i]}
		if r, ok := item["response"]; ok {
			line["response"] = r
		}
		if e, ok := item["error"]; ok {
			line["error"] = e
		}
		l, _ := json.Marshal(line)
		out.Write(l)
		out.WriteByte('\n')
	}
	return out.Bytes(), true, nil
}

// upload sends a file with the resumable protocol. It has no overall time
// limit (a home connection takes many minutes for a batch); ctx can stop it.
func (g *GeminiBatch) upload(ctx context.Context, path, name string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	meta, _ := json.Marshal(map[string]any{"file": map[string]string{"display_name": name}})
	req, _ := http.NewRequestWithContext(ctx, "POST", geminiAPI+"/upload/v1beta/files", bytes.NewReader(meta))
	req.Header.Set("x-goog-api-key", g.Key)
	req.Header.Set("X-Goog-Upload-Protocol", "resumable")
	req.Header.Set("X-Goog-Upload-Command", "start")
	req.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(st.Size(), 10))
	req.Header.Set("X-Goog-Upload-Header-Content-Type", "application/jsonl")
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	url := resp.Header.Get("X-Goog-Upload-URL")
	if url == "" {
		return "", fmt.Errorf("upload start: status %d, no upload url", resp.StatusCode)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	req, _ = http.NewRequestWithContext(ctx, "POST", url, f)
	req.ContentLength = st.Size()
	req.Header.Set("X-Goog-Upload-Offset", "0")
	req.Header.Set("X-Goog-Upload-Command", "upload, finalize")
	t0 := time.Now()
	resp, err = (&http.Client{}).Do(req)
	g.Log("gemini: uploaded %.0f MB in %s", float64(st.Size())/1e6, time.Since(t0).Round(time.Second))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		File struct {
			Name string `json:"name"`
		} `json:"file"`
	}
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &out); err != nil || out.File.Name == "" {
		return "", fmt.Errorf("upload: status %d: %.300s", resp.StatusCode, b)
	}
	return out.File.Name, nil
}

func (g *GeminiBatch) call(ctx context.Context, method, url string, body []byte, out any) error {
	req, _ := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	req.Header.Set("x-goog-api-key", g.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s %s: status %d: %.300s", method, url[len(geminiAPI):], resp.StatusCode, b)
	}
	return json.Unmarshal(b, out)
}

// batchState finds the job's state, which the API reports in more than one place.
func batchState(m map[string]any) string {
	if s, ok := m["state"].(string); ok {
		return s
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		if s, ok := md["state"].(string); ok {
			return s
		}
	}
	return ""
}

// responsesFile finds the output file's name.
func responsesFile(m map[string]any) string {
	for _, path := range [][]string{{"response", "responsesFile"}, {"dest", "fileName"}, {"metadata", "output", "responsesFile"}, {"output", "responsesFile"}} {
		var cur any = m
		for _, k := range path {
			mm, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			cur = mm[k]
		}
		if s, ok := cur.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func (g *GeminiBatch) poll(ctx context.Context, b *Batch) error {
	var out map[string]any
	if err := g.call(ctx, "GET", geminiAPI+"/v1beta/"+b.Name, nil, &out); err != nil {
		return err
	}
	if s := batchState(out); s != "" {
		b.State = s
	}
	return nil
}

// merge downloads a finished batch's answers, writes each into its ruling's
// reading, and books the cost.
func (g *GeminiBatch) merge(ctx context.Context, b *Batch) error {
	if !strings.HasSuffix(b.State, "SUCCEEDED") {
		g.Log("gemini %s ended %s; its pages will be submitted again", b.Name, b.State)
		b.EstUSD = 0
		g.forget(ctx, b.Upload)
		return nil
	}
	var out map[string]any
	if err := g.call(ctx, "GET", geminiAPI+"/v1beta/"+b.Name, nil, &out); err != nil {
		return err
	}
	file := responsesFile(out)
	var src io.Reader
	if lines, ok, err := inlinedLines(out, b.Keys); err != nil {
		return err // not merged: asked again next step, nothing written
	} else if ok {
		src = bytes.NewReader(lines)
	} else {
		if file == "" {
			return fmt.Errorf("no responses file in %.300v", out)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", geminiAPI+"/download/v1beta/"+file+":download?alt=media", nil)
		req.Header.Set("x-goog-api-key", g.Key)
		resp, err := g.HTTP.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		src = resp.Body
	}
	ledger, err := os.OpenFile(filepath.Join(g.stateDir(), "spend.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer ledger.Close()
	// The cost and its ledger rows are booked once the whole batch is merged:
	// a merge cut short runs again from the start.
	var spent float64
	var rows strings.Builder
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	n, failed := 0, 0
	for sc.Scan() {
		key, pages, usd, err := g.answer(sc.Bytes())
		spent += usd
		fmt.Fprintf(&rows, "%s\t%s\t%.6f\t%v\n", b.Name, key, usd, err)
		id, asked := parseKey(key)
		t, terr := LoadTriage(g.Dir, id)
		if terr != nil || t == nil {
			g.Log("gemini %s: ruling %d has no triage (%v)", b.Name, id, terr)
			continue
		}
		if err != nil {
			// Gemini refused (blockReason): count it on each page, so the page is
			// asked for alone next, and handed to Claude after a second refusal.
			failed++
			rs, _ := LoadReadings(g.Dir, id)
			refused := map[int]string{}
			for _, n := range asked {
				c, _ := strconv.Atoi(rs[n][GeminiBlocked])
				refused[n] = strconv.Itoa(c + 1)
			}
			if err := g.Router.AddReadings(g.Dir, id, GeminiBlocked, refused, t, g.Meta[id]); err != nil {
				return err
			}
			continue
		}
		if err := g.Router.AddReadings(g.Dir, id, Gemini, pages, t, g.Meta[id]); err != nil {
			return err
		}
		n++
	}
	if err := sc.Err(); err != nil {
		return err // merged again next step, so its files stay
	}
	if _, err := ledger.WriteString(rows.String()); err != nil {
		return err
	}
	b.SpentUSD = spent
	b.EstUSD = 0
	g.Log("gemini %s: merged %d answers (%d failed), cost $%.2f", b.Name, n, failed, b.SpentUSD)
	g.forget(ctx, b.Upload, file)
	return nil
}

// forget deletes a batch's files from Google's Files API, which holds 20 GB
// per project: every batch uploads 160-390 MB, nothing deleted them, and
// uploads failed with 429 for 12 hours until 120 files (21.45 GB) were
// deleted by hand (25 Oct 2026).
func (g *GeminiBatch) forget(ctx context.Context, names ...string) {
	for _, n := range names {
		if n == "" {
			continue
		}
		var out map[string]any
		if err := g.call(ctx, "DELETE", geminiAPI+"/v1beta/"+n, nil, &out); err != nil {
			g.Log("gemini: delete %s: %v", n, err)
		}
	}
}

// answer parses one output line into page texts and its cost. A page with
// the model's own talk in it is left out, so it is asked for again.
func (g *GeminiBatch) answer(line []byte) (string, map[int]string, float64, error) {
	var a struct {
		Key      string `json:"key"`
		Response *struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
			UsageMetadata struct {
				Prompt     int `json:"promptTokenCount"`
				Candidates int `json:"candidatesTokenCount"`
				Thoughts   int `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(line, &a); err != nil {
		return "", nil, 0, err
	}
	if a.Response == nil {
		return a.Key, nil, 0, fmt.Errorf("no response: %v", a.Error)
	}
	u := a.Response.UsageMetadata
	usd := float64(u.Prompt)*g.PriceIn/1e6 + float64(u.Candidates+u.Thoughts)*g.PriceOut/1e6
	if len(a.Response.Candidates) == 0 {
		return a.Key, nil, usd, fmt.Errorf("no candidates (blocked: %s)", a.Response.PromptFeedback.BlockReason)
	}
	var text strings.Builder
	for _, p := range a.Response.Candidates[0].Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	_, want := parseKey(a.Key)
	got := onePage(SplitPages(text.String()), want)
	if len(got) == 0 && len(want) == 1 {
		got = map[int]string{want[0]: strings.TrimSpace(text.String())}
	}
	pages := map[int]string{}
	for _, n := range want {
		if t, ok := got[n]; ok && !Chatter(t) {
			pages[n] = t // a page left out stays pending; the next batch asks again
		}
	}
	return a.Key, pages, usd, nil
}
