package calculatedcurrent

import (
	"bytes"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"reflect"
	"testing"
	"time"
)

type exportRows struct {
	pgx.Rows
	values [][]any
	i      int
}

func (r *exportRows) Next() bool { return r.i < len(r.values) }
func (r *exportRows) Err() error { return nil }
func (r *exportRows) Scan(dst ...any) error {
	for i, v := range r.values[r.i] {
		reflect.ValueOf(dst[i]).Elem().Set(reflect.ValueOf(v))
	}
	r.i++
	return nil
}
func TestExportVerifiedCurrentRows(t *testing.T) {
	var source string
	for _, r := range sources(t) {
		if r.Number == "86" {
			source = r.TextAr
		}
	}
	future := time.Now().AddDate(1, 0, 0)
	rows := &exportRows{values: [][]any{
		{244565, "86", 86, source, "in_force", false, (*time.Time)(nil), false},
		{244565, "86", 86, source + " changed", "in_force", false, (*time.Time)(nil), false},
		{244565, "86", 86, source, "in_force", true, (*time.Time)(nil), false},
		{244565, "86", 86, source, "in_force", false, &future, false},
		{244565, "86", 86, source, "in_force", false, (*time.Time)(nil), true},
	}}
	var out bytes.Buffer
	n, err := writeRows(rows, &out)
	if err != nil || n != 1 {
		t.Fatalf("export=%d %v", n, err)
	}
	var row struct {
		LawID             int    `json:"law_id"`
		Number            string `json:"number"`
		Position          int    `json:"position"`
		SourceSHA256      string `json:"source_sha256"`
		CalculatedCurrent Record `json:"calculated_current"`
	}
	if err = json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.LawID != 244565 || row.Number != "86" || row.Position != 86 || row.SourceSHA256 != row.CalculatedCurrent.SourceSHA256 || row.CalculatedCurrent.SourceTextAr != source {
		t.Fatal(row)
	}
}

func TestExportStdoutDestination(t *testing.T) {
	var source string
	for _, r := range sources(t) {
		if r.Number == "86" {
			source = r.TextAr
		}
	}
	rows := &exportRows{values: [][]any{{244565, "86", 86, source, "in_force", false, (*time.Time)(nil), false}}}
	var out bytes.Buffer
	n, err := writeDestination(rows, "-", &out)
	if err != nil || n != 1 || !bytes.Contains(out.Bytes(), []byte(`"calculated_current"`)) {
		t.Fatalf("stdout export=%d %v %s", n, err, out.String())
	}
}
