package modelout

import (
	"context"
	"strings"
	"testing"
)

// copyRows refuses a law-text table before it touches the connection, even
// with no rows.
func TestCopyRowsRefusesLawText(t *testing.T) {
	for _, table := range []string{"legislation", "legislation_article", "article_version", "amendment"} {
		for _, rows := range [][][]any{nil, {{1, "1", "نص"}}} {
			err := copyRows(context.Background(), nil, table, []string{"law_id", "number", "text_ar"}, rows)
			if err == nil || !strings.Contains(err.Error(), "modelout writes no law text") {
				t.Errorf("%s, %d rows: %v", table, len(rows), err)
			}
		}
	}
	if err := copyRows(context.Background(), nil, "ruling_page", nil, nil); err != nil {
		t.Errorf("no rows for another table: %v", err)
	}
}
