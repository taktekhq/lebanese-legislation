package staticexport

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The live site moves an old address in one hop (internal/site/redirect.go):
// /ar/... to the root, a page address to its slashed form, a text named with
// the wrong word (/law/ for a decree) to its own word, and the address of a
// scanned text the Centre has since published to the Centre's text. A static
// edge needs the same answers without a database, so this file carries the
// data those rules read and a reference implementation of the rules, tested
// against the live handler. The article-spelling rule ("٣", "12/1") reads the
// articles of a text and stays with the origin: a request no rule and no file
// answers goes to the origin, which answers it as it does today.

// RedirectData is what an edge needs to apply the moves.
type RedirectData struct {
	// Decree and Text list the ids of texts whose address word is not "law",
	// as flattened ranges: [start, count, start, count, ...].
	Decree []int `json:"decree"`
	Text   []int `json:"text"`
	// Aliases map the id of an old scanned text to the text that replaced it.
	Aliases [][2]int `json:"aliases"`

	words map[int]string
	alias map[int]int
}

// Querier is the part of a Postgres pool the exporter reads aliases through.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// BuildRedirectData derives the address words from the exported manifest (so
// they are the words the live handler answered with, not a second copy of
// its rule) and reads the aliases from the database; db may be nil.
func BuildRedirectData(ctx context.Context, m map[string]Entry, db Querier) (*RedirectData, error) {
	var dec, txt []int
	for p, e := range m {
		if e.Status != 200 {
			continue
		}
		word, id, rest, ok := textAddress(p)
		if !ok || rest != "" {
			continue
		}
		switch word {
		case "decree":
			dec = append(dec, id)
		case "text":
			txt = append(txt, id)
		}
	}
	d := &RedirectData{Decree: ranges(dec), Text: ranges(txt)}
	if db != nil {
		rows, err := db.Query(ctx, `SELECT a.id, a.law_id FROM legislation_alias a
			WHERE NOT EXISTS (SELECT 1 FROM legislation l WHERE l.id = a.id) ORDER BY a.id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var a [2]int
			if err := rows.Scan(&a[0], &a[1]); err != nil {
				return nil, err
			}
			d.Aliases = append(d.Aliases, a)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	d.index()
	return d, nil
}

// WriteRedirectData saves d as JSON.
func WriteRedirectData(file string, d *RedirectData) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// ReadRedirectData loads what WriteRedirectData saved.
func ReadRedirectData(file string) (*RedirectData, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var d RedirectData
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	d.index()
	return &d, nil
}

func (d *RedirectData) index() {
	d.words = map[int]string{}
	for _, r := range []struct {
		word string
		list []int
	}{{"decree", d.Decree}, {"text", d.Text}} {
		for i := 0; i+1 < len(r.list); i += 2 {
			for id := r.list[i]; id < r.list[i]+r.list[i+1]; id++ {
				d.words[id] = r.word
			}
		}
	}
	d.alias = map[int]int{}
	for _, a := range d.Aliases {
		d.alias[a[0]] = a[1]
	}
}

// Word is the address word of text id.
func (d *RedirectData) Word(id int) string {
	if w, ok := d.words[id]; ok {
		return w
	}
	return "law"
}

// ranges flattens a set of ids into [start, count, ...].
func ranges(ids []int) []int {
	sort.Ints(ids)
	var out []int
	for _, id := range ids {
		if n := len(out); n > 0 && out[n-2]+out[n-1] == id {
			out[n-1]++
		} else if n > 0 && out[n-2]+out[n-1] > id {
			continue // a duplicate
		} else {
			out = append(out, id, 1)
		}
	}
	return out
}

var addressWords = []string{"law", "decree", "text"}

// textAddress splits /{word}/{id}/rest, as the live handler does.
func textAddress(p string) (word string, id int, rest string, ok bool) {
	parts := strings.SplitN(p, "/", 4) // "", word, id, rest
	if len(parts) < 4 {
		return "", 0, "", false
	}
	for _, w := range addressWords {
		if parts[1] == w {
			if id, err := strconv.Atoi(parts[2]); err == nil && id > 0 {
				return w, id, parts[3], true
			}
		}
	}
	return "", 0, "", false
}

// Resolve applies the live site's moves to an escaped path (no query) until
// none changes it, and returns where it ends up. exists says whether the
// edge holds a page at a path; it decides the trailing-slash move, which the
// live site makes only where its router has a slashed page. Of the article
// spelling moves only Arabic-Indic digits are here (see the file comment).
func (d *RedirectData) Resolve(p string, exists func(string) bool) string {
	for range 10 {
		moved := false
		// /ar/... is the same path at the root
		if p == "/ar" || strings.HasPrefix(p, "/ar/") {
			p, moved = "/"+strings.TrimLeft(p[len("/ar"):], "/"), true
		}
		// a page address without its final slash
		if !strings.HasSuffix(p, "/") && !strings.Contains(p[strings.LastIndex(p, "/"):], ".") && exists(p+"/") {
			p, moved = p+"/", true
		}
		// a text named with the wrong word
		if word, id, rest, ok := textAddress(p); ok {
			if want := d.Word(id); want != word {
				p, moved = "/"+want+"/"+strconv.Itoa(id)+"/"+rest, true
			}
			// an old scanned text the Centre has published
			if to, ok := d.alias[id]; ok {
				if strings.HasPrefix(rest, "article/") {
					p, moved = "/"+d.Word(to)+"/"+strconv.Itoa(to)+"/"+rest, true
				} else {
					p, moved = "/"+d.Word(to)+"/"+strconv.Itoa(to)+"/", true
				}
			}
		}
		// an article number written with Arabic-Indic digits ("٤٢" for 42), when
		// the ASCII spelling is a page (the other spellings stay with the origin)
		if word, id, rest, ok := textAddress(p); ok && strings.HasPrefix(rest, "article/") {
			if seg, tail, found := strings.Cut(rest[len("article/"):], "/"); found {
				if dec, err := url.PathUnescape(seg); err == nil && hasIndic(dec) {
					cand := "/" + word + "/" + strconv.Itoa(id) + "/article/" + url.PathEscape(asciiDigits(dec)) + "/" + tail
					if exists(cand) {
						p, moved = cand, true
					}
				}
			}
		}
		if !moved {
			break
		}
	}
	return p
}

func hasIndic(s string) bool {
	for _, r := range s {
		if r >= '٠' && r <= '٩' {
			return true
		}
	}
	return false
}

// asciiDigits writes Arabic-Indic digits as ASCII digits.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '٠' && r <= '٩' {
			return '0' + (r - '٠')
		}
		return r
	}, s)
}
