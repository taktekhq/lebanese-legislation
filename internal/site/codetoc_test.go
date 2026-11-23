package site

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A code's table of contents (/codes/<code>/) lists chapter headings over
// article chips. With the display heading (.sec__title, up to 3rem) the
// chapters dwarfed their articles and the page looked broken (a law
// student, 23 Nov 2026). They stay on the body scale: at most 1.25rem.
func TestCodeTOCHeadingsOnTheBodyScale(t *testing.T) {
	tpl, err := os.ReadFile("templates/codehub.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tpl), "sec__title") || !strings.Contains(string(tpl), `class="codetoc__heading"`) {
		t.Fatal("codehub.html must use codetoc__heading for chapter headings, not the display sec__title")
	}
	css, err := os.ReadFile("static/legislation.css")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\.codetoc__heading \{[^}]*font-size: ([0-9.]+)rem`).FindSubmatch(css)
	if m == nil {
		t.Fatal("no font-size for .codetoc__heading")
	}
	if size, _ := strconv.ParseFloat(string(m[1]), 64); size > 1.25 {
		t.Errorf(".codetoc__heading is %srem, want at most 1.25rem", m[1])
	}
}
