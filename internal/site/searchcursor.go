package site

import (
	"errors"
	"strconv"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/cursor"
)

// Paging /api/v1/search/. Each part of a search (laws, articles, rulings,
// Gazette issues) is ranked, and its next page starts after the rank of the
// last row it showed: a WHERE on the whole rank tuple, ending with an id so
// no two rows tie. Never an OFFSET, which made Postgres rank and throw away
// every row before the page.
//
// One next_cursor carries every part asked for: a phase, then the last row's
// key. A part with no rows left is phase 0, done, and only counts. The first
// page (no cursor) also shows what is looked up rather than ranked (a
// citation, a law's id or code, a pinned text, a cited article): those are
// never paged, so a later page may list one of them again at its own rank.

// errOffsetRemoved and errBadCursor are the 400s of a list or a search asked
// for a page the old way, or with a cursor this API did not make.
var (
	errOffsetRemoved = errors.New("offset was removed on 6 Nov 2026: pass cursor, the next_cursor of the previous answer; to jump to a period use since and until")
	errBadCursor     = errors.New("cursor is not one this API made; pass next_cursor unchanged")
)

// The parts of a search, in the order a cursor holds them.
const (
	partLaws = iota
	partArticles
	partRulings
	partGazette
)

// The phases of the laws part: its Arabic titles ranked, then the unofficial
// English titles, leaving out the texts the Arabic titles found
// (lawsEnglish) or, when the Arabic titles were not searched, none
// (lawsEnglishOnly). Every other part has phase 1 only.
const (
	phaseDone       = 0
	phaseRanked     = 1
	lawsEnglish     = 2
	lawsEnglishOnly = 3
)

// searchPart is how a part's key is written: each field i (an integer), r
// (a real, float4), f (a double, float8) or d (a date). The last field is an
// id or a position, never 0 in a row, so 0 there means the phase's start.
var searchParts = [4]struct {
	name   string
	fields string
	phases int
}{
	{"laws", "irdi", lawsEnglishOnly}, // is_code, similarity, issue date, id; the English titles leave the similarity 0
	{"articles", "fiii", phaseRanked}, // score, is_code, law id, position
	{"rulings", "iirdi", phaseRanked}, // tier, phrase, rank, date, id
	{"gazette", "di", phaseRanked},    // publication date, id
}

// searchPos is where one part of a search stands: its phase, and the key of
// the last row it showed there (nil: from the phase's start).
type searchPos struct {
	phase int
	key   []string
}

// searchCursor is a decoded next_cursor; nil on the first page.
type searchCursor struct {
	at [4]*searchPos
}

// pos is where part continues; nil on the first page.
func (c *searchCursor) pos(part int) *searchPos {
	if c == nil {
		return nil
	}
	return c.at[part]
}

// searchedParts are the parts a search of type typ covers.
func searchedParts(typ string) []int {
	for i, p := range searchParts {
		if p.name == typ {
			return []int{i}
		}
	}
	return []int{partLaws, partArticles, partRulings, partGazette}
}

func decodeSearchCursor(tok, typ string) (*searchCursor, bool) {
	parts := searchedParts(typ)
	n := 0
	for _, p := range parts {
		n += 1 + len(searchParts[p].fields)
	}
	f, ok := cursor.Decode(tok, n)
	if !ok {
		return nil, false
	}
	c := &searchCursor{}
	for _, p := range parts {
		spec := searchParts[p]
		phase, err := strconv.Atoi(f[0])
		if err != nil || phase < 0 || phase > spec.phases {
			return nil, false
		}
		key := f[1 : 1+len(spec.fields)]
		for i, kind := range spec.fields {
			if !keyFieldOK(byte(kind), key[i]) {
				return nil, false
			}
		}
		pos := &searchPos{phase: phase}
		if phase != phaseDone && key[len(key)-1] != "0" {
			pos.key = key
		}
		c.at[p] = pos
		f = f[1+len(spec.fields):]
	}
	return c, true
}

func keyFieldOK(kind byte, s string) bool {
	var err error
	switch kind {
	case 'i':
		_, err = strconv.ParseInt(s, 10, 32)
	case 'r':
		_, err = strconv.ParseFloat(s, 32)
	case 'f':
		_, err = strconv.ParseFloat(s, 64)
	case 'd':
		_, err = time.Parse("2006-01-02", s)
	}
	return err == nil
}

// encodeSearchCursor is the next_cursor of a search of type typ whose parts
// stand at next: empty when every part is done.
func encodeSearchCursor(typ string, next [4]searchPos) string {
	var f []string
	more := false
	for _, p := range searchedParts(typ) {
		spec, at := searchParts[p], next[p]
		more = more || at.phase != phaseDone
		f = append(f, strconv.Itoa(at.phase))
		if at.key != nil && at.phase != phaseDone {
			f = append(f, at.key...)
			continue
		}
		for _, kind := range spec.fields {
			if kind == 'd' {
				f = append(f, cursor.NoDate)
			} else {
				f = append(f, "0")
			}
		}
	}
	if !more {
		return ""
	}
	return cursor.Encode(f...)
}

// Key fields, written so the value read back compares equal in SQL: a real
// with the fewest digits that give the same float4 ($n::real), a double the
// same for float8.
func keyInt(n int) string         { return strconv.Itoa(n) }
func keyReal(f float32) string    { return strconv.FormatFloat(float64(f), 'g', -1, 32) }
func keyDouble(f float64) string  { return strconv.FormatFloat(f, 'g', -1, 64) }
func keyDate(t *time.Time) string { return cursor.Date(t) }

// Reading them back; decodeSearchCursor has checked each field.
func argInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func argReal(s string) float32 {
	f, _ := strconv.ParseFloat(s, 32)
	return float32(f)
}

func argDouble(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func argDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

// fill adds ranked rows to a page that holds n rows, up to limit, leaving
// out those dup says are on it already. rows were read with a LIMIT of
// fetched, so as many rows as that may mean more after them. It returns the
// index of the last row it went through (-1: none) and whether the phase has
// rows after that one.
func fill[T any](n, limit int, rows []T, fetched int, dup func(T) bool, add func(T)) (last int, more bool) {
	last = -1
	for i, r := range rows {
		if n >= limit {
			return last, true
		}
		last = i
		if dup(r) {
			continue
		}
		add(r)
		n++
	}
	return last, len(rows) >= fetched
}

// after is where a phase stands once fill went through rows up to last: the
// key of that row, or the key it started from when it went through none.
func after(from *searchPos, phase, last int, key func(int) []string) searchPos {
	if last >= 0 {
		return searchPos{phase: phase, key: key(last)}
	}
	if from != nil && from.phase == phase {
		return *from
	}
	return searchPos{phase: phase}
}
