package site

import (
	"context"
	"errors"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

type fakeRewrite struct {
	calls   int
	err     error
	queries []string
}

func (f *fakeRewrite) Rewrite(context.Context, string) (nlsearch.Plan, error) {
	f.calls++
	return nlsearch.Plan{Queries: f.queries}, f.err
}

func TestMeaningPreservesMissingCitation(t *testing.T) {
	s := testServer(t)
	f := &fakeRewrite{queries: []string{"قانون العمل"}}
	r, err := s.SearchMeaning(t.Context(), "القرار رقم 513 تاريخ 11/7/2005", f)
	if err != nil || r.Missing == "" || f.calls != 0 {
		t.Fatalf("missing=%q calls=%d err=%v", r.Missing, f.calls, err)
	}
}

func TestMeaningRetrievalAndFailure(t *testing.T) {
	s := testServer(t)
	f := &fakeRewrite{queries: []string{"قانون العمل", "قانون العمل"}}
	r, err := s.SearchMeaning(t.Context(), "Can my employer fire me?", f)
	if err != nil || len(r.Laws) == 0 || r.Laws[0].ID != 190374 || r.ViaKind != "meaning" {
		t.Fatalf("%+v %v", r, err)
	}
	for i, l := range r.Laws {
		for _, m := range r.Laws[:i] {
			if l.ID == m.ID {
				t.Error("duplicate law")
			}
		}
	}
	f.err = errors.New("provider unavailable")
	r, err = s.SearchMeaning(t.Context(), "labor rights", f)
	if err == nil || len(r.Laws) == 0 {
		t.Fatalf("fallback missing: %v %v", r, err)
	}
}
