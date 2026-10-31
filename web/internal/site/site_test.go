package site

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzReportsBuildID(t *testing.T) {
	old := BuildID
	BuildID = "test-build"
	t.Cleanup(func() { BuildID = old })

	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("GET /healthz = %d, want 200", w.Code)
	}
	var body struct {
		Status string `json:"status"`
		Build  string `json:"build"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Build != "test-build" {
		t.Fatalf("GET /healthz = %+v, want status ok and build test-build", body)
	}
}

func TestHeading(t *testing.T) {
	for in, want := range map[string]string{
		"الكتاب الاول : - في انواع الموجبات": "الكتاب الاول: في انواع الموجبات",
		"الباب الاول: - في الموجبات المدنية": "الباب الاول: في الموجبات المدنية",
		"1 - - مفاعيل التضامن":               "1 - مفاعيل التضامن",
		"المواد":                             "المواد",
	} {
		if got := Heading(in); got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
}

func TestNormalizeNumber(t *testing.T) {
	for in, want := range map[string]string{"٢٣": "23", "المادة 23": "23", "12bis": "12 مكرر", "12 - مكرر2": "12 مكرر 2", " 14  مكرر ": "14 مكرر"} {
		if got := normalizeNumber(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestArticlesAr(t *testing.T) {
	for n, want := range map[int]string{1: "مادة واحدة", 2: "مادتان", 3: "3 مواد", 10: "10 مواد", 11: "11 مادة", 103: "103 مواد", 250: "250 مادة"} {
		if got := articlesAr(n); got != want {
			t.Errorf("articlesAr(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestHealthzAndDevelopersAnswer200(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/healthz", "/developers/", "/en/developers/"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Errorf("GET %s = %d, want 200", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/developers/", nil))
	if !strings.Contains(w.Body.String(), "/api/v1/search/") {
		t.Error("GET /developers/ does not document the JSON API")
	}
}
