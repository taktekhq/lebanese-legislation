package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Texts the Lebanese University has not published yet, read from scans of
// the Gazette's own pages (lebgazette sader-bundle: one folder per text with
// meta.json, text.json and the page images). The Centre's text always comes
// first: a scan whose kind, number and year match a text the Centre holds is
// not loaded, and its address is kept as an alias of the Centre's.

// ScanIDBase starts the ids of scanned texts, far above the Centre's LawIDs.
// A scan keeps its id while it is shown: ScanIDBase + its id at the source.
const ScanIDBase = 2_000_000_000

type scanMeta struct {
	ID        int     `json:"id"`
	Source    string  `json:"source"`
	SourceURL string  `json:"source_url"`
	Kind      string  `json:"kind"`
	Number    string  `json:"number"`
	Date      string  `json:"date"`
	Year      int     `json:"year"`
	Title     string  `json:"title"`
	Pages     int     `json:"pages"`
	Issue     string  `json:"gazette_issue"`
	IssueDate string  `json:"gazette_issue_date"`
	PageFrom  int     `json:"gazette_page_from"`
	PageTo    int     `json:"gazette_page_to"`
	Flagged   int     `json:"flagged_spans"`
	Agreement float64 `json:"agreement"`
	Status    string  `json:"status"`
}

var (
	listedTitle = regexp.MustCompile(`^\S+(?:\s\S+)?\s+رقم\s+\S+\s+صادر\s+بتاريخ\s+\d{1,2}/\d{1,2}/\d{4}\s*`)
	erratum     = regexp.MustCompile(`^\s*(?:تصحيح|استدراك)`)
)

// scanTitle drops the listing's "قانون رقم 27 صادر بتاريخ 05/01/2026 " prefix.
func scanTitle(listed string) string {
	if t := strings.TrimSpace(listedTitle.ReplaceAllString(listed, "")); t != "" {
		return t
	}
	return strings.TrimSpace(listed)
}

var scanCols = []string{"law_id", "source", "source_id", "source_url", "pages", "agreement", "flagged"}
var aliasCols = []string{"id", "law_id"}

func (b *builder) scans() error {
	if b.Scans == "" {
		return nil
	}
	metas, err := filepath.Glob(filepath.Join(b.Scans, "*", "meta.json"))
	if err != nil {
		return err
	}
	sort.Strings(metas)
	// The Centre's texts by kind, number and year.
	centre := map[string]int{}
	rows, err := b.conn.Query(b.ctx, `SELECT id, kind, number_int, extract(year FROM issued_on)::int
		FROM legislation WHERE number_int IS NOT NULL AND issued_on IS NOT NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, n, y int
		var kind string
		if err := rows.Scan(&id, &kind, &n, &y); err != nil {
			rows.Close()
			return err
		}
		centre[fmt.Sprintf("%s|%d|%d", kind, n, y)] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	issues := readIssueIndex(filepath.Join(b.Scans, "issues.tsv"))
	r := newRows()
	seen := map[string]int{}
	for _, f := range metas {
		var m scanMeta
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if m.Status != "official-scan" || m.Kind == "" || m.Number == "" {
			b.rep.note("scan %d skipped: status %q, kind %q, number %q", m.ID, m.Status, m.Kind, m.Number)
			continue
		}
		title := scanTitle(m.Title)
		if erratum.MatchString(title) { // a correction notice is not the text it corrects
			b.rep.note("scan %d skipped: an erratum (%s)", m.ID, cutRunes(title, 60))
			continue
		}
		num, err := strconv.Atoi(m.Number)
		if err != nil {
			b.rep.note("scan %d skipped: number %q", m.ID, m.Number)
			continue
		}
		issued, err := time.Parse("2006-01-02", m.Date)
		if err != nil {
			b.rep.note("scan %d skipped: date %q", m.ID, m.Date)
			continue
		}
		id := ScanIDBase + m.ID
		key := fmt.Sprintf("%s|%d|%d", m.Kind, num, issued.Year())
		if c, ok := centre[key]; ok {
			r.add("legislation_alias", aliasCols, id, c)
			b.rep.ScansCentre++
			continue
		}
		if other, ok := seen[key]; ok {
			b.rep.note("scan %d skipped: same kind, number and year as scan %d", m.ID, other)
			continue
		}
		seen[key] = m.ID

		raw, err = os.ReadFile(filepath.Join(filepath.Dir(f), "text.json"))
		if err != nil {
			return err
		}
		st, err := parseScanFile(raw, title)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		// Held back until someone looks: nothing recognisable as a text, or
		// readers that disagree on more than a word in ten.
		if len(st.Articles) == 0 && st.Closing == "" {
			b.rep.note("scan %d held back: no article and no closing found", m.ID)
			continue
		}
		if m.Agreement < 0.90 {
			b.rep.note("scan %d held back: the readers agree on %.0f%% of the words", m.ID, 100*m.Agreement)
			continue
		}

		if err := b.ensure("legislation_kind", "kind", m.Kind, b.kinds); err != nil {
			return err
		}
		gnum, gdate, gpages := st.GazetteNumber, st.GazetteDate, ""
		if m.Issue != "" {
			gnum = m.Issue
		}
		if d, err := time.Parse("2006-01-02", m.IssueDate); err == nil {
			gdate = &d
		}
		// The issue's date as PCM lists it beats a date read off the page.
		if d, ok := issues.date(gnum, issued); ok {
			gdate = &d
		}
		if gdate != nil && (gdate.Before(issued) || gdate.After(issued.AddDate(0, 6, 0))) {
			b.rep.note("scan %d: Gazette date %s does not fit a text of %s; left out", m.ID, gdate.Format("2006-01-02"), m.Date)
			gdate = nil
		}
		from, to := st.PageFrom, st.PageTo
		if m.PageFrom > 0 {
			from, to = m.PageFrom, m.PageTo
		}
		switch {
		case from > 0 && to > from:
			gpages = fmt.Sprintf("%d-%d", from, to)
		case from > 0:
			gpages = strconv.Itoa(from)
		}
		r.add("legislation", lawCols, id, m.Kind, m.Number, num, issued, title, "in_force",
			nullable(gnum), gdate, nullable(gpages), nullable(st.Preamble), nullable(st.ExplanatoryNote), nullable(st.Closing))
		b.laws[id] = true
		r.add("legislation_scan", scanCols, id, m.Source, m.ID, m.SourceURL, m.Pages, m.Agreement, m.Flagged)
		// Headings: ids below zero, so they never meet the Centre's
		// LawTreeSectionIDs; parent is the nearest heading above it.
		secIDs := make([]int, len(st.Sections))
		for k, sec := range st.Sections {
			secIDs[k] = -(m.ID*1000 + k + 1)
			var parent any
			for j := k - 1; j >= 0; j-- {
				if st.Sections[j].Depth < sec.Depth {
					parent = secIDs[j]
					break
				}
			}
			r.add("legislation_section", sectionCols, secIDs[k], id, parent, k+1, sec.Depth, sec.Heading, nil)
		}
		numbers := scanNumbers(st.Articles)
		for i, a := range st.Articles {
			n := numbers[i]
			var ni any
			if v, err := strconv.Atoi(strings.Fields(n)[0]); err == nil {
				ni = v
			} else if n == "وحيدة" {
				ni = 1
			}
			var sec any
			if a.Section >= 0 && a.Section < len(secIDs) {
				sec = secIDs[a.Section]
			}
			r.add("legislation_article", articleCols, id, i+1, nil, sec, n, ni, nullable(a.Note), a.Text, nil, nil,
				SourceSaderlex, strconv.Itoa(m.ID))
		}
		b.rep.Scans++
		b.rep.ScanArticles += len(st.Articles)
	}
	return b.flush(r, "legislation", "legislation_scan", "legislation_alias", "legislation_section", "legislation_article")
}

// parseScanFile reads a bundle text's text.json (its pages' columns) into
// its parts, the way scans loads them.
func parseScanFile(raw []byte, title string) (ScanText, error) {
	var tx struct {
		Pages []struct {
			Cols []string `json:"cols"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return ScanText{}, err
	}
	var pages [][]string
	for _, p := range tx.Pages {
		pages = append(pages, p.Cols)
	}
	return ParseScanText(pages, title), nil
}

// scanNumbers gives each article the number it is stored under: a number
// printed twice keeps its own address ("5", then "5 (2)").
func scanNumbers(arts []ScanArticle) []string {
	out := make([]string, len(arts))
	used := map[string]bool{}
	for i, a := range arts {
		n := a.Number
		for k := 2; used[n]; k++ {
			n = fmt.Sprintf("%s (%d)", a.Number, k)
		}
		used[n] = true
		out[i] = n
	}
	return out
}

// ScanArticles reads one bundle text from its meta.json and text.json into
// its articles' numbers and wordings, exactly as scans stores them. The
// wording guard (internal/provenance) reads a scanned text's source through
// it, so both sides cut the pages the same way.
func ScanArticles(metaRaw, textRaw []byte) (numbers, texts []string, err error) {
	var m scanMeta
	if err := json.Unmarshal(metaRaw, &m); err != nil {
		return nil, nil, fmt.Errorf("meta.json: %w", err)
	}
	st, err := parseScanFile(textRaw, scanTitle(m.Title))
	if err != nil {
		return nil, nil, fmt.Errorf("text.json: %w", err)
	}
	for _, a := range st.Articles {
		texts = append(texts, a.Text)
	}
	return scanNumbers(st.Articles), texts, nil
}

func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// issueIndex is PCM's list of Gazette issues (lebgazette index/issues.tsv:
// year, number, date, …), copied into the bundle folder.
type issueIndex map[string]time.Time

func readIssueIndex(path string) issueIndex {
	ix := issueIndex{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ix
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) < 3 {
			continue
		}
		if d, err := time.Parse("2006-01-02", f[2]); err == nil {
			ix[f[0]+"|"+f[1]] = d
		}
	}
	return ix
}

// date finds issue number's date in the year the text was issued or the
// next, the first one not before the text itself.
func (ix issueIndex) date(number string, issued time.Time) (time.Time, bool) {
	if number == "" {
		return time.Time{}, false
	}
	for _, y := range []int{issued.Year(), issued.Year() + 1} {
		for _, n := range []string{number, number + "م"} {
			if d, ok := ix[fmt.Sprintf("%d|%s", y, n)]; ok && !d.Before(issued) {
				return d, true
			}
		}
	}
	return time.Time{}, false
}
