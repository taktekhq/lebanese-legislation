package cite

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Name is the model as recorded in each answer.
func (a *Agy) Name() string { return "agy:" + a.Model }

// Open starts a worker's agy session when it first reads.
func (a *Agy) Open() Session { return &agySession{a: a} }

// agySession keeps one agy process, started on the first read and replaced
// after maxUses prompts or any failure.
type agySession struct {
	a    *Agy
	proc *agyProc
}

func (s *agySession) Read(prompt string) (string, error) {
	if s.proc == nil || s.proc.uses >= maxUses {
		s.Close()
		p, err := s.a.start()
		if err != nil {
			return "", err
		}
		s.proc = p
	}
	resp, err := s.proc.prompt(prompt, s.a.Timeout)
	if err != nil {
		s.Close()
		return "", err
	}
	return resp, nil
}

func (s *agySession) Close() {
	if s.proc != nil {
		s.proc.stop()
		s.proc = nil
	}
}

// RunAgy reads articles in batches through agy (see RunBatches).
func RunAgy(ctx context.Context, a *Agy, items []Item, path string, workers, batch, limit int, redo string, log func(string, ...any)) (Stats, error) {
	return RunBatches(ctx, a, Articles, items, path, workers, batch, limit, redo, log)
}

// batches cuts items into calls of at most n items and, when maxBytes is
// set, that many bytes of their part of the prompt (an item over it goes
// alone).
func batches(todo []Item, n, maxBytes int, part func(Item) string) [][]Item {
	var out [][]Item
	var cur []Item
	size := 0
	for _, it := range todo {
		l := len(part(it))
		if len(cur) > 0 && (len(cur) >= n || (maxBytes > 0 && size+l > maxBytes)) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, it)
		size += l
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// RunBatches reads, in batches through a backend (agy or claude), the items
// not yet read with the source's prompt and the same input; redo asks again
// about items a model whose name contains it answered. The answers are saved
// to path after every batch.
func RunBatches(ctx context.Context, be Backend, src Source, items []Item, path string, workers, batch, limit int, redo string, log func(string, ...any)) (Stats, error) {
	st := Stats{Keep: strings.HasPrefix(redo, "keep:")}
	have, err := Load(path)
	if err != nil {
		return st, err
	}
	todo := selectTodo(have, items, src, redo, limit, &st)
	log("%d %s to read through %s, %d already read", len(todo), src.Name, be.Name(), st.Skipped)
	model := be.Name()
	var mu sync.Mutex
	jobs := make(chan []Item)
	var wg sync.WaitGroup
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := be.Open()
			defer sess.Close()
			ask := func(b []Item) (map[string]answer, error) {
				p := src.prompt(b)
				mu.Lock()
				st.Calls++
				st.PromptChars += len([]rune(p))
				mu.Unlock()
				resp, err := sess.Read(p)
				if err != nil {
					return nil, err
				}
				return parseBatch(resp)
			}
			for b := range jobs {
				got, err := ask(b)
				if err != nil { // one retry, on a fresh session
					sess.Close()
					got, err = ask(b)
				}
				mu.Lock()
				if err != nil {
					st.Failed += len(b)
					log("batch of %d from %s: %v", len(b), b[0].Key, err)
					mu.Unlock()
					continue
				}
				for _, it := range b {
					ans, ok := got[it.Key]
					if !ok {
						st.Failed++
						continue
					}
					st.record(have, src, it, model, ans)
				}
				if err := Save(path, have); err != nil {
					log("save: %v", err)
				}
				log("%d read, %d failed, %d changes (%d with a quote not in the text)", st.Asked, st.Failed, st.Changes, st.Invalid)
				mu.Unlock()
			}
		}()
	}
	for _, b := range batches(todo, max(batch, 1), src.MaxBytes, src.Part) {
		select {
		case jobs <- b:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return st, Save(path, have)
}

// selectTodo keeps the items not yet read with this prompt and input (any
// model's answer counts, unless redo names a model to ask again), in the
// source's order, at most limit of them (0: all). Skipped counts the rest.
func selectTodo(have map[string]Citation, items []Item, src Source, redo string, limit int, st *Stats) []Item {
	var todo []Item
	for _, it := range items {
		if c, ok := have[it.Key]; ok && c.Input == it.Input && c.Prompt == src.Prompt && !wantRedo(c, redo) {
			st.Skipped++
			continue
		}
		todo = append(todo, it)
	}
	if src.Order != nil {
		src.Order(todo)
	}
	if limit > 0 && len(todo) > limit {
		todo = todo[:limit]
	}
	return todo
}

// record keeps a model's answer about an item: its changes, each checked
// against the item's own words, and counts it.
func (st *Stats) record(have map[string]Citation, src Source, it Item, model string, ans answer) {
	c := Citation{Key: it.Key, Input: it.Input, Prompt: src.Prompt, Model: model, Amends: ans.Amends,
		Changes: check(it.Text, ans), At: time.Now().UTC().Format(time.RFC3339)}
	if old, ok := have[it.Key]; ok && st.Keep && old.Input == it.Input && old.Prompt == src.Prompt {
		c = keepValid(c, old)
	}
	st.Asked++
	st.TextChars += len([]rune(it.Text))
	st.Changes += len(c.Changes)
	for _, ch := range c.Changes {
		if !ch.Valid {
			st.Invalid++
		}
	}
	have[it.Key] = c
}

// wantRedo says whether redo asks again about an answer: redo "" never,
// "sonnet" when the model's name contains it, and "invalid:sonnet" only when
// that model's answer also has a change whose quote is not in the text
// ("invalid:" alone: any model's). A leading "keep:" selects the same way
// and makes the run keep the earlier answer's valid citations (Stats.Keep).
func wantRedo(c Citation, redo string) bool {
	if redo == "" {
		return false
	}
	model, onlyInvalid := strings.CutPrefix(strings.TrimPrefix(redo, "keep:"), "invalid:")
	if !strings.Contains(c.Model, model) {
		return false
	}
	if !onlyInvalid {
		return true
	}
	for _, ch := range c.Changes {
		if !ch.Valid {
			return true
		}
	}
	return false
}

// keepValid adds to a new answer the earlier answer's citations whose quote
// is in the text and whose target the new answer does not already give
// (same Target.ID, whatever the action), and names both models.
func keepValid(c, old Citation) Citation {
	seen := map[string]bool{}
	for _, ch := range c.Changes {
		seen[ch.Target.ID()] = true
	}
	kept := 0
	for _, ch := range old.Changes {
		k := ch.Target.ID()
		if ch.Valid && !seen[k] {
			seen[k] = true
			c.Changes = append(c.Changes, ch)
			kept++
		}
	}
	if kept > 0 {
		c.Model += "+" + old.Model
		c.Amends = c.Amends || old.Amends
	}
	return c
}
