package site

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/semantic"
)

type vectorFake struct {
	hits  []semantic.Hit
	err   error
	calls int
}

func (f *vectorFake) Search(_ context.Context, _ string, _ int) ([]semantic.Hit, error) {
	f.calls++
	return f.hits, f.err
}

func TestHybridPreservesBaselineAndRulings(t *testing.T) {
	s := testServer(t)
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	r, e := s.SearchMeaning(t.Context(), "سرقة يد عارضة", f)
	if e != nil || len(r.Rulings) == 0 {
		t.Fatalf("baseline ruling discarded: %v", e)
	}
	ps, e := s.answerSources(t.Context(), "سرقة يد عارضة", r)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range ps {
		if strings.HasPrefix(p.Path, "/ruling/") {
			found = true
		}
	}
	if !found {
		t.Fatal("relevant ruling text missing from evidence")
	}
}
func TestArticleReferenceUsesSamePathAsBrowser(t *testing.T) {
	s := testServer(t)
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	q := "المادة 47 من قانون تنظيم الملكية المشتركة"
	r, e := s.SearchMeaning(t.Context(), q, f)
	if e != nil || len(r.Articles) != 1 || r.Articles[0].ID != 189342 || r.Articles[0].Article != "47" || f.calls != 0 {
		t.Fatalf("article lookup differs from browser: %+v err=%v", r, e)
	}
}
func TestHybridRejectsStaleVectorsAndBypassesCitations(t *testing.T) {
	s := testServer(t)
	s.SetMeaningSearch(&evidenceFake{}, "Gemini", false, 20)
	doc := semantic.Document{Kind: "article", ID: 190374, Part: "39", Lang: "ar"}
	canonical, e := semantic.Load(t.Context(), s.db, doc)
	if e != nil {
		t.Fatal(e)
	}
	body := []rune(canonical.Text)
	n := min(300, len(body))
	h := semantic.Hit{Document: doc, Start: 0, End: n, Hash: semantic.Hash(canonical.Text)}
	h.Text = string(body[:n])
	f := &vectorFake{hits: []semantic.Hit{h}}
	s.SetVectorSearch(f)
	ps, e := s.vectorSources(t.Context(), "How much leave do employees get?")
	if e != nil || len(ps) != 1 || !strings.Contains(ps[0].Path, "/39/") {
		t.Fatalf("no canonical evidence: %d %v", len(ps), e)
	}
	f.hits[0].Hash = "changed"
	s.meaning.vectors = nil
	ps, e = s.vectorSources(t.Context(), "How much leave do employees get?")
	if e != nil || len(ps) != 0 {
		t.Fatal("stale vector trusted")
	}
	before := f.calls
	ps, e = s.vectorSources(t.Context(), "القرار رقم 513 تاريخ 11/7/2005")
	if e != nil || len(ps) != 0 || f.calls != before {
		t.Fatal("missing citation broadened")
	}
	if ps, e = s.vectorSources(t.Context(), "قانون العمل"); e != nil || len(ps) != 0 || f.calls != before {
		t.Fatal("exact title broadened")
	}
	f.err = errors.New("vector database down")
	if _, e = s.vectorSources(t.Context(), "other question"); e == nil {
		t.Fatal("outage not surfaced for fallback")
	}
}
func TestHybridVectorBudgetAndCache(t *testing.T) {
	s := testServer(t)
	s.SetMeaningSearch(&evidenceFake{}, "Gemini", false, 1)
	f := &vectorFake{}
	s.SetVectorSearch(f)
	for range 2 {
		if _, e := s.vectorSources(t.Context(), "same"); e != nil {
			t.Fatal(e)
		}
	}
	if f.calls != 1 {
		t.Fatal("query embedding not cached")
	}
	if _, e := s.vectorSources(t.Context(), "different"); e == nil || f.calls != 1 {
		t.Fatal("embedding bypasses daily limit")
	}
}

func TestRulingCitationRequiresItsFullDate(t *testing.T) {
	s := testServer(t)
	r, e := s.SearchDocuments(t.Context(), "تمييز جزائي قرار رقم 33 تاريخ 19/1/1993")
	if e != nil || r.Missing == "" || len(r.Rulings) != 0 {
		t.Fatalf("different-day ruling substituted: %+v %v", r, e)
	}
	r, e = s.SearchDocuments(t.Context(), "تمييز جزائي قرار رقم 33 تاريخ 2/3/1993")
	if e != nil || len(r.Rulings) == 0 || r.Rulings[0].ID != 66808 {
		t.Fatalf("matching date absent: %+v %v", r, e)
	}
}
