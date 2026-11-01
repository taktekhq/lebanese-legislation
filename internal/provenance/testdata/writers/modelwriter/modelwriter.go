// Package modelwriter is a fixture for the wording guard's static check: a
// build-path writer that reads a model's answers and stores model text as
// an article's wording. ScanWriters must flag it.
package modelwriter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/jackc/pgx/v5"
)

type answer struct {
	Law     int    `json:"law_id"`
	Article string `json:"article"`
	Text    string `json:"text"`
}

// Store writes each answer's text under a real source id, with a hash it
// computed itself.
func Store(ctx context.Context, conn *pgx.Conn) error {
	raw, err := os.ReadFile("db/model/judgements.jsonl")
	if err != nil {
		return err
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	h := sha256.Sum256([]byte(a.Text))
	_, err = conn.Exec(ctx, `INSERT INTO legislation_article (law_id, position, number, text_ar, source_kind, source_id)
		VALUES ($1, 1, $2, $3, 'ul', $4)`, a.Law, a.Article, a.Text, "lawtext/8225#"+hex.EncodeToString(h[:]))
	return err
}
