package amend

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseNameAliases reads db/aliases.tsv: name, law id, note, tab separated;
// a first line "name\tlaw_id\tnote" and lines starting with # are skipped.
// The result is keyed by the folded title key, so spellings of a name that
// fold alike are one.
func ParseNameAliases(tsv string) (map[string]int, error) {
	out := map[string]int{}
	for i, line := range strings.Split(tsv, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "name\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 || strings.TrimSpace(f[0]) == "" || strings.TrimSpace(f[2]) == "" {
			return nil, fmt.Errorf("aliases.tsv line %d: want name, law id and a note", i+1)
		}
		id, err := strconv.Atoi(strings.TrimSpace(f[1]))
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("aliases.tsv line %d: %q is not a law id", i+1, f[1])
		}
		k := titleKey(f[0])
		if prev, ok := out[k]; ok && prev != id {
			return nil, fmt.Errorf("aliases.tsv line %d: %q is already %d", i+1, strings.TrimSpace(f[0]), prev)
		}
		out[k] = id
	}
	return out, nil
}

// SetNameAliases tells the index the names texts are cited by (see
// ParseNameAliases). It returns the ids it does not hold.
func (ix *Index) SetNameAliases(m map[string]int) (missing []int) {
	ix.nameAlias = m
	for _, id := range m {
		if ix.laws[id] == nil {
			missing = append(missing, id)
		}
	}
	return missing
}
