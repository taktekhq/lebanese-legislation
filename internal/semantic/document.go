// Package semantic indexes canonical corpus text in an independent pgvector database.
// Indexed text is a hint only: callers must Load and hash-check every hit before use.
package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const Dimensions = 768
const DefaultModel = "gemini-embedding-001"

type Document struct {
	Kind  string
	ID    int
	Part  string
	Lang  string
	Title string
	Text  string
}

type Hit struct {
	Document
	Start    int // Rune offsets into the full canonical Text, end exclusive.
	End      int
	Hash     string
	Distance float64
}

// Hash fingerprints the exact canonical text, without trimming or normalization.
func Hash(text string) string { h := sha256.Sum256([]byte(text)); return hex.EncodeToString(h[:]) }

// Chunks preserves contiguous original text and counts offsets in Unicode code points.
func Chunks(d Document) []Hit {
	if strings.TrimSpace(d.Text) == "" {
		return nil
	}
	r := []rune(d.Text)
	hash := Hash(d.Text)
	var out []Hit
	for start := 0; start < len(r); start += 780 {
		end := min(start+900, len(r))
		chunk := d
		chunk.Text = string(r[start:end])
		out = append(out, Hit{Document: chunk, Start: start, End: end, Hash: hash})
		if end == len(r) {
			break
		}
	}
	return out
}
