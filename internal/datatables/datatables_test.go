package datatables

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArticles(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "tables", "legislation_article")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "COLUMNS"), []byte("law_id,position,centre_id,section_id,number,number_int,note,text_ar,text_fr,images,in_force_from\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "000001.tsv"), []byte(
		"7\t2\t\\N\t\\N\t2\t2\t\\N\tالفقرة الثانية\\nسطر\t\\N\t\\N\t\\N\n"+
			"7\t1\t\\N\t\\N\t1\t1\t\\N\tنص\\\\مائل\t\\N\t\\N\t\\N\n"+
			"7\t3\t\\N\t\\N\t3\t3\t\\N\t\\N\t\\N\t\\N\t\\N\n"+
			"8\t1\t\\N\t\\N\t1\t1\t\\N\tغيره\t\\N\t\\N\t\\N\n"), 0o644)
	got, err := Articles(data, map[int]bool{7: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Position != 1 || got[1].Position != 2 {
		t.Fatalf("articles: %+v", got)
	}
	if got[0].Text != `نص\مائل` || got[1].Text != "الفقرة الثانية\nسطر" || len(got[0].Hash) != 64 {
		t.Errorf("unescape or hash: %+v", got)
	}
}
