package site

import "github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"

// AmountDisplay is calculated wording, shown alongside the published source.
type AmountDisplay struct{ Text string }

func (l *Law) calculatedCurrent(a *Article) *calculatedcurrent.Record {
	state := "in_force"
	if l.Status == "repealed" || len(l.RepealedByChange) > 0 {
		state = "repealed"
	}
	pending := len(a.Pending()) > 0
	from := a.From
	if v := a.Latest(); v != nil {
		if state != "repealed" {
			state = v.State
		}
		if v.From != nil && (from == nil || v.From.After(*from)) {
			from = v.From
		}
		if v.State == "in_force" && v.TextAr == nil {
			pending = true
		}
	}
	if a.Repealed() {
		state = "repealed"
	}
	return calculatedcurrent.Calculate(calculatedcurrent.Input{LawID: l.ID, Number: a.Number, Source: a.Shown(), State: state, Pending: pending, Annulled: l.Annulled() || l.NotInForce() || a.AnnulledWhole(), ValidFrom: from})
}

// ArticleAmounts retains the template's existing Text contract.
func (l *Law) ArticleAmounts(a *Article) *AmountDisplay {
	if r := l.calculatedCurrent(a); r != nil {
		return &AmountDisplay{Text: r.TextAr}
	}
	return nil
}

// calculatedCollection provides the complete derived set for release parity.
func (l *Law) calculatedCollection() map[string]*calculatedcurrent.Record {
	out := map[string]*calculatedcurrent.Record{}
	seen := map[string]bool{}
	for _, a := range l.List {
		if seen[a.Number] {
			continue
		}
		seen[a.Number] = true
		if r := l.calculatedCurrent(a); r != nil {
			out[a.Number] = r
		}
	}
	return out
}
