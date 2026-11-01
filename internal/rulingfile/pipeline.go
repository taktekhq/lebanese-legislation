package rulingfile

// The plan for each page: which readers it needs, and how their readings
// become the published text.
//
//	typed page        Gemini (A), Haiku (B), Tesseract votes; Sonnet, then
//	                  Opus, join while a dispute is left
//	handwritten page  Gemini (A), Opus (B), Sonnet votes
//	important ruling  every page also read by Opus and Gemini
//	(see PagePlan for pages Gemini refuses and pages read under the first plan)
//
// Every reader's text is kept as it came (<id>.readings.json), so a page can
// be merged again when a new reading arrives or the rules change, and anyone
// can check what each model wrote.

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Model ids.
const (
	Opus   = "claude-opus-5-5"
	Sonnet = "claude-sonnet-5-5"
	Haiku  = "claude-haiku-5-5"
	Gemini = "gemini-3.8-flash"
)

// Readings is every reader's text for a ruling: page -> model -> text.
type Readings map[int]map[string]string

// ReadingsPath is where a ruling's raw readings are kept under dir.
func ReadingsPath(dir string, id int) string {
	return strings.TrimSuffix(Path(dir, id), ".pdf") + ".readings.json"
}

// LoadReadings reads a ruling's raw readings (empty when there are none).
func LoadReadings(dir string, id int) (Readings, error) {
	b, err := os.ReadFile(ReadingsPath(dir, id))
	if os.IsNotExist(err) {
		return Readings{}, nil
	} else if err != nil {
		return nil, err
	}
	var raw map[string]map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := Readings{}
	for k, v := range raw {
		n, _ := strconv.Atoi(k)
		out[n] = v
	}
	return out, nil
}

// lock takes a ruling's lock, so stages running at once don't lose each other's work.
func lock(dir string, id int) (func(), error) {
	f, err := os.OpenFile(strings.TrimSuffix(Path(dir, id), ".pdf")+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// AddReadings stores one model's text for some pages, then rebuilds the
// ruling's published pages from everything it has.
func (r Router) AddReadings(dir string, id int, model string, pages map[int]string, t *Triage, meta Meta) error {
	unlock, err := lock(dir, id)
	if err != nil {
		return err
	}
	defer unlock()
	rs, err := LoadReadings(dir, id)
	if err != nil {
		return err
	}
	for n, text := range pages {
		if rs[n] == nil {
			rs[n] = map[string]string{}
		}
		rs[n][model] = text
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	if err := writeFile(ReadingsPath(dir, id), b); err != nil {
		return err
	}
	return r.assemble(dir, id, t, rs, meta)
}

// Plan is what a page needs before it can be published.
type Plan struct {
	A, B   string   // the two readings aligned
	Voters []string // readings that settle their disputes
	Need   []string // readers that must have read the page
	More   []string // readers to add, in order, while a dispute is left
}

// Readers that are not models: Tesseract's text (free, a voter on typed
// pages) and how many times Gemini refused the page ("1", "2").
const (
	Tesseract     = "tesseract"
	GeminiBlocked = "gemini-blocked"
	HaikuDeclined = "haiku-declined" // Haiku answered that it can't transcribe the page
)

// geminiGaveUp: Gemini refused the page twice (blockReason), so Claude reads it instead.
func geminiGaveUp(got map[string]string) bool { return got[GeminiBlocked] >= "2" }

// PagePlan decides a page's readers from its kind, what it has already, and
// what the last merge asked for. Every reading a page has always votes.
//
//	typed        Gemini (A) + Haiku (B), Tesseract votes; then Sonnet, then Opus
//	handwritten  Gemini (A) + Opus (B), Sonnet votes
//	Gemini refused the page: Claude only (typed Sonnet + Haiku, then Opus;
//	handwritten Opus + Sonnet, Haiku votes)
//	Haiku declined the page (it reads it as handwriting): Sonnet takes its place
//	read under the first plan (Opus, Sonnet and Haiku already in): kept
//	important ruling: Opus and Gemini always read it too
func (r Router) PagePlan(id int, p TriagePage, got map[string]string, want []string) Plan {
	hand := r.Tier(id, p) == TierGemini
	gaveUp := geminiGaveUp(got)
	declined := got[HaikuDeclined] != ""
	var pl Plan
	switch {
	case hand && !gaveUp:
		pl = Plan{A: Gemini, B: Opus, Voters: []string{Sonnet}}
	case hand:
		pl = Plan{A: Opus, B: Sonnet, Voters: []string{Haiku}}
	case got[Opus] != "" && got[Sonnet] != "" && got[Haiku] != "":
		pl = Plan{A: Opus, B: Sonnet, Voters: []string{Haiku}, More: []string{Gemini}}
	case !gaveUp && declined:
		pl = Plan{A: Gemini, B: Sonnet, Voters: []string{Tesseract}, More: []string{Opus}}
	case !gaveUp:
		pl = Plan{A: Gemini, B: Haiku, Voters: []string{Tesseract}, More: []string{Sonnet, Opus}}
	case declined:
		pl = Plan{A: Sonnet, B: Opus, Voters: []string{Tesseract}}
	default:
		pl = Plan{A: Sonnet, B: Haiku, Voters: []string{Tesseract}, More: []string{Opus}}
	}
	if declined {
		pl.Voters = slices.DeleteFunc(pl.Voters, func(m string) bool { return m == Haiku })
	}
	if gaveUp {
		pl.More = slices.DeleteFunc(pl.More, func(m string) bool { return m == Gemini })
	}
	in := func(m string) bool { return m == pl.A || m == pl.B || slices.Contains(pl.Voters, m) }
	add := func(m string) {
		if !in(m) && !(m == Gemini && gaveUp) {
			pl.Voters = append(pl.Voters, m)
		}
	}
	if r.Important[id] {
		add(Opus)
		add(Gemini)
	}
	for _, m := range want {
		add(m)
	}
	for _, m := range []string{Gemini, Opus, Sonnet, Haiku, Tesseract} {
		if got[m] != "" {
			add(m)
		}
	}
	pl.Need = append([]string{pl.A, pl.B}, pl.Voters...)
	return pl
}

// wants lists, per page, the readers the last merge asked for.
func wants(rd *Reading) map[int][]string {
	out := map[int][]string{}
	if rd != nil {
		for _, p := range rd.Pages {
			out[p.N] = p.Want
			if p.NeedGemini { // the first plan's flag
				out[p.N] = append(out[p.N], Gemini)
			}
		}
	}
	return out
}

// Missing lists, for a reader, the pages of a ruling it still has to read.
func (r Router) Missing(t *Triage, rd *Reading, rs Readings, model string) []int {
	w := wants(rd)
	var out []int
	for _, p := range t.Pages {
		if p.Blank {
			continue
		}
		pl := r.PagePlan(t.ID, p, rs[p.N], w[p.N])
		if slices.Contains(pl.Need, model) && rs[p.N][model] == "" {
			out = append(out, p.N)
		}
	}
	return out
}

// assemble publishes every page whose readers are all in. A page with a
// dispute left asks for the next reader in its plan; one whose header doesn't
// match the Centre's record asks for Gemini (or Opus, if Gemini refused it).
//
// A page still waiting for its other readers is published with the one
// reading it has (Gemini first), its model a single name; the vote replaces it
// once its readers are in. Since 24 Oct 2026 every page we read goes live.
// Each page also carries LowConfidence (handwriting, or a model declined it),
// which the site shows as a warning; printed pages are trusted.
func (r Router) assemble(dir string, id int, t *Triage, rs Readings, meta Meta) error {
	rd, err := LoadReading(dir, id)
	if err != nil {
		return err
	}
	if rd == nil {
		rd = &Reading{ID: id, Prompt: PromptVersion}
	}
	byN := map[int]Page{}
	for _, p := range rd.Pages {
		byN[p.N] = p
	}
	w := wants(rd)
	tps := map[int]TriagePage{}
	for _, tp := range t.Pages {
		tps[tp.N] = tp
		if tp.Blank { // nothing to read: published empty, marked blank
			byN[tp.N] = Page{N: tp.N, Model: BlankModel, Blank: true}
			continue
		}
		got := rs[tp.N]
		pl := r.PagePlan(id, tp, got, w[tp.N])
		ready := true
		for _, m := range pl.Need {
			if got[m] == "" {
				ready = false
			}
		}
		low := r.Tier(id, tp) == TierGemini || got[HaikuDeclined] != ""
		if !ready {
			p, ok := byN[tp.N]
			if !ok || p.Text == "" || !p.Voted() {
				for _, m := range []string{Gemini, Opus, Sonnet, Haiku} {
					if got[m] != "" {
						p, ok = Page{N: tp.N, Text: got[m], Model: m}, true
						break
					}
				}
			}
			if ok { // keep the request standing until its reader comes
				p.Want = w[tp.N]
				p.LowConfidence = low
				byN[tp.N] = p
			}
			continue
		}
		var voters []string
		for _, m := range pl.Voters {
			voters = append(voters, got[m])
		}
		text, v := Merge3(got[pl.A], got[pl.B], voters...)
		p := Page{N: tp.N, Text: text, Model: strings.Join(pl.Need, "+"), Words: v.Words,
			Agreed: v.Agreed, Settled: v.Settled, Disputed: v.Disputed}
		if v.Disputed > 0 {
			for _, m := range pl.More {
				if !slices.Contains(pl.Need, m) {
					p.Want = []string{m}
					break
				}
			}
		}
		p.LowConfidence = low
		byN[tp.N] = p
	}
	// The header (page 1, sometimes 2) must show the Centre's number and year
	// or president; if it doesn't, a stronger reader reads those pages too.
	// Only voted pages count: a single reading of handwriting often
	// misreads the number, and the mismatch is meant for after every reader.
	var head strings.Builder
	for _, n := range []int{1, 2} {
		if byN[n].Voted() {
			head.WriteString(byN[n].Text + "\n")
		}
	}
	rd.HeaderMismatch = false
	if p, ok := byN[1]; ok && p.Voted() && !CheckHeader(head.String(), meta) {
		rd.HeaderMismatch = true
		for _, n := range []int{1, 2} {
			p, ok := byN[n]
			if !ok {
				continue
			}
			for _, m := range []string{Gemini, Opus} {
				if rs[n][m] == "" && !(m == Gemini && geminiGaveUp(rs[n])) && !slices.Contains(p.Want, m) {
					p.Want = append(p.Want, m)
					break
				}
			}
			byN[n] = p
		}
	}
	rd.Pages = rd.Pages[:0]
	for _, p := range byN {
		p.NeedGemini = false // replaced by Want
		rd.Pages = append(rd.Pages, p)
	}
	sort.Slice(rd.Pages, func(i, j int) bool { return rd.Pages[i].N < rd.Pages[j].N })
	rd.Model = ""
	b, err := json.MarshalIndent(rd, "", " ")
	if err != nil {
		return err
	}
	return writeFile(ReadPath(dir, id), b)
}

// Published reports whether every page has its final text: all readers in
// and none still asked for.
func Published(t *Triage, rd *Reading) bool {
	if t == nil || rd == nil {
		return false
	}
	have := map[int]bool{}
	for _, p := range rd.Pages {
		have[p.N] = len(p.Want) == 0 && p.Voted()
	}
	for _, p := range t.Pages {
		if !have[p.N] {
			return false
		}
	}
	return true
}

// Remerge votes every ruling again from its stored readings, after a change
// to the voting rules. No model is called.
func (r Router) Remerge(dir string, meta map[int]Meta, log func(string, ...any)) error {
	ids, err := Downloaded(dir)
	if err != nil {
		return err
	}
	n := 0
	for _, id := range ids {
		t, err := LoadTriage(dir, id)
		if err != nil || t == nil {
			continue
		}
		if _, err := os.Stat(ReadingsPath(dir, id)); err != nil {
			continue
		}
		if err := r.AddReadings(dir, id, "", nil, t, meta[id]); err != nil {
			return err
		}
		n++
	}
	log("merge: voted %d rulings again", n)
	return nil
}
