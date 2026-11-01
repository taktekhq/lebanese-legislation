package rulingfile

// The Claude stage: typed pages read by Claude Code (`claude -p`) on a
// subscription, so they cost no money, only plan usage. A usage guard pauses
// the work before the plan's 5-hour or weekly limit is reached, so the
// person whose plan it is can still use it.
//
// Every reading is checked against what the Centre recorded for the ruling
// (number, year, president): a reading that doesn't show them, or has too
// many words marked unreadable, goes up a tier (Haiku, Sonnet, Opus, then
// Gemini) and is read again.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Meta is what the Centre recorded about a ruling, for checking readings.
type Meta struct {
	Court, Number, Year, President string
}

// LoadMeta reads a TSV of id, court, number, year, president (no header),
// as written by `psql -At -F'<tab>'`.
func LoadMeta(path string) (map[int]Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[int]Meta{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(sc.Text(), "\t")
		if len(c) < 5 {
			continue
		}
		id, err := strconv.Atoi(c[0])
		if err != nil {
			continue
		}
		out[id] = Meta{Court: c[1], Number: c[2], Year: c[3], President: c[4]}
	}
	return out, sc.Err()
}

// norm folds what differs between a scan and the Centre's record without
// changing meaning: Arabic-Indic digits, alef and yeh forms, ta marbuta, tatweel.
func norm(s string) string {
	return strings.NewReplacer("أ", "ا", "إ", "ا", "آ", "ا", "ى", "ي", "ة", "ه", "ـ", "",
		"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
		"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9").Replace(s)
}

// CheckHeader says whether a reading of a ruling's first pages shows the
// ruling's number and either its year or its president's family name, as the
// Centre recorded them. Rulings with no recorded number pass.
func CheckHeader(text string, m Meta) bool {
	if m.Number == "" {
		return true
	}
	t := norm(text)
	if !strings.Contains(t, norm(m.Number)) {
		return false
	}
	if m.Year != "" && m.Year != "0" && strings.Contains(t, m.Year) {
		return true
	}
	if f := strings.Fields(norm(m.President)); len(f) > 0 && strings.Contains(t, f[len(f)-1]) {
		return true
	}
	return m.Year == "" && m.President == ""
}

// UsageGuard reads the plan's usage from Anthropic's OAuth usage endpoint
// with the token Claude Code keeps, and waits while it is over the limits.
type UsageGuard struct {
	Credentials    string  // ~/.claude/.credentials.json
	Max5h, MaxWeek float64 // percent
	Log            func(format string, args ...any)
	mu             sync.Mutex
	checked        time.Time
	five, week     float64
	fiveAt, weekAt time.Time
}

func (g *UsageGuard) read() error {
	b, err := os.ReadFile(g.Credentials)
	if err != nil {
		return err
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	req, _ := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	req.Header.Set("Authorization", "Bearer "+c.ClaudeAiOauth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	type window struct {
		Utilization float64   `json:"utilization"`
		ResetsAt    time.Time `json:"resets_at"`
	}
	var u struct {
		F *window `json:"five_hour"`
		W *window `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return err
	}
	if u.F == nil || u.W == nil {
		return fmt.Errorf("usage: status %d, no limits in the answer", resp.StatusCode)
	}
	g.five, g.fiveAt, g.week, g.weekAt = u.F.Utilization, u.F.ResetsAt, u.W.Utilization, u.W.ResetsAt
	return nil
}

// Wait returns once the plan is under both limits (or ctx ends). If the usage
// can't be read it lets the work go on and says so.
func (g *UsageGuard) Wait(ctx context.Context) {
	if g == nil {
		return
	}
	for ctx.Err() == nil {
		g.mu.Lock()
		if time.Since(g.checked) > 2*time.Minute {
			if err := g.read(); err != nil {
				g.Log("usage guard: %v (carrying on)", err)
			}
			g.checked = time.Now()
		}
		over, until := false, time.Time{}
		if g.five >= g.Max5h {
			over, until = true, g.fiveAt
		}
		if g.week >= g.MaxWeek {
			over, until = true, g.weekAt
		}
		five, week := g.five, g.week
		g.mu.Unlock()
		if !over {
			return
		}
		d := min(max(time.Until(until)+time.Minute, 5*time.Minute), time.Hour)
		g.Log("usage guard: 5h %.0f%%, week %.0f%%; waiting %s", five, week, d.Round(time.Minute))
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
		g.mu.Lock()
		g.checked = time.Time{}
		g.mu.Unlock()
	}
}

// ClaudeReader is the Claude stage.
type ClaudeReader struct {
	Dir, Cache string
	Bin        string // claude
	Workers    int
	ChunkPages int
	Timeout    time.Duration
	Router     Router
	Meta       map[int]Meta
	Guard      *UsageGuard
	Log        func(format string, args ...any)
	// OnlyUnread: read only pages no model has read yet (Gemini refused them),
	// never a second reading (since 24 Oct 2026 every read page is live, no
	// second readings for now).
	OnlyUnread bool
}

// errDeclined: the model answered that it can't transcribe the pages.
var errDeclined = errors.New("the model declined to transcribe")

// unread says no model has read the page yet.
func unread(got map[string]string) bool {
	for _, m := range []string{Gemini, Opus, Sonnet, Haiku} {
		if got[m] != "" {
			return false
		}
	}
	return true
}

// owed lists the pages of a ruling a model still has to read here: the
// router's, less the ones already read when OnlyUnread, and never Haiku on
// handwriting, which it declines. With OnlyUnread a handwritten page goes to
// Opus alone: on pages Gemini's API blocks, Opus read the handwriting right
// line for line, Sonnet misreads numbers, and Codex invented whole sentences
// (24 Oct 2026).
func (c *ClaudeReader) owed(t *Triage, rd *Reading, rs Readings, model string) []int {
	hand := map[int]bool{}
	for _, p := range t.Pages {
		hand[p.N] = c.Router.Tier(t.ID, p) == TierGemini
	}
	var out []int
	for _, n := range c.Router.Missing(t, rd, rs, model) {
		if (model == Haiku && hand[n]) || (c.OnlyUnread && !unread(rs[n])) || (c.OnlyUnread && hand[n] && model != Opus) {
			continue
		}
		out = append(out, n)
	}
	return out
}

const claudePrompt = `Use the Read tool once on each of these image files, in order: %s. They are pages of one Lebanese court judgment, in Arabic; file p-N.jpg is page N. Transcribe each of these pages exactly as written, line by line in reading order, including the header and margin notes (prefix margin lines with [هامش]). Start each page with a line '=== صفحة N ===' using its page number. Write [؟] for any word you cannot read; never guess, correct, complete or summarize. Output only the transcription of these pages: no notes, no comments, nothing in English.`

// claudeModels read in this order: the cheapest first, so a page's base
// readings land before anyone decides a stronger reader is needed.
var claudeModels = []string{Haiku, Sonnet, Opus}

// Run reads, for every triaged ruling, the pages that still need a Claude
// reader; the rulings in Router.First go first.
func (c *ClaudeReader) Run(ctx context.Context) error {
	ids, err := Downloaded(c.Dir)
	if err != nil {
		return err
	}
	var todo []int
	for _, id := range ids {
		t, _ := LoadTriage(c.Dir, id)
		if t == nil {
			continue
		}
		rd, _ := LoadReading(c.Dir, id)
		rs, _ := LoadReadings(c.Dir, id)
		for _, m := range claudeModels {
			if len(c.owed(t, rd, rs, m)) > 0 {
				todo = append(todo, id)
				break
			}
		}
	}
	c.Router.firstSort(todo)
	c.Log("claude: %d rulings with pages to read", len(todo))
	var mu sync.Mutex
	n, pages := 0, 0
	start := time.Now()
	return parallel(ctx, todo, c.Workers, func(id int) {
		p, err := c.one(ctx, id)
		mu.Lock()
		defer mu.Unlock()
		n++
		pages += p
		if err != nil {
			c.Log("claude %d: %v", id, err)
		}
		if n%25 == 0 {
			c.Log("claude: %d/%d rulings, %d page readings, %.0f readings/h", n, len(todo), pages, float64(pages)/time.Since(start).Hours())
		}
	})
}

// one has each Claude model read the pages of a ruling it hasn't read yet.
func (c *ClaudeReader) one(ctx context.Context, id int) (int, error) {
	t, err := LoadTriage(c.Dir, id)
	if err != nil || t == nil {
		return 0, err
	}
	read := 0
	for _, model := range claudeModels {
		rd, err := LoadReading(c.Dir, id)
		if err != nil {
			return read, err
		}
		rs, err := LoadReadings(c.Dir, id)
		if err != nil {
			return read, err
		}
		todo := c.owed(t, rd, rs, model)
		for lo := 0; lo < len(todo); lo += c.ChunkPages {
			chunk := todo[lo:min(lo+c.ChunkPages, len(todo))]
			c.Guard.Wait(ctx)
			got, err := c.call(ctx, id, model, chunk)
			if errors.Is(err, errDeclined) {
				c.Log("claude %d %s: declined pages %v", id, model, chunk)
				if model == Haiku { // Sonnet takes its place (PagePlan); don't ask again
					mark := map[int]string{}
					for _, n := range chunk {
						mark[n] = "1"
					}
					if err := c.Router.AddReadings(c.Dir, id, HaikuDeclined, mark, t, c.Meta[id]); err != nil {
						return read, err
					}
				}
				continue
			}
			if err != nil {
				return read, err
			}
			keep := map[int]string{}
			for _, n := range chunk {
				if text, ok := got[n]; ok {
					keep[n] = text
				}
			}
			if len(keep) < len(chunk) {
				c.Log("claude %d %s: %d of %d pages answered; the rest are asked again next run", id, model, len(keep), len(chunk))
			}
			read += len(keep)
			if err := c.Router.AddReadings(c.Dir, id, model, keep, t, c.Meta[id]); err != nil {
				return read, err
			}
		}
	}
	return read, nil
}

// call runs one claude -p on some pages of a ruling and splits its answer by
// page. An answer with the model's own talk in it is asked for once more,
// then dropped.
func (c *ClaudeReader) call(ctx context.Context, id int, model string, pages []int) (map[int]string, error) {
	files := make([]string, len(pages))
	for i, n := range pages {
		files[i] = PageImage(c.Cache, id, n)
		if _, err := os.Stat(files[i]); err != nil {
			return nil, err
		}
	}
	prompt := fmt.Sprintf(claudePrompt, strings.Join(files, ", "))
	chatter := 0
	for try := 0; try < 3; try++ {
		cctx, cancel := context.WithTimeout(ctx, c.Timeout)
		cmd := exec.CommandContext(cctx, c.Bin, "-p", prompt, "--model", model, "--allowedTools", "Read",
			"--strict-mcp-config", "--output-format", "json")
		cmd.Dir = filepath.Join(c.Cache, strconv.Itoa(id))
		out, err := cmd.Output()
		cancel()
		var ans struct {
			Result  string `json:"result"`
			IsError bool   `json:"is_error"`
		}
		if jerr := json.Unmarshal(out, &ans); jerr != nil || ans.IsError {
			msg := fmt.Sprintf("%v %.200s", err, out)
			if strings.Contains(strings.ToLower(msg), "limit") {
				c.Log("claude %d: %s; waiting 30 min", id, strings.TrimSpace(msg))
				select {
				case <-time.After(30 * time.Minute):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if try == 2 {
				return nil, fmt.Errorf("claude: %s", msg)
			}
			continue
		}
		got := onePage(SplitPages(ans.Result), pages)
		if len(got) == 0 && Chatter(ans.Result) {
			return nil, errDeclined
		}
		if len(got) == 0 && len(pages) == 1 && strings.TrimSpace(ans.Result) != "" {
			got = map[int]string{pages[0]: strings.TrimSpace(ans.Result)}
		}
		bad := false
		for n, text := range got {
			if Chatter(text) {
				delete(got, n)
				bad = true
			}
		}
		if bad && chatter == 0 {
			chatter++
			c.Log("claude %d %s: the answer has the model talking; asking again", id, model)
			continue
		}
		return got, nil
	}
	return nil, fmt.Errorf("claude: no answer")
}

// ImportantIDs picks the rulings of the given courts.
func ImportantIDs(meta map[int]Meta, courts []string) map[int]bool {
	want := map[string]bool{}
	for _, c := range courts {
		want[strings.TrimSpace(c)] = true
	}
	out := map[int]bool{}
	for id, m := range meta {
		if want[m.Court] {
			out[id] = true
		}
	}
	return out
}
