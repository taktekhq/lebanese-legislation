// Package datatables reads data/tables: one folder per table, PostgreSQL COPY
// text files (*.tsv) and a COLUMNS file naming the fields. It only reads, and
// it reads no model output, so a tool that talks to a model (cmd/translate-en)
// can take articles from here without naming a law-text table itself
// (internal/provenance).
package datatables

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Article is one row of legislation_article, with the SHA-256 of its text_ar.
type Article struct {
	Law, Position int
	Number        string
	Text          string // text_ar, trimmed
	Hash          string // hex SHA-256 of text_ar as stored
}

// Each calls fn with the fields of every row of the table folder dir, in file
// order. col maps a column name to its field index.
func Each(dir string, fn func(col map[string]int, f []string) error) error {
	cols, err := os.ReadFile(filepath.Join(dir, "COLUMNS"))
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, c := range strings.Split(strings.TrimSpace(string(cols)), ",") {
		col[c] = i
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, name := range files {
		fh, err := os.Open(name)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			for i := range f {
				f[i] = Unescape(f[i])
			}
			if err := fn(col, f); err != nil {
				fh.Close()
				return err
			}
		}
		fh.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	return nil
}

var copyEsc = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\t`, "\t", `\r`, "\r")

// Unescape turns a COPY text field into its value; \N (NULL) becomes "".
func Unescape(s string) string {
	if s == `\N` {
		return ""
	}
	return copyEsc.Replace(s)
}

// Titles returns the titles of the texts in ids, from data/tables/legislation.
func Titles(data string, ids map[int]bool) (map[int]string, error) {
	out := map[int]string{}
	err := Each(filepath.Join(data, "tables", "legislation"), func(col map[string]int, f []string) error {
		id, _ := strconv.Atoi(f[col["id"]])
		if ids[id] {
			out[id] = f[col["title"]]
		}
		return nil
	})
	return out, err
}

// Articles returns the articles of the texts in ids that have Arabic text,
// ordered by text and position, from data/tables/legislation_article.
func Articles(data string, ids map[int]bool) ([]Article, error) {
	var out []Article
	err := Each(filepath.Join(data, "tables", "legislation_article"), func(col map[string]int, f []string) error {
		law, _ := strconv.Atoi(f[col["law_id"]])
		if !ids[law] {
			return nil
		}
		text := strings.TrimSpace(f[col["text_ar"]])
		if text == "" {
			return nil
		}
		pos, _ := strconv.Atoi(f[col["position"]])
		sum := sha256.Sum256([]byte(f[col["text_ar"]]))
		out = append(out, Article{Law: law, Position: pos, Number: f[col["number"]], Text: text, Hash: hex.EncodeToString(sum[:])})
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Law != out[j].Law {
			return out[i].Law < out[j].Law
		}
		return out[i].Position < out[j].Position
	})
	return out, err
}
