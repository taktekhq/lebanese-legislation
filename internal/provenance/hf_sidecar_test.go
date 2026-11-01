package provenance

import (
	"os"
	"strings"
	"testing"
)

// The Parquet export turns data/article_calculated_current.jsonl (written by
// the guarded export) into the dataset's typed table.
func TestParquetCalculatedSidecarContract(t *testing.T) {
	b, err := os.ReadFile("../../.github/scripts/data-parquet.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"article_calculated_current.jsonl", "article_calculated_current.parquet", "law_id: 'INTEGER'", "number: 'VARCHAR'", "calculated_current: 'JSON'", "CAST(calculated_current AS VARCHAR)"} {
		if !strings.Contains(s, want) {
			t.Fatalf("Parquet sidecar missing %q", want)
		}
	}
}
