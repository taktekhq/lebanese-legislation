// Command searchbench compares natural-language retrieval with ordinary search.
// Use only public, non-personal fixtures when an API project shares traffic.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/site"
)

type fixture struct {
	Query string `json:"query"`
	Want  []int  `json:"want"`
	Kind  string `json:"kind"`
}
type record struct {
	Query        string   `json:"query"`
	Kind         string   `json:"kind"`
	Queries      []string `json:"queries,omitempty"`
	BaselineHit  bool     `json:"baseline_hit"`
	Hit          bool     `json:"hit"`
	Milliseconds int64    `json:"milliseconds"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	Error        string   `json:"error,omitempty"`
	LawIDs       []int    `json:"law_ids"`
	ArticleIDs   []string `json:"article_ids"`
	Titles       []string `json:"titles"`
}
type captured struct {
	c *nlsearch.Client
	p nlsearch.Plan
}

func (c *captured) Rewrite(ctx context.Context, q string) (nlsearch.Plan, error) {
	p, e := c.c.Rewrite(ctx, q)
	c.p = p
	return p, e
}
func hit(r site.Results, want []int) bool {
	if len(want) == 0 {
		return len(r.Laws)+len(r.Articles)+len(r.Rulings) == 0
	}
	for _, id := range want {
		for _, l := range r.Laws[:min(5, len(r.Laws))] {
			if l.ID == id {
				return true
			}
		}
		for _, a := range r.Articles[:min(5, len(r.Articles))] {
			if a.ID == id {
				return true
			}
		}
	}
	return false
}

func main() {
	fixtures := flag.String("fixtures", "testdata/searchbench/public.json", "public test cases with expected parent law IDs")
	provider := flag.String("provider", "openai", "openai, gemini or claude")
	model := flag.String("model", "", "API model ID")
	flag.Parse()
	raw, err := os.ReadFile(*fixtures)
	if err != nil {
		panic(err)
	}
	var cases []fixture
	if err = json.Unmarshal(raw, &cases); err != nil {
		panic(err)
	}
	keyName := map[string]string{"openai": "OPENAI_API_KEY", "gemini": "GEMINI_API_KEY", "claude": "ANTHROPIC_API_KEY"}[*provider]
	c, err := nlsearch.New(nlsearch.Config{Provider: *provider, Model: *model, Key: os.Getenv(keyName)})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, os.Getenv("LEBLEG_DSN"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	s, err := site.New(db)
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	for _, f := range cases {
		base, err := s.SearchDocuments(ctx, f.Query)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline database search failed")
			os.Exit(1)
		}
		rw := &captured{c: c}
		start := time.Now()
		res, err := s.SearchMeaning(ctx, f.Query, rw)
		r := record{Query: f.Query, Kind: f.Kind, Queries: rw.p.Queries, BaselineHit: hit(base, f.Want), Hit: hit(res, f.Want), Milliseconds: time.Since(start).Milliseconds(), InputTokens: rw.p.InputTokens, OutputTokens: rw.p.OutputTokens}
		if err != nil {
			r.Error = err.Error()
			r.Hit = false
		}
		for _, l := range res.Laws[:min(5, len(res.Laws))] {
			r.LawIDs = append(r.LawIDs, l.ID)
			r.Titles = append(r.Titles, l.Title)
		}
		for _, a := range res.Articles[:min(5, len(res.Articles))] {
			r.ArticleIDs = append(r.ArticleIDs, fmt.Sprintf("%d/%s", a.ID, a.Article))
		}
		if err := enc.Encode(r); err != nil {
			panic(err)
		}
		// API errors are recorded independently; do not hide partial comparisons.
		if strings.Contains(r.Error, "HTTP 401") {
			os.Exit(2)
		}
	}
}
