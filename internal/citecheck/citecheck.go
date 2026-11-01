// Package citecheck is a nightly check:
// the texts added last night were read for citations, nothing from them is
// pending, every judgement was given the article it names, and the cite and
// judge logs show no failed items. It reads; it changes nothing.
package citecheck

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Article is an amending article of a new text, as cite.Items reads it now.
type Article struct{ Key, Input string }

// Input is everything the check compares; Load fills it from the database
// and the model files.
type Input struct {
	New        []Article                  // amending articles of texts added in the last 24 hours
	Citations  map[string]cite.Citation   // db/model/citations.jsonl
	Gaps       map[string][]string        // citation key -> targets that match nothing in our data
	Judged     map[string]judge.Judgement // judgements in scope: made in the last 24 hours or about a new text
	Recomputed map[string]string          // judgement key -> input hash recomputed from the database now
	Current    []int                      // every law id in the database now (with -prev)
	LogLines   []string                   // the new part of the judge and cite logs
}

// Result is what the check found. Violations fail it; Gaps never do.
type Result struct {
	Violations []string
	Gaps       []string
}

// OK says whether the check passes.
func (r Result) OK() bool { return len(r.Violations) == 0 }

var failedRE = regexp.MustCompile(`\bfailed (\d+)`)

// Check applies the four conditions.
func Check(in Input) Result {
	var r Result
	for _, a := range in.New {
		c, ok := in.Citations[a.Key]
		switch {
		case !ok:
			r.Violations = append(r.Violations, fmt.Sprintf("no citation reading: article %s of a new text (karim)", a.Key))
		case c.Input != a.Input || c.Prompt != cite.Prompt:
			r.Violations = append(r.Violations, fmt.Sprintf("pending citation: article %s has a reading of older text or prompt (karim)", a.Key))
		}
	}
	for _, k := range sortedKeys(in.Judged) {
		want, ok := in.Recomputed[k]
		if ok && want != in.Judged[k].Input {
			r.Violations = append(r.Violations, fmt.Sprintf("judgement %s: input %s is not the article and version in its key (now %s) ", k, in.Judged[k].Input, want))
		}
	}
	for _, l := range in.LogLines {
		for _, m := range failedRE.FindAllStringSubmatch(l, -1) {
			if n, _ := strconv.Atoi(m[1]); n > 0 {
				r.Violations = append(r.Violations, fmt.Sprintf("log reports failed %d: %s", n, l))
			}
		}
	}
	for _, a := range in.New {
		for _, g := range in.Gaps[a.Key] {
			r.Gaps = append(r.Gaps, fmt.Sprintf("%s: %s", a.Key, g))
		}
	}
	sort.Strings(r.Gaps)
	return r
}

func sortedKeys(m map[string]judge.Judgement) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
