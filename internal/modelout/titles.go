package modelout

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
)

// TitlesEn loads db/titles_en.tsv into legislation_title_en: an unofficial
// English rendering of each text's subject line (law id, title, model). A
// row for a text not in laws, an empty title, or a title that still holds
// Arabic letters is noted and left out; em and en dashes become plain
// hyphens, the house style.
func TitlesEn(ctx context.Context, conn *pgx.Conn, laws map[int]bool, note func(string, ...any)) error {
	cols := []string{"law_id", "title_en", "model"}
	var rows [][]any
	seen := map[int]bool{}
	for i, ln := range strings.Split(db.TitlesEn, "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		id, err := strconv.Atoi(f[0])
		if len(f) != 3 || err != nil {
			note("titles_en.tsv line %d unreadable", i+1)
			continue
		}
		title := cleanTitleEn(f[1])
		if title == "" || seen[id] || !laws[id] {
			note("titles_en.tsv line %d (text %d) skipped: empty, repeated or no such text", i+1, id)
			continue
		}
		seen[id] = true
		rows = append(rows, []any{id, title, strings.TrimSpace(f[2])})
	}
	return copyRows(ctx, conn, "legislation_title_en", cols, rows)
}

// cleanTitleEn tidies a model's English title: dashes in the house style,
// one line, no Arabic letters. It returns "" for a title it will not show.
func cleanTitleEn(s string) string {
	s = strings.NewReplacer("—", " - ", "–", "-", "\t", " ", "\n", " ", "\r", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	for _, c := range s {
		if unicode.Is(unicode.Arabic, c) {
			return ""
		}
	}
	return s
}
