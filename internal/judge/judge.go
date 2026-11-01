package judge

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PromptState is the version of the prompt; a new wording of the prompt gets
// a new version so old answers can be told apart. Answers to the earlier
// "apply-1" prompt carry the same state and stay valid.
const PromptState = "state-1"

var prompts = map[string]bool{"apply-1": true, PromptState: true}

// The model only judges; it never writes or rewrites the law's wording.
const systemState = `You are a careful editor of Lebanese legislation. You compare an amending provision with a published text of the article it amends, and decide whether that published text already reflects the change.

Rules:
- Judge only; do not rewrite anything.
- Answer in JSON only.`

var schemaState = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"state": map[string]any{"type": "STRING", "enum": []string{"includes", "predates", "later", "unclear"},
			"description": "includes: the published article already reflects this change. predates: it is the wording from before the change. later: it reflects a different, later wording. unclear: cannot tell."},
		"reason": map[string]any{"type": "STRING", "description": "One short sentence in English."},
	},
	"required":         []string{"state", "reason"},
	"propertyOrdering": []string{"state", "reason"},
}

// Item is one change for the model to judge.
type Item struct {
	Key                string
	Input              string
	Amending, Centre   string
	Article, Part      string
	LawID              int
	Action             string
	ByIdent, ByTitle   string
	LawIdent, LawTitle string
	Effective          string
}

// Items selects the changes to judge: those whose order against the Centre's
// text is unknown, and those that change part of an article. scope "codes"
// keeps the principal codes only.
func Items(ctx context.Context, db *pgxpool.Pool, scope string) ([]Item, error) {
	where := ""
	if scope == "codes" {
		where = "AND x.law_id IN (SELECT law_id FROM code)"
	}
	rows, err := db.Query(ctx, `SELECT x.by_law_id, x.by_article, x.seq, x.article_number, coalesce(x.part, ''), x.action, x.law_id,
		coalesce(to_char(x.effective_on, 'DD/MM/YYYY'), ''), ba.text_ar, ta.text_ar,
		bl.kind || coalesce(' رقم ' || bl.number, '') || coalesce(' تاريخ ' || to_char(bl.issued_on, 'DD/MM/YYYY'), ''), bl.title,
		tl.kind || coalesce(' رقم ' || tl.number, '') || coalesce(' تاريخ ' || to_char(tl.issued_on, 'DD/MM/YYYY'), ''), tl.title
		FROM amendment x
		JOIN legislation_article ba ON ba.law_id = x.by_law_id AND ba.number = x.by_article
		JOIN legislation_article ta ON ta.law_id = x.law_id AND ta.number = x.article_number
		JOIN legislation bl ON bl.id = x.by_law_id JOIN legislation tl ON tl.id = x.law_id
		WHERE x.article_number IS NOT NULL AND ta.text_ar <> ''
		  AND ((x.status = 'review' AND x.reason LIKE '%matches no version%') OR x.status = 'partial') `+where+`
		ORDER BY x.law_id, x.article_number, x.effective_on`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var byLaw, seq int
		var byArticle string
		if err := rows.Scan(&byLaw, &byArticle, &seq, &it.Article, &it.Part, &it.Action, &it.LawID, &it.Effective, &it.Amending, &it.Centre,
			&it.ByIdent, &it.ByTitle, &it.LawIdent, &it.LawTitle); err != nil {
			return nil, err
		}
		it.Key = Key(byLaw, byArticle, seq, it.Article)
		it.Input = InputHash(it.Amending, it.Centre, it.Article, it.Part)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (it Item) prompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "AMENDING TEXT: %s (%s)", it.ByIdent, it.ByTitle)
	if it.Effective != "" {
		fmt.Fprintf(&b, ", published %s", it.Effective)
	}
	fmt.Fprintf(&b, "\nTHE AMENDING PROVISION:\n<<<\n%s\n>>>\n\n", strings.TrimSpace(it.Amending))
	fmt.Fprintf(&b, "AMENDED TEXT: %s (%s)\nARTICLE %s AS PUBLISHED (it may be older or newer than the change):\n<<<\n%s\n>>>\n\n",
		it.LawIdent, it.LawTitle, it.Article, strings.TrimSpace(it.Centre))
	switch {
	case it.Part == "+":
		fmt.Fprintf(&b, "The provision adds to article %s.", it.Article)
	case it.Part != "":
		fmt.Fprintf(&b, "The provision changes part of article %s: «%s».", it.Article, it.Part)
	case it.Action == "repeal":
		fmt.Fprintf(&b, "The provision repeals article %s.", it.Article)
	default:
		fmt.Fprintf(&b, "The provision changes article %s.", it.Article)
	}
	b.WriteString(" If it changes other articles too, consider only this one.\n\nSay whether the published article includes the change, predates it, or reflects a later wording.")
	return b.String()
}

// Stats is what a run did.
type Stats struct {
	Asked, Skipped, Failed int
	States                 map[string]int
	Usage                  Usage
}

// Run asks the model about each item not already judged on the same input,
// checks the answers and saves them to path as it goes.
func Run(ctx context.Context, g Asker, items []Item, path string, workers, limit int, log func(string, ...any)) (Stats, error) {
	st := Stats{States: map[string]int{}}
	have, err := Load(path)
	if err != nil {
		return st, err
	}
	var todo []Item
	for _, it := range items {
		if j, ok := have[it.Key]; ok && j.Input == it.Input && prompts[j.Prompt] {
			st.Skipped++
			continue
		}
		todo = append(todo, it)
	}
	if limit > 0 && len(todo) > limit {
		todo = todo[:limit]
	}
	log("%d to ask, %d already judged", len(todo), st.Skipped)
	var mu sync.Mutex
	jobs := make(chan Item)
	var wg sync.WaitGroup
	for w := 0; w < max(workers, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				var ans struct {
					State  string `json:"state"`
					Reason string `json:"reason"`
				}
				model, u, err := g.Ask(ctx, systemState, it.prompt(), schemaState, &ans)
				mu.Lock()
				st.Usage.In += u.In
				st.Usage.Out += u.Out
				st.Usage.Thinking += u.Thinking
				if err != nil {
					st.Failed++
					log("%s: %v", it.Key, err)
					mu.Unlock()
					continue
				}
				j := Judgement{Key: it.Key, Input: it.Input, Task: "state", Prompt: PromptState, Model: model,
					State: ans.State, Reason: ans.Reason, At: time.Now().UTC().Format(time.RFC3339)}
				st.Asked++
				st.States[j.State]++
				have[it.Key] = j
				if st.Asked%10 == 0 {
					if err := Save(path, have); err != nil {
						log("save: %v", err)
					}
					log("%d asked (%v)", st.Asked, st.States)
				}
				mu.Unlock()
			}
		}()
	}
	for _, it := range todo {
		select {
		case jobs <- it:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return st, Save(path, have)
}
