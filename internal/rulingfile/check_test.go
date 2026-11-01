package rulingfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestMarkedSpansExpectedFromVote(t *testing.T) {
	const voted = "قرار رقم " + markStart + "٣٢٣٢" + markSep + "٣٢٢٢" + markEnd + " بتاريخ ٢٠٠٠"
	if n, ok := markedSpans(voted); !ok || n != 1 {
		t.Fatalf("markedSpans(%q) = %d, %v; want 1, true (route_test.go TestMerge3 tie case)", voted, n, ok)
	}
	for _, bad := range []string{"a" + markStart + "x" + markEnd, "a" + markSep + "b", "a" + markStart + markSep + "b" + markEnd, "a" + markStart + "x" + markSep + "x" + markEnd} {
		if _, ok := markedSpans(bad); ok {
			t.Errorf("markedSpans accepted malformed/equal sides %q", bad)
		}
	}
}

func TestLedgerSpendExpectedUSDColumn(t *testing.T) {
	dir := t.TempDir()
	g := filepath.Join(dir, "gemini.tsv")
	if err := os.WriteFile(g, []byte("batch\tkey\tusd\terror\nb1\t0063-1\t12.500000\t\nb1\t0063-2\t3.250000\trefused\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := filepath.Join(dir, "claude.tsv")
	if err := os.WriteFile(c, []byte("batch\tmodel\tkey\tusd\terror\nb2\tsonnet\t0064-1\t4.125000\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	gs, bad, err := ledgerSpend(g, false, &out)
	if err != nil || bad {
		t.Fatalf("Gemini ledger: invalid=%v err=%v", bad, err)
	}
	cs, bad, err := ledgerSpend(c, true, &out)
	if err != nil || bad {
		t.Fatalf("Claude ledger: invalid=%v err=%v", bad, err)
	}
	if gs != 15.75 || cs != 4.125 {
		t.Fatalf("ledgerSpend = %v + %v, want 15.75 + 4.125", gs, cs)
	}
}

func TestLedgerSpendRejectsMalformedAmount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spend.tsv")
	if err := os.WriteFile(path, []byte("batch\tkey\tusd\terror\nb1\tkey\tnope\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, bad, err := ledgerSpend(path, false, &out)
	if err != nil || !bad || !strings.Contains(out.String(), "malformed amount") {
		t.Fatalf("invalid ledger: bad=%v err=%v output=%q", bad, err, out.String())
	}
}

type checkRow []any
type checkRows struct {
	rows []checkRow
	at   int
	row  checkRow
}

func (r *checkRows) Close()                                       {}
func (r *checkRows) Err() error                                   { return nil }
func (r *checkRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *checkRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *checkRows) Values() ([]any, error)                       { return r.row, nil }
func (r *checkRows) RawValues() [][]byte                          { return nil }
func (r *checkRows) Conn() *pgx.Conn                              { return nil }
func (r *checkRows) TypeMap() *pgtype.Map                         { return nil }
func (r *checkRows) Next() bool {
	if r.at >= len(r.rows) {
		return false
	}
	r.row = r.rows[r.at]
	r.at++
	return true
}
func (r *checkRows) Scan(dest ...any) error {
	if len(dest) != len(r.row) {
		return fmt.Errorf("Scan got %d destinations for %d values", len(dest), len(r.row))
	}
	for i, v := range r.row {
		switch d := dest[i].(type) {
		case *int:
			x, ok := v.(int)
			if !ok {
				return fmt.Errorf("column %d is %T, want int", i, v)
			}
			*d = x
		case *bool:
			x, ok := v.(bool)
			if !ok {
				return fmt.Errorf("column %d is %T, want bool", i, v)
			}
			*d = x
		case *string:
			x, ok := v.(string)
			if !ok {
				return fmt.Errorf("column %d is %T, want string", i, v)
			}
			*d = x
		case *time.Time:
			x, ok := v.(time.Time)
			if !ok {
				return fmt.Errorf("column %d is %T, want time.Time", i, v)
			}
			*d = x
		default:
			return fmt.Errorf("unsupported destination %T", d)
		}
	}
	return nil
}

type checkDB struct{ files, pages []checkRow }

func (d checkDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "FROM ruling_file") {
		return &checkRows{rows: d.files}, nil
	}
	if strings.Contains(sql, "FROM ruling_page") {
		return &checkRows{rows: d.pages}, nil
	}
	return nil, fmt.Errorf("unexpected query: %s", sql)
}

func TestCheckRulesAgainstFixture(t *testing.T) {
	root := t.TempDir()
	dir, archiveDir := filepath.Join(root, "files"), filepath.Join(root, "archive")
	for _, p := range []string{filepath.Join(dir, "0000"), filepath.Join(dir, "gemini"), filepath.Join(dir, "claude-api"), filepath.Join(archiveDir, "ruling", "0000")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "ruling.tsv"), []byte("doc_id\turl\tfirst_seen\tlast_changed\tblob_sha\n1\t/source/1\t\t\t\n2\t/source/2\t\t\t\n3\t/source/3\t\t\t\n4\t/source/4\t\t\t\n5\t/source/5\t\t\t\n7\t/source/7\t\t\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for id, body := range map[int]string{1: `<a href="RulingFile.aspx?RuliID=1">PDF</a>`, 2: `<a href="RulingFile.aspx?RuliID=2">PDF</a>`, 3: `<a href="RulingFile.aspx?RuliID=3">PDF</a>`, 4: `<a href="RulingFile.aspx?RuliID=4">PDF</a>`, 7: `<a href="RulingFile.aspx?RuliID=8">PDF</a>`, 5: `<a href="/legal/ruling/5">PDF</a>`} {
		if err := os.WriteFile(filepath.Join(archiveDir, "ruling", "0000", fmt.Sprintf("%d.html", id)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int{1, 2, 3, 4, 5, 7} {
		if err := os.WriteFile(filepath.Join(dir, "0000", fmt.Sprintf("%d.pdf", id)), []byte("%PDF-fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	writeReading := func(id int, p Page) {
		t.Helper()
		rd := fmt.Sprintf(`{"id":%d,"model":"gemini+claude","read_at":%q,"pages":[%s]}`, id, now.Add(-time.Hour).Format(time.RFC3339), pageJSON(t, p))
		if err := os.WriteFile(filepath.Join(dir, "0000", fmt.Sprintf("%d.read.json", id)), []byte(rd), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeReading(1, Page{N: 1, Text: "قرار صحيح", Model: "gemini+claude"})
	writeReading(2, Page{N: 1, Text: "قرار غير موسوم", Model: "gemini+claude", Disputed: 1})
	writeReading(3, Page{N: 1, Text: "قرار منفرد", Model: "gemini", Disputed: 1})
	writeReading(4, Page{N: 1, Text: "قرار بلا صف", Model: "gemini+claude"})
	writeReading(5, Page{N: 1, Text: "قرار صحيح", Model: "gemini+claude"})
	writeReading(7, Page{N: 1, Text: "", Model: "gemini+claude"})
	writeReading(6, Page{N: 1, Text: "قرار معلّق", Model: "gemini", Want: []string{"claude"}})
	if err := os.Remove(filepath.Join(dir, "0000", "4.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gemini", "spend.tsv"), []byte("batch\tkey\tusd\terror\nb1\t1-1\t200.000000\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-api", "spend.tsv"), []byte("batch\tmodel\tkey\tusd\terror\nb2\tsonnet\t2-1\t1.000000\t\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	db := checkDB{
		files: []checkRow{{1, true}, {2, true}, {3, true}, {4, true}, {5, true}},
		pages: []checkRow{{1, 1, "قرار صحيح", now}, {2, 1, "قرار غير موسوم", now}, {3, 1, "قرار منفرد", now}, {4, 1, "قرار بلا صف", now}, {5, 1, "قرار صحيح", now}, {6, 1, "قرار معلّق", now}},
	}
	var out bytes.Buffer
	err := Check(context.Background(), db, archiveDir, CheckOptions{Dir: dir, Since: 26 * time.Hour, Budget: 200, Now: now}, &out)
	if err == nil {
		t.Fatal("Check passed fixture with violations")
	}
	for _, want := range []string{"FAIL disputed-marks ruling=2 page=1", "FAIL reading-count ruling=3 page=1", "published page still wants readers", "FAIL reading-count ruling=7 page=1 page has fewer than two readings"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "FAIL disputed-marks ruling=3 page=1") {
		t.Errorf("non-voted page was checked for disputed marks:\n%s", out.String())
	}
	for _, want := range []string{"FAIL file-link ruling=4 page=0 ruling_file row has no PDF", "FAIL file-link ruling=5 page=0 PDF ruling has no source PDF link", "FAIL file-link ruling=7 page=0 PDF ruling has no source PDF link"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(out.String(), "FAIL budget ruling=0 page=0 spend $201.000000 exceeds $200.00") {
		t.Errorf("output missing combined ledger budget failure:\n%s", out.String())
	}

	// The nightly's copy holds readings only; the PDFs stay on the machine that reads them.
	pdfDir := filepath.Join(root, "pdfs", "0000")
	if err := os.MkdirAll(pdfDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2, 3, 5, 7} {
		if err := os.Rename(filepath.Join(dir, "0000", fmt.Sprintf("%d.pdf", id)), filepath.Join(pdfDir, fmt.Sprintf("%d.pdf", id))); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	_ = Check(context.Background(), db, archiveDir, CheckOptions{Dir: dir, Since: 26 * time.Hour, Budget: 200, Now: now}, &out)
	if !strings.Contains(out.String(), "FAIL file-link ruling=1 page=0 ruling_file row has no PDF") {
		t.Errorf("without PDFDir the moved PDFs should be missing:\n%s", out.String())
	}
	out.Reset()
	_ = Check(context.Background(), db, archiveDir, CheckOptions{Dir: dir, PDFDir: filepath.Join(root, "pdfs"), Since: 26 * time.Hour, Budget: 200, Now: now}, &out)
	if strings.Contains(out.String(), "FAIL file-link ruling=1 page=0 ruling_file row has no PDF") {
		t.Errorf("PDFDir was not read for rule 1:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FAIL file-link ruling=4 page=0 ruling_file row has no PDF") {
		t.Errorf("PDFDir hid a PDF that is gone:\n%s", out.String())
	}
}

func pageJSON(t *testing.T, p Page) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
