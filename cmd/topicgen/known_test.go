package main

import (
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

func TestKnownQuotesParse(t *testing.T) {
	known, err := parseKnown(knownQuotesTSV)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range known {
		tp := topics.Get(k.Slug)
		if tp == nil || tp.Cite(k.Cite) == nil {
			t.Errorf("known-quotes.tsv: %s has no cite %s", k.Slug, k.Cite)
		}
	}
	if _, err := parseKnown("slug\tcite\n"); err == nil {
		t.Error("a line without a reason parsed")
	}
}

func TestApplyKnown(t *testing.T) {
	known := []knownQuote{{"nssf", "a1", "the page prints one clause"}, {"nssf", "a2", "the page prints one clause"}, {"other", "a1", "x"}}
	problems := []string{
		`nssf: cite a1: the quote is not an exact piece of article 9 of law 244971: "الاجراء"`,
		`nssf: cite a3: the quote is not an exact piece of article 9 of law 244971: "يستفيد"`,
		`nssf: cite a1: article 9 of law 244971 does not exist`,
		`nssf: [en] answer: number 7 is in no cited article: "x"`,
	}
	bad, tolerated := applyKnown("nssf", problems, known)
	if len(tolerated) != 1 || !strings.Contains(tolerated[0], "cite a1") || !strings.HasSuffix(tolerated[0], "(the page prints one clause)") {
		t.Errorf("tolerated = %q", tolerated)
	}
	// a3 is not listed, a missing article and a number are never tolerated,
	// and a2 is listed but did not fail.
	if len(bad) != 4 || !strings.Contains(bad[3], "cite a2: listed in") {
		t.Errorf("bad = %q", bad)
	}
}
