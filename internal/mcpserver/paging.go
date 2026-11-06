package mcpserver

import (
	"math"
	"strconv"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Paging. Every tool that answers a page at a time (get_law_texts,
// get_links, list_legislation, list_gazette_issues) takes cursor, the
// next_cursor of the previous answer, and gives next_cursor only when there
// is a next page. The next page is a WHERE on the sort key: an index answers
// it at any depth, and a walk neither repeats nor skips a row while texts are
// added nightly. It is also how MCP itself pages (an opaque nextCursor).
//
// offset was removed in version 2.0.0 (6 Nov 2026): LIMIT n OFFSET m made
// Postgres read and throw away every row before the page (0.85 s for a deep
// page of laws, 3 to 9 s on the container under a crawl). A call that still
// passes it gets a tool error that says what to do instead.

// badCursor is the error for a cursor this server did not make.
const badCursor = "cursor is not one this server made; pass next_cursor unchanged"

// offsetGone is the error for a call that passes offset, or "" when it does
// not. hint is the tool's own way to reach a row without walking to it.
func offsetGone(a args, hint string) string {
	if v, has := a["offset"]; !has || v == nil {
		return ""
	}
	return "offset was removed in version 2.0.0 (6 Nov 2026). Pass cursor, the next_cursor of the previous answer. " + hint
}

// readCursor reads the cursor argument with parse. given is false when it
// is absent, null or "" (the first page); msg is badCursor when it is
// anything parse does not accept.
func readCursor[K any](v any, parse func(string) (K, bool)) (key K, given bool, msg string) {
	switch x := v.(type) {
	case nil:
		return key, false, ""
	case string:
		if x == "" {
			return key, false, ""
		}
		k, good := parse(x)
		if !good {
			return key, false, badCursor
		}
		return k, true, ""
	}
	return key, false, badCursor
}

// int4 reports whether n fits a Postgres integer column (every id and
// position is one), so a forged cursor is refused instead of failing in the
// query.
func int4(n int64) bool { return n >= math.MinInt32 && n <= math.MaxInt32 }

// positionCursor reads a get_law_texts cursor: the last article's position.
func positionCursor(tok string) (int64, bool) {
	n, good := cursor.ParseInt(tok)
	return n, good && int4(n)
}

// dateID is a list row's sort key: its date (YYYY-MM-DD) and id.
type dateID struct {
	date string
	id   int64
}

// dateIDCursor reads a list cursor.
func dateIDCursor(tok string) (dateID, bool) {
	d, id, good := cursor.ParseDateID(tok)
	return dateID{d, id}, good && int4(id)
}

// linkKey is an edge's sort key in get_links: whether the text it comes from
// has a date, the date (cursor.NoDate when it has none) and the edge id. The
// flag is kept rather than read from the date, so the key is exact.
type linkKey struct {
	dated bool
	date  string
	id    int64
}

func (k linkKey) token() string {
	flag := "0"
	if k.dated {
		flag = "1"
	}
	return cursor.Encode(flag, k.date, strconv.FormatInt(k.id, 10))
}

func linkCursor(tok string) (linkKey, bool) {
	p, good := cursor.Decode(tok, 3)
	if !good || (p[0] != "0" && p[0] != "1") {
		return linkKey{}, false
	}
	if _, err := time.Parse("2006-01-02", p[1]); err != nil {
		return linkKey{}, false
	}
	id, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil || !int4(id) {
		return linkKey{}, false
	}
	return linkKey{p[0] == "1", p[1], id}, true
}
