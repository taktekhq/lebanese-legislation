package snapshot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type pageFixture struct {
	id, n int
	text  string
	low   bool
}
type pageRows struct {
	pgx.Rows
	pages []pageFixture
	i     int
	err   error
}

func (r *pageRows) Next() bool { return r.i < len(r.pages) }
func (r *pageRows) Err() error { return r.err }
func (r *pageRows) Scan(dest ...any) error {
	p := r.pages[r.i]
	r.i++
	*dest[0].(*int) = p.id
	*dest[1].(*int) = p.n
	*dest[2].(*string) = p.text
	*dest[3].(*bool) = p.low
	return nil
}

func TestRulingPageSchema(t *testing.T) {
	for _, want := range []string{"CREATE TABLE ruling_page (", "ruling_id      INTEGER", "n              INTEGER", "text_ar        TEXT", "low_confidence INTEGER", "PRIMARY KEY (ruling_id, n)"} {
		if !strings.Contains(Schema(), want) {
			t.Errorf("schema missing %q", want)
		}
	}
	if !strings.Contains(rulingPagesQuery, "JOIN ruling r ON r.id = p.ruling_id") || !strings.Contains(rulingPagesQuery, "ORDER BY p.ruling_id, p.n") {
		t.Fatal("pages must belong to exported rulings and be ordered")
	}
}

func TestExportRulingPages(t *testing.T) {
	dir := t.TempDir()
	ch, err := newChunker(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer ch.close()
	ov, err := newOversizeWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ov.close()
	large := strings.Repeat("ح", oversizeThreshold)
	rows := &pageRows{pages: []pageFixture{{42, 1, "نص 'الحكم'", false}, {42, 2, "صعب", true}, {42, 3, large, true}}}
	n, err := exportRulingPageRows(rows, ch, ov)
	if err != nil || n != 3 {
		t.Fatalf("export = %d, %v", n, err)
	}
	if err := ch.close(); err != nil {
		t.Fatal(err)
	}
	if err := ov.close(); err != nil {
		t.Fatal(err)
	}
	var sql strings.Builder
	for _, file := range ch.files {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		sql.Write(data)
	}
	for _, want := range []string{"INSERT INTO ruling_page (ruling_id,n,text_ar,low_confidence)", "(42,1,'نص ''الحكم''',0)", "(42,2,'صعب',1)"} {
		if !strings.Contains(sql.String(), want) {
			t.Errorf("SQL missing %q: %s", want, sql.String())
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "oversize.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row oversizeRow
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	if row.Table != "ruling_page" || len(row.Columns) != 4 || row.Values[2] != large || row.Values[3] != float64(1) {
		t.Fatalf("bad oversize page metadata: %s %v", row.Table, row.Columns)
	}
}

func TestExportRulingPagesReadError(t *testing.T) {
	want := errors.New("read interrupted")
	n, err := exportRulingPageRows(&pageRows{err: want}, nil, nil)
	if n != 0 || !errors.Is(err, want) {
		t.Fatalf("export = %d, %v", n, err)
	}
}
