// Package patternonly is a fixture: it names model output only in a regular
// expression's pattern (as the guard's own scanner does) and names a text
// table to read it (as the export does). It reads no model output, so
// ScanWriters leaves it alone.
package patternonly

import "regexp"

var modelPath = regexp.MustCompile(`db/model/|\.read\.json\b`)

// Rows counts a text table's rows, and the files that look like model output.
func Rows(read func(table string) []string, files []string) (n int) {
	for _, f := range files {
		if modelPath.MatchString(f) {
			n++
		}
	}
	return n + len(read("legislation_article"))
}
