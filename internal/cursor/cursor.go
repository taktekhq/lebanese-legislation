// Package cursor makes the tokens that page through a list: the key of the
// last row a page showed, so the next page starts right after it.
//
// Every list on the site, the API and the MCP server pages this way, never by
// OFFSET. An OFFSET makes Postgres read and throw away every row before the
// page: page 2,000 of /laws/ read 80,000 laws and their article counts, 835 ms
// on a fast machine and 3 to 9 s on the container under a crawl (6 Nov 2026).
// A cursor is a WHERE on the sort key, which an index answers in about a
// millisecond at any depth.
//
// A token is opaque to callers: base64url of "v1|part|part...". Parts are
// dates, integers and numbers, never free text, so "|" cannot occur in them.
// It imports nothing of the project.
package cursor

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

const (
	version = "v1"
	maxLen  = 400 // a list token is under 60 characters; a search token for all four parts, up to about 210
)

// Encode joins a row's sort key into a token.
func Encode(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(version + "|" + strings.Join(parts, "|")))
}

// Decode splits a token made by Encode into its n parts. ok is false when tok
// is not such a token.
func Decode(tok string, n int) (parts []string, ok bool) {
	if tok == "" || len(tok) > maxLen {
		return nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, false
	}
	parts = strings.Split(string(b), "|")
	if len(parts) != n+1 || parts[0] != version {
		return nil, false
	}
	for _, p := range parts[1:] {
		if p == "" {
			return nil, false
		}
	}
	return parts[1:], true
}

// NoDate stands for a missing date in a sort key: lists sort
// coalesce(date, '0001-01-01'), so texts with no date come last, newest first.
const NoDate = "0001-01-01"

// DateID is the token of a row sorted by date, then id.
func DateID(date string, id int64) string {
	return Encode(date, strconv.FormatInt(id, 10))
}

// ParseDateID reads a DateID token. The date is YYYY-MM-DD.
func ParseDateID(tok string) (date string, id int64, ok bool) {
	p, ok := Decode(tok, 2)
	if !ok {
		return "", 0, false
	}
	if _, err := time.Parse("2006-01-02", p[0]); err != nil {
		return "", 0, false
	}
	id, err := strconv.ParseInt(p[1], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return p[0], id, true
}

// Int is the token of a row sorted by one integer (an id, a position).
func Int(n int64) string { return Encode(strconv.FormatInt(n, 10)) }

// ParseInt reads an Int token.
func ParseInt(tok string) (int64, bool) {
	p, ok := Decode(tok, 1)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(p[0], 10, 64)
	return n, err == nil
}

// Date formats t as a key part; the zero time is NoDate.
func Date(t *time.Time) string {
	if t == nil || t.IsZero() {
		return NoDate
	}
	return t.Format("2006-01-02")
}
