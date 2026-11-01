package modelout

import (
	"fmt"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Judgements are a model's checked answers about amendments the patterns
// couldn't settle (db/model/judgements.jsonl, written by lebleg judge). They
// decide which version of an article stands, never what a version says: the
// build gets a Verdict, a state and the model's name, and no text.
type Judgements struct{ m map[string]judge.Judgement }

// Verdict is the judge's answer about one change.
type Verdict struct {
	Model Model // the model whose judgement applied
	State State // includes, predates, later, unclear
}

// Model names the model that answered.
type Model string

// State is the judge's answer: whether the Centre's text already shows a
// change (includes), predates it, is later than it, or the model can't tell.
type State string

// LoadJudgements reads the embedded db/model/judgements.jsonl.
func LoadJudgements() (Judgements, error) {
	js, err := judge.Parse([]byte(db.Judgements))
	if err != nil {
		return Judgements{}, fmt.Errorf("db/model/judgements.jsonl: %w", err)
	}
	return Judgements{m: js}, nil
}

// Verdict is the model's answer about the change byLaw/byArticle makes
// (its seq-th) to article, when there is one for the same inputs it was
// shown: the amending text, the Centre's text and the part changed.
func (js Judgements) Verdict(byLaw int, byArticle string, seq int, article, amending, centre, part string) (Verdict, bool) {
	j, ok := js.m[judge.Key(byLaw, byArticle, seq, article)]
	if !ok || j.Input != judge.InputHash(amending, centre, article, part) {
		return Verdict{}, false
	}
	return Verdict{Model: Model(j.Model), State: State(j.State)}, true
}
