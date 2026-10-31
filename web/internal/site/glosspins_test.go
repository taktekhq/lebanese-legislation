package site

import (
	"net/url"
	"testing"
)

var cyberQueries = []string{
	"cybercrime", "cyber crime", "cybercrimes", "Cyber-Crime", "cyber crimes law", "information crimes",
	"Information crime", "computer crimes", "Lebanon computer crimes",
	"الجرائم المعلوماتية", "الجرائم الالكترونية", "الجرائم الإلكترونية", "جرائم المعلوماتية", "جرائم الكترونية", "قانون الجرائم المعلوماتية",
}

func TestPinnedLaw(t *testing.T) {
	for _, q := range cyberQueries {
		if got := pinnedLaw(q); got != lawEtransactions {
			t.Errorf("pinnedLaw(%q) = %d, want %d", q, got, lawEtransactions)
		}
	}
	for _, q := range []string{"", "crime", "cyber", "computer", "الجرائم", "قانون العقوبات", "penal code", "cybercrime bureau grant"} {
		if got := pinnedLaw(q); got != 0 {
			t.Errorf("pinnedLaw(%q) = %d, want 0", q, got)
		}
	}
}

func TestWithPin(t *testing.T) {
	a, b, p := LawRow{ID: 1}, LawRow{ID: 2}, LawRow{ID: 9}
	got, added := withPin([]LawRow{a, b}, &p, 10)
	if !added || len(got) != 3 || got[0].ID != 9 || got[1].ID != 1 {
		t.Errorf("new pin: %+v added=%v", got, added)
	}
	got, added = withPin([]LawRow{a, p, b}, &p, 10)
	if added || len(got) != 3 || got[0].ID != 9 || got[1].ID != 1 || got[2].ID != 2 {
		t.Errorf("pin already there: %+v added=%v", got, added)
	}
	got, _ = withPin([]LawRow{a, b}, &p, 2)
	if len(got) != 2 || got[0].ID != 9 {
		t.Errorf("limit: %+v", got)
	}
	if got, added := withPin([]LawRow{a}, nil, 10); added || len(got) != 1 {
		t.Errorf("no pin: %+v", got)
	}
}

func TestCybercrimeFindsLaw81(t *testing.T) {
	s := testServer(t)
	s.ipLim = newIPLimiter(1_000_000, 1_000_000)
	for _, q := range cyberQueries {
		var out apiSearchResult
		getJSON(t, s, "/api/v1/search/?type=laws&q="+url.QueryEscape(q), &out)
		if len(out.Laws) == 0 || out.Laws[0].ID != lawEtransactions {
			t.Errorf("api %q: first law = %+v, want %d", q, out.Laws, lawEtransactions)
		}
		var all apiSearchResult
		getJSON(t, s, "/api/v1/search/?q="+url.QueryEscape(q), &all)
		if len(all.Laws) == 0 || all.Laws[0].ID != lawEtransactions {
			t.Errorf("api (all kinds) %q: first law = %+v, want %d", q, all.Laws, lawEtransactions)
		}
		res, _, err := s.searchFor(t.Context(), q)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(res.Laws) == 0 || res.Laws[0].ID != lawEtransactions {
			t.Errorf("search page %q: first law = %+v, want %d", q, res.Laws, lawEtransactions)
		}
	}

	var out apiSearchResult
	getJSON(t, s, "/api/v1/search/?q=cybercrime&limit=20", &out)
	n := 0
	for _, l := range out.Laws {
		if l.ID == lawEtransactions {
			n++
		}
	}
	if n != 1 || len(out.Laws) < 2 {
		t.Errorf("cybercrime: law 81 listed %d times among %d laws", n, len(out.Laws))
	}

	for _, path := range []string{"/api/v1/search/?type=laws&year=1999&q=cybercrime", "/api/v1/search/?type=laws&offset=1&q=cybercrime"} {
		var y apiSearchResult
		getJSON(t, s, path, &y)
		for _, l := range y.Laws {
			if l.ID == lawEtransactions {
				t.Errorf("%s lists law 81", path)
			}
		}
	}
}
