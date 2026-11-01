// Package text turns the Centre's printed values into typed ones.
package text

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var dateRe = regexp.MustCompile(`(\d{1,2})\s*[/\-.]\s*(\d{1,2})\s*[/\-.]\s*(\d{4})`)

// DMY reads the first day/month/year date in s. The Centre writes
// 01/01/1900 and 01/01/0001 where it has no date; those come back as nil,
// as does anything that is not a real calendar date.
func DMY(s string) *time.Time { return date(s, false) }

// MDY is DMY for the one field printed month first (advisory opinions,
// .NET's default "2/23/1978").
func MDY(s string) *time.Time { return date(s, true) }

func date(s string, monthFirst bool) *time.Time {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	y, _ := strconv.Atoi(m[3])
	d, mo := a, b
	if monthFirst {
		d, mo = b, a
	}
	if y <= 1900 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return nil
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d { // 31/02 and the like
		return nil
	}
	return &t
}

var leadInt = regexp.MustCompile(`\d+`)

// LeadingInt returns the first integer in s, or nil.
func LeadingInt(s string) *int {
	m := leadInt.FindString(s)
	if m == "" || len(m) > 9 {
		return nil
	}
	n, _ := strconv.Atoi(m)
	return &n
}

// Opt returns nil for "", else a pointer to s, for nullable columns.
func Opt(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// Year returns a plausible year from s, or nil (the Centre prints 0).
func Year(s string) *int {
	n := LeadingInt(s)
	if n == nil || *n < 1800 || *n > 2100 {
		return nil
	}
	return n
}
