package nlsearch

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type Passage struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Label string `json:"label,omitempty"`
}
type Quote struct {
	SourceID string `json:"source_id"`
	Quote    string `json:"quote"`
}

func ValidateQuotes(qs []Quote, sources []Passage) ([]Quote, error) {
	if qs == nil || len(qs) > 3 {
		return nil, errors.New("invalid evidence selection")
	}
	byID := map[string]string{}
	for _, p := range sources {
		if _, exists := byID[p.ID]; p.ID == "" || exists {
			return nil, errors.New("invalid source set")
		}
		byID[p.ID] = p.Text
	}
	out := []Quote{}
	seen := map[string]bool{}
	for _, q := range qs {
		body, ok := byID[q.SourceID]
		if !ok || strings.TrimSpace(q.Quote) == "" || utf8.RuneCountInString(q.Quote) > 1000 || !strings.Contains(body, q.Quote) {
			return nil, errors.New("answer is not an exact excerpt of supplied evidence")
		}
		key := q.SourceID + "\x00" + q.Quote
		if !seen[key] {
			seen[key] = true
			out = append(out, q)
		}
	}
	return out, nil
}
