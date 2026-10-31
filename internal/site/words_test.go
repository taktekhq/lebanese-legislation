package site

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestWordForKind(t *testing.T) {
	for kind, want := range map[string]string{
		"قانون": "law", "قانون منفذ بمرسوم": "law", "الدستور": "law",
		"مرسوم": "decree", "مرسوم إشتراعى": "decree", "مرسوم نافذ حكما": "decree",
		"قرار": "text", "تعميم": "text", "علم وخبر": "text",
	} {
		if got := wordForKind(kind); got != want {
			t.Errorf("wordForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestRightWord(t *testing.T) {
	textWords = map[int]string{335716: "decree", 7: "text"}
	defer func() { textWords = map[int]string{} }()
	s := &Server{}
	cases := []struct{ path, to string }{
		{"/law/335716/", "/decree/335716/"},
		{"/decree/7/feed.xml", "/text/7/feed.xml"},
		{"/decree/335716/", ""},
		{"/law/12/", ""},
		{"/text/12/", "/law/12/"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, c.path, nil)
		var id int
		for _, x := range []int{335716, 7, 12} {
			if strings.Contains(c.path, "/"+strconv.Itoa(x)+"/") {
				id = x
			}
		}
		moved := s.rightWord(w, r, id)
		if got := w.Header().Get("Location"); moved != (c.to != "") || got != c.to {
			t.Errorf("%s: moved %v to %q, want %q", c.path, moved, got, c.to)
		}
	}
}
