package indexing

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Weak pages (issue #86, decided 2 Nov 2026): pages kept for visitors but not
// offered to search engines, so Google judges the site by its strong pages.
// They are `noindex, follow` (their links still lead to the laws) and out of
// the sitemaps.
//
//   - Machine-translated English (kind english, ReasonMachineEnglish): every
//     text not in data/popular_texts.txt has one row (id the text, position
//     0) that covers all of its /en/ pages: the text, its parts and its
//     articles. The Arabic pages are not affected.
//   - Pages hidden whole, with no rows: the generated topic pages
//     (/topics/, /topic/<slug>/; files in internal/site/topics) and every
//     court ruling page and list of rulings (rulings name people). The site
//     marks them itself (internal/site/indexing.go, topics.go).
const ReasonMachineEnglish = "machine_english"

// ReadPopular reads a list of text ids, one per line, # for comments
// (data/popular_texts.txt). A line that is not an id is an error, so a broken
// refresh fails the build instead of hiding every English page.
func ReadPopular(path string) ([]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePopular(string(raw), path)
}

// ParsePopular is ReadPopular on the file's contents; name is for errors.
func ParsePopular(s, name string) ([]int, error) {
	var ids []int
	seen := map[int]bool{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := strconv.Atoi(line)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s line %d: not a text id: %q", name, i+1, line)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s: no text ids", name)
	}
	return ids, nil
}
