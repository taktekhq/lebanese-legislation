package cursor

import (
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	tok := DateID("2020-05-03", 123456)
	if strings.ContainsAny(tok, "+/=|") {
		t.Fatalf("token %q is not URL-safe", tok)
	}
	d, id, ok := ParseDateID(tok)
	if !ok || d != "2020-05-03" || id != 123456 {
		t.Fatalf("got %q %d %v", d, id, ok)
	}
	n, ok := ParseInt(Int(42))
	if !ok || n != 42 {
		t.Fatalf("int: %d %v", n, ok)
	}
	p, ok := Decode(Encode("1", "0.53", "2001-01-01", "9"), 4)
	if !ok || strings.Join(p, ",") != "1,0.53,2001-01-01,9" {
		t.Fatalf("parts: %v %v", p, ok)
	}
}

func TestRejects(t *testing.T) {
	for _, tok := range []string{
		"", "x", "!!!", strings.Repeat("A", 300),
		Encode("2020-05-03"),               // one part, two wanted
		Encode("2020-05-03", "1", "2"),     // three parts
		Encode("2020-13-40", "1"),          // not a date
		Encode("2020-05-03", "x"),          // not an id
		Encode("2020-05-03", ""),           // empty part
		Encode("2020-05-03", "1") + "Zm9v", // trailing garbage
	} {
		if _, _, ok := ParseDateID(tok); ok {
			t.Errorf("accepted %q", tok)
		}
	}
	if _, ok := Decode("djJ8MjAyMC0wNS0wM3wx", 2); ok { // "v2|2020-05-03|1"
		t.Error("accepted another version")
	}
}

func TestDate(t *testing.T) {
	if Date(nil) != NoDate {
		t.Fatal("nil date")
	}
	d := time.Date(1999, 2, 3, 0, 0, 0, 0, time.UTC)
	if Date(&d) != "1999-02-03" {
		t.Fatal(Date(&d))
	}
}
