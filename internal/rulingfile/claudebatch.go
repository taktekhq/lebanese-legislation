package rulingfile

// The Claude API stage: the pages a Claude reader still owes, read through
// the paid Message Batches API (half the standard price) instead of Claude
// Code on a plan, for when the plan's usage has run out. One model per run.
// Like the Gemini stage it keeps a ledger and never submits work whose
// estimated cost would take the total past Budget.
//
// The pages go as a PDF cut from the original scan (subPDF), not as the
// rendered images: about a tenth of the bytes, and the house uploads at
// about 0.35 MB/s.
//
// State lives in <dir>/claude-api/: batches.json (every batch, its requests
// and state, all models) and spend.tsv (the cost of each answered request).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// ClaudeBatchPrice is USD per million input and output tokens in Batch mode.
var ClaudeBatchPrice = map[string][2]float64{
	Haiku:  {0.05, 0.25},
	Sonnet: {1, 5},
	Opus:   {2, 10},
}

type ClaudeBatch struct {
	Dir        string
	Client     *anthropic.Client
	Model      string  // Haiku, Sonnet or Opus
	EstPerPage float64 // USD, to reserve budget for pages in flight
	Budget     float64 // USD, all batches of this model together
	MaxPages   int     // pages per batch
	MaxBytes   int     // PDF bytes per batch, well under the API's 256 MB request cap
	MaxFlight  int     // batches of this model in flight at once
	MaxTries   int     // a page asked for this many times is left alone
	ChunkPages int
	Router     Router
	Meta       map[int]Meta
	Log        func(format string, args ...any)
}

func (c *ClaudeBatch) stateDir() string { return filepath.Join(c.Dir, "claude-api") }

func (c *ClaudeBatch) load() ([]*Batch, error) {
	b, err := os.ReadFile(filepath.Join(c.stateDir(), "batches.json"))
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var bs []*Batch
	return bs, json.Unmarshal(b, &bs)
}

// save writes this model's batches from bs and keeps every other model's as
// they are on disk now, under a lock: the Haiku and Sonnet units share the
// file, and writing back a whole copy loaded minutes earlier dropped the
// other unit's new batches (19 paid batches lost, 24 Oct 2026).
func (c *ClaudeBatch) save(bs []*Batch) error {
	f, err := os.OpenFile(filepath.Join(c.stateDir(), "batches.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	disk, err := c.load()
	if err != nil {
		return err
	}
	var out []*Batch
	for _, b := range disk {
		if b.Model != c.Model {
			out = append(out, b)
		}
	}
	for _, b := range bs {
		if b.Model == c.Model {
			out = append(out, b)
		}
	}
	b, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(c.stateDir(), "batches.json"), b)
}

// customID turns a request key ("id:1,2") into a batch custom_id, which
// allows only letters, digits, - and _.
func customID(key string) string {
	return "r" + strings.NewReplacer(":", "_p", ",", "-").Replace(key)
}

func keyOf(customID string) string {
	return strings.NewReplacer("_p", ":", "-", ",").Replace(strings.TrimPrefix(customID, "r"))
}

// Step polls this model's batches in flight, merges what came back, and
// submits new work while the budget allows.
func (c *ClaudeBatch) Step(ctx context.Context) error {
	if err := os.MkdirAll(c.stateDir(), 0o755); err != nil {
		return err
	}
	bs, err := c.load()
	if err != nil {
		return err
	}
	inFlight := map[string]bool{}
	tries := map[string]int{}
	flying := 0
	var mine []*Batch
	for _, b := range bs {
		if b.Model != c.Model {
			continue
		}
		mine = append(mine, b)
		for k := range b.Requests {
			id, pages := parseKey(k)
			for _, n := range pages {
				tries[fmt.Sprintf("%d:%d", id, n)]++
			}
		}
	}
	for _, b := range mine {
		if b.Merged {
			continue
		}
		got, err := c.Client.Messages.Batches.Get(ctx, b.Name, anthropic.MessageBatchGetParams{})
		if err != nil {
			c.Log("claude-api %s: %v", b.Name, err)
		} else {
			b.State = string(got.ProcessingStatus)
		}
		if b.State == string(anthropic.MessageBatchProcessingStatusEnded) {
			if err := c.merge(ctx, b); err != nil {
				c.Log("claude-api %s: merge: %v", b.Name, err)
			} else {
				b.Merged = true
			}
			if err := c.save(bs); err != nil {
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
	spent, reserved := Spent(mine)
	c.Log("claude-api %s: spent $%.2f, reserved $%.2f, budget $%.2f, %d batches in flight", c.Model, spent, reserved, c.Budget, flying)
	if flying >= c.MaxFlight {
		return nil
	}
	room := int((c.Budget - spent - reserved) / c.EstPerPage)
	if room <= 0 {
		c.Log("claude-api %s: budget reached; nothing new submitted", c.Model)
		return nil
	}
	reqs, err := c.pending(inFlight, tries, min(room, c.MaxPages))
	if err != nil || len(reqs) == 0 {
		return err
	}
	b, err := c.submit(ctx, reqs)
	if err != nil {
		return err
	}
	return c.save(append(bs, b))
}

// pending gathers up to max pages this model still has to read and that are
// not already in flight, rulings in Router order, grouped per ruling in chunks.
func (c *ClaudeBatch) pending(inFlight map[string]bool, tries map[string]int, max int) ([]request, error) {
	ids, err := Downloaded(c.Dir)
	if err != nil {
		return nil, err
	}
	c.Router.firstSort(ids)
	var out []request
	total := 0
	for _, id := range ids {
		t, err := LoadTriage(c.Dir, id)
		if err != nil || t == nil {
			continue
		}
		rd, _ := LoadReading(c.Dir, id)
		rs, _ := LoadReadings(c.Dir, id)
		hand := map[int]bool{}
		for _, p := range t.Pages {
			hand[p.N] = c.Router.Tier(id, p) == TierGemini
		}
		var todo []int
		for _, n := range c.Router.Missing(t, rd, rs, c.Model) {
			k := fmt.Sprintf("%d:%d", id, n)
			// Haiku declines handwriting ("can't reliably produce" a
			// transcription, tested 24 Oct 2026): don't pay for the refusal.
			if c.Model == Haiku && hand[n] {
				continue
			}
			if !inFlight[k] && tries[k] < c.MaxTries {
				todo = append(todo, n)
			}
		}
		for lo := 0; lo < len(todo); lo += c.ChunkPages {
			chunk := todo[lo:min(lo+c.ChunkPages, len(todo))]
			if total+len(chunk) > max {
				return out, nil
			}
			out = append(out, request{id: id, pages: chunk})
			total += len(chunk)
		}
	}
	return out, nil
}

// params is one request: the pages as a PDF, then the prompt Gemini gets.
// Thinking is off where the model allows it (a transcription needs none) and
// as low as it goes on Opus, where it can't be turned off.
func (c *ClaudeBatch) params(pdf []byte, pages []int) anthropic.MessageBatchNewParamsRequestParams {
	names := make([]string, len(pages))
	for i, n := range pages {
		names[i] = strconv.Itoa(n)
	}
	p := anthropic.MessageBatchNewParamsRequestParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: 32000,
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(
			anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(pdf)}),
			anthropic.NewTextBlock(fmt.Sprintf(geminiPrompt, strings.Join(names, ", "))),
		)},
	}
	switch c.Model {
	case Haiku:
		p.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
	case Sonnet:
		p.Thinking = anthropic.ThinkingConfigParamUnion{OfBetweenTools: &anthropic.ThinkingConfigBetweenToolsParam{}}
	default:
		p.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort("low")}
	}
	return p
}

func (c *ClaudeBatch) submit(ctx context.Context, reqs []request) (*Batch, error) {
	b := &Batch{Model: c.Model, Created: time.Now().UTC(), Requests: map[string]int{}}
	var body []anthropic.MessageBatchNewParamsRequest
	bytes := 0
	for _, r := range reqs {
		pdf, err := subPDF(Path(c.Dir, r.id), r.pages)
		if err != nil {
			c.Log("claude-api %d: %v", r.id, err)
			continue
		}
		if bytes+len(pdf) > c.MaxBytes && len(body) > 0 {
			break // the rest go in the next batch
		}
		bytes += len(pdf)
		body = append(body, anthropic.MessageBatchNewParamsRequest{CustomID: customID(r.key()), Params: c.params(pdf, r.pages)})
		b.Requests[r.key()] = len(r.pages)
		b.Pages += len(r.pages)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("no request could be built")
	}
	b.EstUSD = float64(b.Pages) * c.EstPerPage
	start := time.Now()
	got, err := c.Client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: body})
	if err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}
	b.Name, b.State = got.ID, string(got.ProcessingStatus)
	c.Log("claude-api %s: submitted %s, %d requests, %d pages, %.0f MB in %s, est $%.2f",
		c.Model, b.Name, len(body), b.Pages, float64(bytes)/1e6, time.Since(start).Round(time.Second), b.EstUSD)
	return b, nil
}

func (c *ClaudeBatch) merge(ctx context.Context, b *Batch) error {
	ledger, err := os.OpenFile(filepath.Join(c.stateDir(), "spend.tsv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer ledger.Close()
	price := ClaudeBatchPrice[c.Model]
	stream := c.Client.Messages.Batches.ResultsStreaming(ctx, b.Name, anthropic.MessageBatchResultsParams{})
	defer stream.Close()
	b.SpentUSD = 0 // a merge cut short is read again from the start
	n, failed := 0, 0
	for stream.Next() {
		res := stream.Current()
		key := keyOf(res.CustomID)
		id, asked := parseKey(key)
		var pages map[int]string
		var usd float64
		var rerr error
		switch res.Result.Type {
		case "succeeded":
			m := res.Result.AsSucceeded().Message
			usd = float64(m.Usage.InputTokens)*price[0]/1e6 + float64(m.Usage.OutputTokens)*price[1]/1e6
			var declined bool
			pages, declined, rerr = claudePages(m, asked)
			if declined && c.Model == Haiku {
				// Haiku says it can't read these pages: Sonnet reads them instead
				// (PagePlan), so asking Haiku again would only pay for the refusal.
				mark := map[int]string{}
				for _, n := range asked {
					mark[n] = "1"
				}
				if t, _ := LoadTriage(c.Dir, id); t != nil {
					if err := c.Router.AddReadings(c.Dir, id, HaikuDeclined, mark, t, c.Meta[id]); err != nil {
						return err
					}
				}
			}
		default:
			rerr = fmt.Errorf("%s %s", res.Result.Type, res.Result.RawJSON())
		}
		b.SpentUSD += usd
		fmt.Fprintf(ledger, "%s\t%s\t%s\t%.6f\t%v\n", b.Name, c.Model, key, usd, rerr)
		if rerr != nil {
			failed++ // its pages are asked for again, up to MaxTries
			continue
		}
		t, terr := LoadTriage(c.Dir, id)
		if terr != nil || t == nil {
			c.Log("claude-api %s: ruling %d has no triage (%v)", b.Name, id, terr)
			continue
		}
		if err := c.Router.AddReadings(c.Dir, id, c.Model, pages, t, c.Meta[id]); err != nil {
			return err
		}
		n++
	}
	if err := stream.Err(); err != nil {
		return err
	}
	b.EstUSD = 0
	c.Log("claude-api %s: merged %d answers (%d failed), cost $%.2f", b.Name, n, failed, b.SpentUSD)
	return nil
}

// claudePages splits an answer into page texts. A page with the model's own
// talk in it is left out, so it is asked for again; a refusal or a cut-off
// answer gives nothing. declined: the whole answer is the model explaining,
// with no page in it, why it won't transcribe.
func claudePages(m anthropic.Message, want []int) (pages map[int]string, declined bool, err error) {
	if m.StopReason != anthropic.StopReasonEndTurn {
		return nil, false, fmt.Errorf("stop_reason %s", m.StopReason)
	}
	var text strings.Builder
	for _, b := range m.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	got := onePage(SplitPages(text.String()), want)
	if len(got) == 0 && Chatter(text.String()) {
		return nil, true, nil
	}
	if len(got) == 0 && len(want) == 1 {
		got = map[int]string{want[0]: strings.TrimSpace(text.String())}
	}
	pages = map[int]string{}
	for _, n := range want {
		if t, ok := got[n]; ok && !Chatter(t) {
			pages[n] = t
		}
	}
	return pages, false, nil
}
