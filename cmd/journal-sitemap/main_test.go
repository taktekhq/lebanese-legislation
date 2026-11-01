package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"testing"
)

type failedSitemapWriter struct{ err error }

func (w failedSitemapWriter) Write([]byte) (int, error) { return 0, w.err }

type shortSitemapWriter struct{}

func (shortSitemapWriter) Write([]byte) (int, error) { return 0, nil }

func TestJournalSitemapExportNeedsNoDatabase(t *testing.T) {
	var output bytes.Buffer
	if err := writeJournalSitemap(&output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.URLs) == 0 {
		t.Fatal("no public journal URLs exported")
	}
	seen := map[string]bool{}
	for _, u := range result.URLs {
		if seen[u.Loc] {
			t.Errorf("duplicate %s", u.Loc)
		}
		seen[u.Loc] = true
	}
}
func TestJournalSitemapExportReportsWriterFailure(t *testing.T) {
	failure := errors.New("write failed")
	if err := writeJournalSitemap(failedSitemapWriter{failure}); !errors.Is(err, failure) {
		t.Errorf("got %v", err)
	}
	if err := writeJournalSitemap(shortSitemapWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("got %v", err)
	}
}
