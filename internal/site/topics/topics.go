// Package topics holds the topic pages of LebaneseLegislation: short plain
// explanations of what Lebanese law says on one question, every statement
// tied to an article of the database and the article's own words quoted.
// The pages are data files (one JSON file per topic, embedded); the site
// renders them and the checks in check.go keep them honest.
package topics

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed *.json
var files embed.FS

// Cite is one article a topic relies on, with the words quoted from it. The
// quote is Arabic, as printed, and must be an exact piece of the article's
// current text. Article may instead name a reading of the Official Gazette's
// pages, an annexed table's row or an article's first wording (reading.go).
type Cite struct {
	ID      string `json:"id"`
	Law     int    `json:"law"`
	Article string `json:"article"`
	Quote   string `json:"quote"`
}

// Para is a paragraph. Cites lists the Cite ids that carry its legal
// statements. A paragraph with no cite must say Note (framing, a pointer or
// a limit of the page, never a statement of what the law provides).
type Para struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites,omitempty"`
	Note  bool     `json:"note,omitempty"`
}

// Row is a table row; Cites tie its figures to articles.
type Row struct {
	Cells []string `json:"cells"`
	Cites []string `json:"cites"`
}

// Table is a small reference table.
type Table struct {
	Caption string   `json:"caption"`
	Head    []string `json:"head"`
	Rows    []Row    `json:"rows"`
}

// Section answers one sub-question.
type Section struct {
	Heading string `json:"heading"`
	Paras   []Para `json:"paras"`
	Table   *Table `json:"table,omitempty"`
}

// Content is a topic in one language.
type Content struct {
	Title       string    `json:"title"`
	Question    string    `json:"question"`
	Description string    `json:"description"`
	Answer      Para      `json:"answer"`
	Sections    []Section `json:"sections"`
}

// Topic is one page, in both languages. The articles are shared: the
// quotes stay in Arabic on either page.
type Topic struct {
	Slug      string  `json:"slug"`
	Order     int     `json:"order"`
	Published string  `json:"published"` // YYYY-MM-DD
	Updated   string  `json:"updated"`   // YYYY-MM-DD, "last checked"
	Articles  []Cite  `json:"articles"`
	EN        Content `json:"en"`
	AR        Content `json:"ar"`
}

// Lang returns the content for "en" or "ar".
func (t *Topic) Lang(lang string) *Content {
	if lang == "ar" {
		return &t.AR
	}
	return &t.EN
}

// Cite returns the cite with that id.
func (t *Topic) Cite(id string) *Cite {
	for i := range t.Articles {
		if t.Articles[i].ID == id {
			return &t.Articles[i]
		}
	}
	return nil
}

var (
	once   sync.Once
	loaded []*Topic
	loadEr error
)

// Load parses every embedded topic, ordered by Order then slug.
func Load() ([]*Topic, error) {
	once.Do(func() {
		names, err := files.ReadDir(".")
		if err != nil {
			loadEr = err
			return
		}
		for _, n := range names {
			if !strings.HasSuffix(n.Name(), ".json") || strings.HasPrefix(n.Name(), "_") {
				continue
			}
			b, err := files.ReadFile(n.Name())
			if err != nil {
				loadEr = err
				return
			}
			t := &Topic{}
			if err := json.Unmarshal(b, t); err != nil {
				loadEr = fmt.Errorf("%s: %w", n.Name(), err)
				return
			}
			if t.Slug+".json" != n.Name() {
				loadEr = fmt.Errorf("%s: slug %q does not match the file name", n.Name(), t.Slug)
				return
			}
			loaded = append(loaded, t)
		}
		sort.Slice(loaded, func(i, j int) bool {
			if loaded[i].Order != loaded[j].Order {
				return loaded[i].Order < loaded[j].Order
			}
			return loaded[i].Slug < loaded[j].Slug
		})
	})
	return loaded, loadEr
}

// All is Load without the error: a topic file that does not parse is caught
// by the tests, and the site serves the ones that do.
func All() []*Topic {
	ts, _ := Load()
	return ts
}

// Get returns the topic with that slug, or nil.
func Get(slug string) *Topic {
	for _, t := range All() {
		if t.Slug == slug {
			return t
		}
	}
	return nil
}

// ByArticle lists the topics that quote an article, its own text or its
// wording as first published ("original-<number>", reading.go).
func ByArticle(law int, number string) []*Topic {
	var out []*Topic
	for _, t := range All() {
		for _, c := range t.Articles {
			if c.Law == law && (c.Article == number || c.Article == "original-"+number) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
