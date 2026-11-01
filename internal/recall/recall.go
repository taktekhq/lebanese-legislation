// Package recall is a nightly check:
// of the articles that carry an amending verb, how many produced an amendment row.
package recall

import (
	"fmt"
	"sort"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

const (
	// MaxDrop is how far the ratio may fall below the baseline, in points (0.5).
	MaxDrop = 0.005
	// MaxNewGroup is the size above which a miss group not in the baseline fails.
	MaxNewGroup = 25
	// HeadWords is how many opening words key a miss group.
	HeadWords = 3
)

// Article is one article: its law, number and text.
type Article struct {
	LawID  int
	Number string
	Text   string
}

// Key is an amendment's amending article, (by_law_id, by_article).
type Key struct {
	LawID  int
	Number string
}

// Group is the candidates with no row that open with the same words.
type Group struct {
	Head  string `json:"head"`
	Count int    `json:"count"`
}

// Report is one night's count. Groups is every miss group, largest first.
type Report struct {
	Candidates int     `json:"candidates"`
	Produced   int     `json:"produced"`
	Ratio      float64 `json:"ratio"`
	Groups     []Group `json:"groups"`
}

// Count reads articles against the amendment rows' amending articles.
func Count(articles []Article, rows map[Key]bool) Report {
	r := Report{}
	miss := map[string]int{}
	for _, a := range articles {
		if !amend.HasVerb(a.Text) {
			continue
		}
		r.Candidates++
		if rows[Key{a.LawID, a.Number}] {
			r.Produced++
		} else {
			miss[amend.Opening(a.Text, HeadWords)]++
		}
	}
	if r.Candidates > 0 {
		r.Ratio = float64(r.Produced) / float64(r.Candidates)
	}
	for h, n := range miss {
		r.Groups = append(r.Groups, Group{h, n})
	}
	sort.Slice(r.Groups, func(i, j int) bool {
		if r.Groups[i].Count != r.Groups[j].Count {
			return r.Groups[i].Count > r.Groups[j].Count
		}
		return r.Groups[i].Head < r.Groups[j].Head
	})
	return r
}

// Compare returns "" when tonight passes against the baseline, else why not.
// It passes when the ratio is at least the baseline's minus MaxDrop and no
// miss group that the baseline did not have is larger than MaxNewGroup.
func Compare(now, base Report) string {
	known := map[string]bool{}
	for _, g := range base.Groups {
		known[g.Head] = true
	}
	var big *Group
	for i, g := range now.Groups { // largest first
		if !known[g.Head] && g.Count > MaxNewGroup {
			big = &now.Groups[i]
			break
		}
	}
	low := now.Ratio < base.Ratio-MaxDrop-1e-12
	if !low && big == nil {
		return ""
	}
	why := fmt.Sprintf("recall %.2f%% (%d of %d), baseline %.2f%%", now.Ratio*100, now.Produced, now.Candidates, base.Ratio*100)
	if big != nil {
		why += fmt.Sprintf("; biggest new miss group %q, %d articles", big.Head, big.Count)
	} else if len(now.Groups) > 0 {
		why += fmt.Sprintf("; biggest miss group %q, %d articles", now.Groups[0].Head, now.Groups[0].Count)
	}
	return why
}
