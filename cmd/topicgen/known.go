package main

import (
	_ "embed"
	"fmt"
	"strings"
)

// known-quotes.tsv lists quotes "check" knows to fail for a reason outside
// the topic page, so the check can gate everything else. A listed quote
// that matches again fails the check until its line is removed: the list
// only shrinks.
//
//go:embed known-quotes.tsv
var knownQuotesTSV string

// knownQuote is one line of known-quotes.tsv.
type knownQuote struct{ Slug, Cite, Reason string }

func parseKnown(tsv string) ([]knownQuote, error) {
	var out []knownQuote
	for i, line := range strings.Split(tsv, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" || f[1] == "" || strings.TrimSpace(f[2]) == "" {
			return nil, fmt.Errorf("known-quotes.tsv line %d: want slug, cite id and reason, tab separated", i+1)
		}
		out = append(out, knownQuote{f[0], f[1], strings.TrimSpace(f[2])})
	}
	return out, nil
}

// applyKnown splits one topic's problems (topics.Check) into those that
// fail the check and the known ones. A known quote that no longer fails is
// a problem itself.
func applyKnown(slug string, problems []string, known []knownQuote) (bad, tolerated []string) {
	hit := map[string]bool{}
next:
	for _, p := range problems {
		for _, k := range known {
			if k.Slug == slug && strings.HasPrefix(p, fmt.Sprintf("%s: cite %s: the quote is not an exact piece of ", slug, k.Cite)) {
				hit[k.Cite] = true
				tolerated = append(tolerated, p+" ("+k.Reason+")")
				continue next
			}
		}
		bad = append(bad, p)
	}
	for _, k := range known {
		if k.Slug == slug && !hit[k.Cite] {
			bad = append(bad, fmt.Sprintf("%s: cite %s: listed in cmd/topicgen/known-quotes.tsv but its quote matches now: remove the line", slug, k.Cite))
		}
	}
	return bad, tolerated
}
