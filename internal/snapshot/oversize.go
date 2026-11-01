package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// D1's maximum SQL statement length is 100,000 bytes (a single string,
// BLOB or row may be up to 2,000,000 bytes, but it must travel as a bound
// parameter, not inlined as SQL text, once the statement itself would
// cross that line). A handful of articles and rulings are long enough
// that their row alone, rendered as an INSERT literal, would bump into
// that 100,000-byte ceiling (found while loading the first real
// snapshot: "statement too long: SQLITE_TOOBIG" on a ~390,000-byte
// statement). Those rows are written here instead, as JSON lines with
// their raw, unescaped values; deploy/load_oversize.py applies them
// through D1's query API with bound parameters, which is not subject to
// the statement-length limit.
const oversizeThreshold = 70_000 // bytes of one rendered SQL tuple

type oversizeWriter struct {
	f   *os.File
	enc *json.Encoder
	n   int
}

func newOversizeWriter(dir string) (*oversizeWriter, error) {
	f, err := os.Create(filepath.Join(dir, "oversize.jsonl"))
	if err != nil {
		return nil, err
	}
	return &oversizeWriter{f: f, enc: json.NewEncoder(f)}, nil
}

type oversizeRow struct {
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	Values  []any    `json:"values"`
}

func (o *oversizeWriter) write(table string, cols []string, vals []any) error {
	o.n++
	return o.enc.Encode(oversizeRow{Table: table, Columns: cols, Values: vals})
}

func (o *oversizeWriter) close() error {
	if o.f == nil {
		return nil
	}
	return o.f.Close()
}
