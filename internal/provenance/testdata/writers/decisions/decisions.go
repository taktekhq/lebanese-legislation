// Package decisions is a fixture of a package that reads model output and
// hands back decisions: a State is a decision, a plain string is free text.
// Declared a decision package, its Wording is flagged and its State is not.
package decisions

import "os"

// State is a decision's value.
type State string

// Answer is what the model said.
type Answer struct {
	State State
	Note  string // free text
}

func read() []byte {
	b, _ := os.ReadFile("db/model/judgements.jsonl")
	return b
}

// Verdict is a decision: allowed.
func Verdict(key string) (State, bool) { return State(read()), true }

// Wording is the model's text: free text.
func Wording(key string) string { return string(read()) }

// Full carries free text in a field.
func Full(key string) (*Answer, error) { return &Answer{}, nil }
