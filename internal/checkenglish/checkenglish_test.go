package checkenglish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/db"
)

// Expected values come from the repo's own data: db/titles_en.tsv is English,
// and the Arabic title of the same text (7815, Lebanon and Senegal) is not.
func TestLatin(t *testing.T) {
	lines := strings.Split(db.TitlesEn, "\n")
	en := strings.Split(lines[1], "\t")[1]
	if !strings.HasPrefix(lines[1], "7815\t") || !Latin(en) {
		t.Fatalf("title_en of 7815 should read as English: %q", en)
	}
	ar := "المادة الأولى: يعمل بهذا القانون فور نشره في الجريدة الرسمية"
	if Latin(ar) {
		t.Fatal("Arabic article text read as English")
	}
	// An article citing a Latin abbreviation inside Arabic text stays Arabic.
	if Latin("تطبق المادة 5 من قانون " + "CNSS" + " على العاملين في القطاع الخاص") {
		t.Fatal("one Latin word made the line English")
	}
	if len(EnglishLines(ar+"\n"+en)) != 1 {
		t.Fatal("expected exactly the English line")
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Each fixture fails alone; the clean one passes.
func TestSurface(t *testing.T) {
	bare := t.TempDir()
	write(t, bare, "internal/site/templates/law.html", `<h1>{{.TitleEn}}</h1>`)
	if v, _ := Surface(bare); len(v) != 1 || !strings.Contains(v[0], "law.html") {
		t.Fatalf("title without flag: %v", v)
	}
	flagged := t.TempDir()
	write(t, flagged, "internal/site/templates/law.html", `<p>{{t "seo.title_en_label"}}</p><h1>{{.TitleEn}}</h1>`)
	write(t, flagged, "internal/site/api.go", "TitleEn *string `json:\"title_en,omitempty\"` // unofficial English title")
	write(t, flagged, "internal/site/sitemap.go", `'legislation_title_en')`) // names the table only
	if v, _ := Surface(flagged); len(v) != 0 {
		t.Fatalf("clean fixture: %v", v)
	}
}
