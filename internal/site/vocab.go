package site

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
)

type vocab struct {
	freq   map[string]int
	bySkel map[string][]string
}

func arNormGo(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x064B && r <= 0x065F, r == 0x0670, r == 0x0640:
			continue
		case r == 'أ', r == 'إ', r == 'آ', r == 'ٱ':
			r = 'ا'
		case r == 'ى':
			r = 'ي'
		case r == 'ة':
			r = 'ه'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func arSkeleton(w string) string {
	w = arNormGo(w)
	if r := []rune(w); len(r) > 4 && strings.HasPrefix(w, "ال") {
		w = string(r[2:])
	}
	var b strings.Builder
	for _, r := range w {
		switch r {
		case 'ا', 'و', 'ي', 'ى':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func newVocab(freq map[string]int) *vocab {
	v := &vocab{freq: freq, bySkel: map[string][]string{}}
	for w := range freq {
		if sk := arSkeleton(w); len([]rune(sk)) >= 2 {
			v.bySkel[sk] = append(v.bySkel[sk], w)
		}
	}
	for _, ws := range v.bySkel {
		sort.Slice(ws, func(i, j int) bool {
			if v.freq[ws[i]] != v.freq[ws[j]] {
				return v.freq[ws[i]] > v.freq[ws[j]]
			}
			return ws[i] < ws[j]
		})
	}
	return v
}

func loadVocab(ctx context.Context, db *pgxpool.Pool) (*vocab, error) {
	rows, err := db.Query(ctx, `SELECT w, count(*)::int FROM (SELECT unnest(string_to_array(ar_norm(title), ' ')) w FROM legislation) t
		WHERE length(w) >= 3 AND w ~ '^[ء-ي]+$' GROUP BY w`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	freq := map[string]int{}
	for rows.Next() {
		var w string
		var n int
		if err := rows.Scan(&w, &n); err != nil {
			return nil, err
		}
		freq[w] = n
	}
	return newVocab(freq), rows.Err()
}

func (v *vocab) has(w string) bool { return v != nil && v.freq[w] > 0 }

func maxEdits(n int) int {
	switch {
	case n < 4:
		return 0
	case n < 8:
		return 1
	}
	return 2
}

func (v *vocab) nearest(w string) (string, bool) {
	if v == nil {
		return "", false
	}
	w = arNormGo(w)
	if v.freq[w] > 0 {
		return w, true
	}
	wr := []rune(w)
	limit := maxEdits(len(wr))
	if limit == 0 {
		return "", false
	}
	best, bestD, bestN := "", limit+1, 0
	for c, n := range v.freq {
		cr := []rune(c)
		if d := len(cr) - len(wr); d > limit || d < -limit {
			continue
		}
		d := editWithin(wr, cr, limit)
		if d > limit {
			continue
		}
		if d < bestD || (d == bestD && (n > bestN || (n == bestN && c < best))) {
			best, bestD, bestN = c, d, n
		}
	}
	return best, bestD <= limit
}

func (v *vocab) bySkeleton(sk string) (string, bool) { return v.bySkeletonEnding(sk, "") }

func (v *vocab) bySkeletonExact(sk, ending string) (string, bool) {
	if v == nil || len([]rune(sk)) < 3 || len(v.bySkel[sk]) == 0 {
		return "", false
	}
	return v.bySkeletonEnding(sk, ending)
}

func (v *vocab) bySkeletonEnding(sk, ending string) (string, bool) {
	if v == nil || len([]rune(sk)) < 3 {
		return "", false
	}
	if ws := v.bySkel[sk]; len(ws) > 0 {
		if ending != "" {
			for _, w := range ws {
				if r := []rune(w); strings.ContainsRune(ending, r[len(r)-1]) {
					return w, true
				}
			}
		}
		return ws[0], true
	}
	skr := []rune(sk)
	best, bestN := "", 0
	for k, ws := range v.bySkel {
		kr := []rune(k)
		if d := len(kr) - len(skr); d > 1 || d < -1 {
			continue
		}
		if editWithin(skr, kr, 1) <= 1 && v.freq[ws[0]] > bestN {
			best, bestN = ws[0], v.freq[ws[0]]
		}
	}
	return best, best != ""
}

func editWithin(a, b []rune, max int) int {
	if d := len(a) - len(b); d > max || d < -max {
		return max + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		low := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			low = min(low, cur[j])
		}
		if low > max {
			return max + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func isArabicWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !unicode.Is(unicode.Arabic, r) {
			return false
		}
	}
	return true
}
