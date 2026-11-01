// Package calculatedcurrent applies only the reviewed Civil Procedure monetary adjustment.
package calculatedcurrent

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"
)

//go:embed civil-procedure-amounts.tsv
var civilProcedureAmountData string

type Authority struct {
	LawID       int    `json:"law_id"`
	Article     string `json:"article"`
	URL         string `json:"url"`
	EffectiveOn string `json:"effective_on"`
}
type Record struct {
	Kind               string    `json:"kind"`
	TextAr             string    `json:"text_ar"`
	SourceTextAr       string    `json:"source_text_ar"`
	SourceSHA256       string    `json:"source_sha256"`
	IsPublishedWording bool      `json:"is_published_wording"`
	Multiplier         int       `json:"multiplier"`
	Authority          Authority `json:"authority"`
}

// Input carries current wording and eligibility, never a historical quote.
type Input struct {
	LawID                 int
	Number, Source, State string
	Pending, Annulled     bool
	ValidFrom             *time.Time
}
type amountReplacement struct {
	words        string
	base         int64
	updatedWords string
}
type amountBaseline struct {
	hash         string
	replacements []amountReplacement
}

var civilProcedureAmounts = func() map[string]amountBaseline {
	out := map[string]amountBaseline{}
	for _, line := range strings.Split(civilProcedureAmountData, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 || strings.TrimSpace(f[4]) == "" {
			panic("invalid Civil Procedure amount row")
		}
		n, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil || n <= 0 || n > 1<<63/50 {
			panic("invalid Civil Procedure amount")
		}
		b := out[f[0]]
		if b.hash != "" && b.hash != f[1] {
			panic("conflicting Civil Procedure amount baseline")
		}
		b.hash = f[1]
		b.replacements = append(b.replacements, amountReplacement{f[2], n, f[4]})
		out[f[0]] = b
	}
	return out
}()

// Calculate returns nil unless the current input is eligible and its hash reviewed.
func Calculate(in Input) *Record {
	now := time.Now()
	if in.LawID != 244565 || in.State != "in_force" || in.Pending || in.Annulled || (in.ValidFrom != nil && in.ValidFrom.After(now)) || now.Before(time.Date(2024, 9, 19, 0, 0, 0, 0, time.UTC)) {
		return nil
	}
	b, ok := civilProcedureAmounts[in.Number]
	if !ok {
		return nil
	}
	source := in.Source
	if fmt.Sprintf("%x", sha256.Sum256([]byte(source))) != b.hash {
		return nil
	}
	pairs := make([]string, 0, 2*len(b.replacements))
	for _, r := range b.replacements {
		pairs = append(pairs, r.words, groupAmount(r.base*50)+" ("+r.updatedWords+")")
	}
	return &Record{Kind: "statutory_monetary_adjustment", TextAr: strings.NewReplacer(pairs...).Replace(source), SourceTextAr: source, SourceSHA256: b.hash, Multiplier: 50, Authority: Authority{LawID: 330819, Article: "1", URL: "https://lebaneselegislation.com/decree/330819/article/1/", EffectiveOn: "2024-09-19"}}
}

func groupAmount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
