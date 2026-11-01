package judgecheck

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// ValidateItems keeps only judge inputs whose citation resolves to the exact
// amended text and article. Missing or mismatched citations become data gaps.
func ValidateItems(items []judge.Item, citations map[string]cite.Citation, resolve func(int, cite.Citation) []cite.Link) ([]judge.Item, []string) {
	var valid []judge.Item
	var gaps []string
	for _, it := range items {
		byLaw, byArticle, _, article, ok := itemKey(it.Key)
		if !ok {
			gaps = append(gaps, fmt.Sprintf("%s: invalid judge key", it.Key))
			continue
		}
		c, found := citations[cite.Key(byLaw, byArticle)]
		if !found {
			gaps = append(gaps, fmt.Sprintf("%s: no citation record for amending article", it.Key))
			continue
		}
		var matches []cite.Link
		for _, link := range resolve(byLaw, c) {
			if cite.Changing(link.Action) && link.Target.Article == article {
				matches = append(matches, link)
			}
		}
		if len(matches) != 1 {
			gaps = append(gaps, fmt.Sprintf("%s: citation does not resolve uniquely to article %s", it.Key, article))
			continue
		}
		link := matches[0]
		if !link.Valid || link.LawID == 0 {
			gaps = append(gaps, fmt.Sprintf("%s: citation target for article %s is unresolved or invalid", it.Key, article))
			continue
		}
		if it.LawID <= 0 || link.LawID != it.LawID {
			gaps = append(gaps, fmt.Sprintf("%s: citation resolves to text %d, retrieved article belongs to text %d", it.Key, link.LawID, it.LawID))
			continue
		}
		if !link.HasArt {
			gaps = append(gaps, fmt.Sprintf("%s: resolved text %d does not contain article %s", it.Key, link.LawID, article))
			continue
		}
		if link.Target.Part != it.Part {
			gaps = append(gaps, fmt.Sprintf("%s: citation part %q does not match judge part %q", it.Key, link.Target.Part, it.Part))
			continue
		}
		if strings.TrimSpace(it.Centre) == "" || strings.TrimSpace(it.Amending) == "" {
			gaps = append(gaps, fmt.Sprintf("%s: retrieved article or amending provision has no text", it.Key))
			continue
		}
		valid = append(valid, it)
	}
	return valid, gaps
}

func itemKey(key string) (int, string, int, string, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		return 0, "", 0, "", false
	}
	law, err := strconv.Atoi(parts[0])
	if err != nil || law <= 0 || parts[1] == "" || parts[3] == "" {
		return 0, "", 0, "", false
	}
	seq, err := strconv.Atoi(parts[2])
	if err != nil || seq <= 0 {
		return 0, "", 0, "", false
	}
	return law, parts[1], seq, parts[3], true
}

// ResolveItems pairs citation changes with the parser's law and article, then
// applies the same resolver used by citation reports.
func ResolveItems(ix *amend.Index, items []judge.Item, citations map[string]cite.Citation) ([]judge.Item, []string) {
	return ValidateItems(items, citations, func(byLaw int, c cite.Citation) []cite.Link {
		return cite.Resolve(ix, byLaw, c)
	})
}

// CitationIndex creates the resolver index used by the citation reader.
func CitationIndex(ctx context.Context, db *pgxpool.Pool) (*amend.Index, error) {
	return cite.LawIndex(ctx, db)
}
