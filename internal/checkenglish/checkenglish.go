// Package checkenglish checks that English stays an unofficial aid. It fails when a surface prints title_en
// without the "unofficial" flag, a text added in the last 24 hours has no
// English title, an article body is mostly Latin letters, or a title_en row has
// no model.
package checkenglish

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Surfaces are the files that print title_en, relative to the repo root.
var Surfaces = []string{
	"internal/site/templates/*.html",
	"internal/site/*.go",
	"internal/snapshot/*.go",
	"internal/export/*.go",
	"mcp/cloudflare/*.mjs",
	"tools/src/lebleg/*.py",
}

// prints matches a file that shows or exports a title_en field (not one that
// only names the table in a list).
var prints = regexp.MustCompile(`\.TitleEn\b|json:"title_en|SELECT[^;]*\btitle_en\b|\btitle_en\s*:|\bAS title_en\b`)

// flag matches the unofficial flag: the word, or the i18n keys that carry it.
var flag = regexp.MustCompile(`(?i)unofficial|title_en_label|title_en_note`)

// Surface checks the files under root: each one that prints title_en must carry
// the flag. It returns one violation per file without it.
func Surface(root string) ([]string, error) {
	var out []string
	for _, g := range Surfaces {
		files, err := filepath.Glob(filepath.Join(root, g))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if prints.Match(b) && !flag.Match(b) {
				rel, _ := filepath.Rel(root, f)
				out = append(out, fmt.Sprintf("%s prints title_en without the unofficial flag", rel))
			}
		}
	}
	return out, nil
}

// Latin reports whether s is mostly Latin letters: more than half of its
// letters are Latin.
func Latin(s string) bool {
	var lat, all int
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		all++
		if unicode.Is(unicode.Latin, r) {
			lat++
		}
	}
	return all > 0 && lat*2 > all
}

// EnglishLines returns the article's lines that are mostly Latin letters.
func EnglishLines(text string) []string {
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		if Latin(ln) {
			out = append(out, strings.TrimSpace(ln))
		}
	}
	return out
}

// Data checks the database: a text first seen within 24 hours has a title_en,
// every title_en row has a model, and no article body holds an English line.
// allow lists "law_id/number" article ids exempt from the body rule.
func Data(ctx context.Context, conn *pgx.Conn, allow map[string]bool) ([]string, error) {
	var out []string
	rows, err := conn.Query(ctx, `SELECT l.id FROM legislation l
		JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
		LEFT JOIN legislation_title_en te ON te.law_id = l.id
		WHERE sp.first_seen > now() - interval '24 hours' AND (te.law_id IS NULL OR te.title_en = '')
		ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("text %d was added in the last 24 hours and has no title_en", id))
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	rows, err = conn.Query(ctx, `SELECT law_id FROM legislation_title_en WHERE btrim(model) = '' ORDER BY law_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("title_en of text %d has no model", id))
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	rows, err = conn.Query(ctx, `SELECT law_id, number, text_ar FROM legislation_article ORDER BY law_id, position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var num, text string
		if err := rows.Scan(&id, &num, &text); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%d/%s", id, num)
		if allow[key] {
			continue
		}
		if ls := EnglishLines(text); len(ls) > 0 {
			out = append(out, fmt.Sprintf("article %s holds English: %q", key, trunc(ls[0])))
		}
	}
	return out, rows.Err()
}

func trunc(s string) string {
	r := []rune(s)
	if len(r) > 60 {
		return string(r[:60]) + "..."
	}
	return s
}
