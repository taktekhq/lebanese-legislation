package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/site/topics"
)

// Spec is the research behind one topic: the question, the sub-questions
// and the articles a writer may use. specs/*.json.
type Spec struct {
	Slug        string     `json:"slug"`
	TitleEN     string     `json:"title_en"`
	TitleAR     string     `json:"title_ar"`
	QuestionEN  string     `json:"question_en"`
	QuestionAR  string     `json:"question_ar"`
	SubQuestion []string   `json:"sub_questions"`
	Notes       string     `json:"notes"`
	Pack        []PackItem `json:"pack"`
}

// PackItem is some articles of one law and why they are in the pack.
type PackItem struct {
	Law      int      `json:"law"`
	Articles []string `json:"articles"`
	Why      string   `json:"why"`
}

func loadSpec(dir, slug string) (*Spec, error) {
	b, err := os.ReadFile(filepath.Join(dir, slug+".json"))
	if err != nil {
		return nil, err
	}
	s := &Spec{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s: %w", slug, err)
	}
	return s, nil
}

func specSlugs(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []string
	for _, f := range m {
		n := strings.TrimSuffix(filepath.Base(f), ".json")
		if !strings.HasPrefix(n, "_") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Article is one article of the pack as the writer reads it.
type Article struct {
	Law    int
	Number string
	Cite   string // the law as cited, in Arabic
	Ident  string // its number and date, as the database holds them
	Text   string
}

// loadPack reads the pack's articles in the wording the site shows.
func loadPack(ctx context.Context, db topics.DB, s *Spec) ([]Article, error) {
	var out []Article
	seen := map[string]bool{}
	for _, it := range s.Pack {
		for _, n := range it.Articles {
			k := fmt.Sprintf("%d/%s", it.Law, n)
			if seen[k] {
				continue
			}
			seen[k] = true
			f, ok, err := db.Article(it.Law, n)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("%s: article %s of law %d does not exist", s.Slug, n, it.Law)
			}
			var cite, num string
			var issued *time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT title, coalesce(number, ''), issued_on FROM legislation WHERE id = $1`, it.Law).Scan(&cite, &num, &issued); err != nil {
				return nil, err
			}
			ident := ""
			if num != "" {
				ident = "no. " + num
			}
			if issued != nil {
				ident += ", dated " + issued.Format("2/1/2006")
			}
			out = append(out, Article{Law: it.Law, Number: n, Cite: cite, Ident: strings.TrimPrefix(ident, ", "), Text: f.Text})
		}
	}
	return out, nil
}
