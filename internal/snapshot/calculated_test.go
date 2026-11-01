package snapshot

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCalculatedCurrentSchema(t *testing.T) {
	if !strings.Contains(Schema(), "calculated_current TEXT") {
		t.Fatal("missing nullable calculated_current JSON column")
	}
}

func TestCalculatedArticleExport(t *testing.T) {
	dir := t.TempDir()
	ch, err := newChunker(dir, 1000000)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := newOversizeWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../site/testdata/civil-procedure-amounts.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Number string
		Text   string `json:"text_ar"`
	}
	if err = json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	source := ""
	for _, r := range fixtures {
		if r.Number == "86" {
			source = r.Text
		}
	}
	rows := &articleRows{values: [][]any{
		{244565, 86, "86", (*string)(nil), source, (*string)(nil), (*time.Time)(nil), (*int)(nil), (*string)(nil), "1.0", "in_force", false, (*time.Time)(nil), false},
		{244565, 87, "86", (*string)(nil), source, (*string)(nil), (*time.Time)(nil), (*int)(nil), (*string)(nil), "1.0", "in_force", true, (*time.Time)(nil), false},
	}}
	n, err := exportArticleRows(rows, ch, ov)
	if err != nil || n != 2 {
		t.Fatalf("export=%d %v", n, err)
	}
	if err = ch.close(); err != nil {
		t.Fatal(err)
	}
	if err = ov.close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "data_*.sql"))
	all := ""
	for _, f := range files {
		data, _ := os.ReadFile(f)
		all += string(data)
	}
	if !strings.Contains(all, "calculated_current") || strings.Count(all, `"kind":"statutory_monetary_adjustment"`) != 1 || !strings.Contains(all, sqlStr(source)) || !strings.Contains(all, "5,000,000,000") {
		t.Fatal(all)
	}
}

type articleRows struct {
	pgx.Rows
	values [][]any
	i      int
}

func (r *articleRows) Next() bool { return r.i < len(r.values) }
func (r *articleRows) Err() error { return nil }
func (r *articleRows) Scan(dst ...any) error {
	for i, v := range r.values[r.i] {
		reflect.ValueOf(dst[i]).Elem().Set(reflect.ValueOf(v))
	}
	r.i++
	return nil
}
