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

type Cite struct {
	ID      string `json:"id"`
	Law     int    `json:"law"`
	Article string `json:"article"`
	Quote   string `json:"quote"`
}

type Para struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites,omitempty"`
	Note  bool     `json:"note,omitempty"`
}

type Row struct {
	Cells []string `json:"cells"`
	Cites []string `json:"cites"`
}

type Table struct {
	Caption string   `json:"caption"`
	Head    []string `json:"head"`
	Rows    []Row    `json:"rows"`
}

type Section struct {
	Heading string `json:"heading"`
	Paras   []Para `json:"paras"`
	Table   *Table `json:"table,omitempty"`
}

type Content struct {
	Title       string    `json:"title"`
	Question    string    `json:"question"`
	Description string    `json:"description"`
	Answer      Para      `json:"answer"`
	Sections    []Section `json:"sections"`
}

type Topic struct {
	Slug      string  `json:"slug"`
	Order     int     `json:"order"`
	Published string  `json:"published"`
	Updated   string  `json:"updated"`
	Articles  []Cite  `json:"articles"`
	EN        Content `json:"en"`
	AR        Content `json:"ar"`
}

func (t *Topic) Lang(lang string) *Content {
	if lang == "ar" {
		return &t.AR
	}
	return &t.EN
}

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

func All() []*Topic {
	ts, _ := Load()
	return ts
}

func Get(slug string) *Topic {
	for _, t := range All() {
		if t.Slug == slug {
			return t
		}
	}
	return nil
}

func ByArticle(law int, number string) []*Topic {
	var out []*Topic
	for _, t := range All() {
		for _, c := range t.Articles {
			if c.Law == law && c.Article == number {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
