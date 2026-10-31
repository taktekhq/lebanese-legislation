package semantic

import (
	"crypto/sha256"
	"encoding/hex"
)

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
	Start    int
	End      int
	Hash     string
	Distance float64
}

func Hash(text string) string { h := sha256.Sum256([]byte(text)); return hex.EncodeToString(h[:]) }
