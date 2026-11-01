package build

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReviewScans prints how each text in a sader-bundle folder will be cut and
// loaded, with warnings, for a person to read before a release: no articles,
// no closing, readers that agree on under 95% of words, an erratum, a Gazette
// date that does not fit the text.
func ReviewScans(dir string, w io.Writer) error {
	metas, err := filepath.Glob(filepath.Join(dir, "*", "meta.json"))
	if err != nil {
		return err
	}
	sort.Strings(metas)
	issues := readIssueIndex(filepath.Join(dir, "issues.tsv"))
	for _, f := range metas {
		var m scanMeta
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var tx struct {
			Pages []struct {
				Cols []string `json:"cols"`
			} `json:"pages"`
		}
		raw, err = os.ReadFile(filepath.Join(filepath.Dir(f), "text.json"))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &tx); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var pages [][]string
		for _, p := range tx.Pages {
			pages = append(pages, p.Cols)
		}
		title := scanTitle(m.Title)
		s := ParseScanText(pages, title)
		var nums []string
		for _, a := range s.Articles {
			nums = append(nums, a.Number)
		}
		gnum := s.GazetteNumber
		if m.Issue != "" {
			gnum = m.Issue
		}
		gdate := ""
		if issued, err := time.Parse("2006-01-02", m.Date); err == nil {
			if d, ok := issues.date(gnum, issued); ok {
				gdate = d.Format("2006-01-02")
			} else if m.IssueDate != "" {
				gdate = m.IssueDate
			} else if s.GazetteDate != nil {
				gdate = s.GazetteDate.Format("2006-01-02")
			}
			if d, err := time.Parse("2006-01-02", gdate); err == nil && (d.Before(issued) || d.After(issued.AddDate(0, 6, 0))) {
				gdate += " (DROPPED: does not fit)"
			}
		}
		var warn []string
		if erratum.MatchString(title) {
			warn = append(warn, "ERRATUM, not loaded")
		}
		if len(s.Articles) == 0 {
			warn = append(warn, "NO ARTICLES")
		}
		if s.Closing == "" {
			warn = append(warn, "NO CLOSING")
		}
		if m.Agreement < 0.95 {
			warn = append(warn, "READERS AGREE < 95%")
		}
		for _, a := range s.Articles {
			if a.Note != "" {
				warn = append(warn, "GAP after article "+a.Number)
			}
		}
		if len(s.Sections) > 0 {
			warn = append(warn, fmt.Sprintf("%d headings", len(s.Sections)))
		}
		if len(nums) > 12 {
			nums = append(append(nums[:6:6], "…"), nums[len(nums)-3:]...)
		}
		fmt.Fprintf(w, "%d  %s %s (%s)  %s\n", m.ID, m.Kind, m.Number, m.Date, strings.Join(warn, "; "))
		fmt.Fprintf(w, "    title    %s\n", cutRunes(title, 90))
		fmt.Fprintf(w, "    gazette  issue %s, %s, pages %d-%d | readers agree %.1f%%, %d spans differ\n",
			gnum, gdate, s.PageFrom, s.PageTo, 100*m.Agreement, m.Flagged)
		fmt.Fprintf(w, "    articles %d %v\n", len(s.Articles), nums)
		fmt.Fprintf(w, "    preamble %q\n", cutRunes(shown(s.Preamble), 70))
		fmt.Fprintf(w, "    closing  %q\n\n", cutRunes(shown(s.Closing), 90))
	}
	return nil
}

// shown writes a disputed span as ‹reading one|reading two›.
var shown = strings.NewReplacer("\uE000", "‹", "\uE001", "|", "\uE002", "›").Replace
