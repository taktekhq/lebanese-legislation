package vectorize

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// ChunkID uses semantic_chunks' entire composite primary key, in schema order.
// Keys over Vectorize's 64-byte limit use a 128-bit SHA-256 prefix.
func ChunkID(model, kind string, sourceID int, part, lang, hash string, start int) string {
	joined := strings.Join([]string{model, kind, strconv.Itoa(sourceID), part, lang, hash, strconv.Itoa(start)}, ":")
	if len(joined) <= 64 {
		return joined
	}
	sum := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(sum[:16])
}

const chunkKeySQL = `model || ':' || kind || ':' || source_id::text || ':' || part || ':' || lang || ':' || hash || ':' || chunk_start::text`

// ChunkIDSQL is the PostgreSQL equivalent of ChunkID on the UTF-8 release database, for indexed row lookup.
// Escape backslashes before bytea decoding to preserve the UTF-8 bytes. Unlike
// convert_to/textsend, these functions are immutable and support an expression index.
// The serving container creates an expression index with this same expression.
const ChunkIDSQL = `(CASE WHEN octet_length(` + chunkKeySQL + `) <= 64 THEN ` + chunkKeySQL + ` ELSE encode(substring(sha256(decode(replace(` + chunkKeySQL + `, chr(92), chr(92) || chr(92)), 'escape')) from 1 for 16), 'hex') END)`
