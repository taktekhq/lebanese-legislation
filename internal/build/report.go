package build

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/modelout"
)

// Report is what a build found, for checking before anything is published.
type Report struct {
	Took               time.Duration
	Laws               int
	Repealed           int
	WithText           int
	Articles           int
	EmptyArticles      int
	Repeats            int      // verbatim repeats on the Centre's pages, dropped
	ImageArticles      int      // articles the Centre has only as pictures
	CountMatch         int      // texts whose parsed articles equal the page's own count
	CountMismatch      [][3]int // law id, printed count, parsed count
	NoArticles         []int    // texts with a full-text page but no article found
	Scans              int      // texts read from Gazette scans (scans.go)
	ScanArticles       int
	ScansCentre        int // scans left out because the Centre has the text
	ScanIssuesNew      int // Gazette issues made for scanned texts (scanissues.go)
	ScanIssuesLinked   int // scanned texts' issues the Centre already had
	ScanIssueTexts     int // scanned texts given an issue
	RulingFiles        int // rulings whose scanned judgment (PDF) is held (internal/modelout)
	RulingsRead        int // of those, rulings with a machine reading
	RulingPages        int // pages of text in those readings
	DateFixes          int // dates corrected from db/date_corrections.tsv
	DateGuard          int // dates set to unknown by the guard (signed after publication, or in the future)
	Codes              int
	Amendments         map[string]int // by status
	Versions           int            // article versions written
	NewerThanCentre    int            // amended articles whose latest version the Centre's text doesn't show
	Loaded             map[string]int
	Blank              map[string]int
	Refs               map[string]int
	Graph              map[string]int       // graph edges by source and status
	RulingArticleLinks int                  // linked ruling-to-article graph edges rendered on ruling pages
	ArtNum             modelout.ArtNumStats // what the one-spelling fold did to the graph (internal/modelout)
	ArtCheck           ArtNumCheck          // what is left of other spellings, after the build
	Tables             map[string]int64
	Notes              []string
	Only               string // the step an -only rerun ran: it gives no publishing verdict
}

func newReport() *Report {
	return &Report{Loaded: map[string]int{}, Blank: map[string]int{}, Refs: map[string]int{}, Amendments: map[string]int{}}
}

func (r *Report) note(format string, args ...any) {
	if len(r.Notes) < 200 {
		r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
	}
}

func (r *Report) count(ctx context.Context, conn *pgx.Conn) error {
	r.Tables = map[string]int64{}
	rows, err := conn.Query(ctx, `SELECT relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY relname`)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, t := range names {
		var n int64
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return err
		}
		r.Tables[t] = n
	}
	if _, ok := r.Tables["graph_edge"]; ok {
		query := `SELECT count(*) FROM (
			SELECT DISTINCT e.from_id, e.to_law_id, e.to_article FROM graph_edge e JOIN legislation_article a
			  ON a.law_id = e.to_law_id AND a.number = e.to_article
			WHERE e.from_type = 'ruling' AND e.status = 'linked' AND e.to_article IS NOT NULL
		) links`
		if err := conn.QueryRow(ctx, query).Scan(&r.RulingArticleLinks); err != nil {
			if !strings.Contains(err.Error(), `relation "legislation_article" does not exist`) {
				return err
			}
			r.RulingArticleLinks = 0
		}
	}
	r.ArtCheck, err = checkArticleNumbers(ctx, conn)
	return err
}

// OK is the gate for publishing: every full text parsed into articles, and
// nearly all of them agreeing with the page's own article count, and no
// article number spelled any way but the Centre's (edges naming an article
// their text lacks are reported only: some texts are not in full).
func (r *Report) OK() bool {
	if r.WithText == 0 || r.ArtCheck.Total() > 0 {
		return false
	}
	agree := float64(r.CountMatch) / float64(r.WithText)
	return agree >= 0.98 && r.Tables["legislation"] > 0
}

func (r *Report) reason() string {
	if r.WithText == 0 {
		return "no full texts were built"
	}
	if r.ArtCheck.Total() > 0 {
		return fmt.Sprintf("%d article numbers are not spelled the Centre's way", r.ArtCheck.Total())
	}
	if r.Tables["legislation"] == 0 {
		return "no legislation rows were built"
	}
	if float64(r.CountMatch)/float64(r.WithText) < 0.98 {
		return fmt.Sprintf("article counts agree for %d of %d full texts (%.2f%%; need at least 98%%)",
			r.CountMatch, r.WithText, 100*float64(r.CountMatch)/float64(r.WithText))
	}
	return "build checks did not pass"
}

// Write prints the report.
func (r *Report) Write(w io.Writer) {
	fmt.Fprintf(w, "build took %s\n\n", r.Took.Round(time.Second))
	fmt.Fprintf(w, "texts            %8d  (%d repealed)\n", r.Laws, r.Repealed)
	fmt.Fprintf(w, "with full text   %8d\n", r.WithText)
	fmt.Fprintf(w, "articles         %8d  (%d only as pictures, %d empty with no note, %d verbatim repeats dropped)\n",
		r.Articles, r.ImageArticles, r.EmptyArticles, r.Repeats)
	if r.WithText > 0 {
		fmt.Fprintf(w, "article count agrees with the page on %d of %d texts (%.2f%%)\n", r.CountMatch, r.WithText,
			100*float64(r.CountMatch)/float64(r.WithText))
	}
	if n := len(r.CountMismatch); n > 0 {
		fmt.Fprintf(w, "  disagreements (%d), first ones: law printed parsed\n", n)
		for _, m := range r.CountMismatch[:min(n, 15)] {
			fmt.Fprintf(w, "    %d %d %d\n", m[0], m[1], m[2])
		}
	}
	if n := len(r.NoArticles); n > 0 {
		fmt.Fprintf(w, "  full text but no articles (%d): %v\n", n, r.NoArticles[:min(n, 15)])
	}
	if r.Scans > 0 || r.ScansCentre > 0 {
		fmt.Fprintf(w, "from Gazette scans %6d  (%d articles; %d left out, the Centre has them)\n", r.Scans, r.ScanArticles, r.ScansCentre)
	}
	if r.ScanIssueTexts > 0 {
		fmt.Fprintf(w, "scan Gazette issues %4d  new, %d the Centre had (%d texts)\n", r.ScanIssuesNew, r.ScanIssuesLinked, r.ScanIssueTexts)
	}
	if r.RulingFiles > 0 || r.RulingsRead > 0 {
		fmt.Fprintf(w, "ruling judgments %7d  scanned (%d read by machine: %d pages)\n", r.RulingFiles, r.RulingsRead, r.RulingPages)
	}
	_, _ = fmt.Fprintf(w, "ruling-to-article links rendered %d\n", r.RulingArticleLinks)
	fmt.Fprintf(w, "dates corrected  %8d  (%d more set to unknown by the guard)\n", r.DateFixes, r.DateGuard)
	fmt.Fprintf(w, "codes resolved   %8d\n", r.Codes)
	fmt.Fprintf(w, "amendments       %8d applied, %d partial, %d to review, %d rejected\n", r.Amendments["applied"],
		r.Amendments["partial"], r.Amendments["review"], r.Amendments["rejected"])
	fmt.Fprintf(w, "article versions %8d (%d articles newer than the Centre's text)\n\n", r.Versions, r.NewerThanCentre)
	if len(r.Graph) > 0 {
		fmt.Fprintln(w, "graph edges, by source and status")
		for _, k := range keys(r.Graph) {
			fmt.Fprintf(w, "  %-22s %8d\n", k, r.Graph[k])
		}
		fmt.Fprintln(w)
	}
	if a := r.ArtNum; a != (modelout.ArtNumStats{}) {
		fmt.Fprintf(w, "article numbers in the graph: %d respelled, %d lists split, %d kept as a part of the article, %d unreadable (words kept as the part), %d merged (%d of them identical before the fold)\n",
			a.Folded, a.Split, a.Parts, a.Unreadable, a.Merged, a.MergedPlain)
	}
	if r.ArtCheck.Checked {
		fmt.Fprintf(w, "article numbers not spelled the Centre's way: %d", r.ArtCheck.Total())
		for _, k := range keys(r.ArtCheck.NonCanonical) {
			fmt.Fprintf(w, "  %s=%d", k, r.ArtCheck.NonCanonical[k])
		}
		fmt.Fprintf(w, "\nedge articles the named text does not have: %d (reported only)\n\n", r.ArtCheck.NotFound)
	}
	fmt.Fprintln(w, "loaded / skipped as blank")
	for _, k := range keys(r.Loaded) {
		fmt.Fprintf(w, "  %-12s %8d / %d\n", k, r.Loaded[k], r.Blank[k])
	}
	fmt.Fprintln(w, "\nreferences")
	for _, k := range keys(r.Refs) {
		fmt.Fprintf(w, "  %-45s %8d\n", k, r.Refs[k])
	}
	fmt.Fprintln(w, "\ntables")
	for _, k := range keys64(r.Tables) {
		fmt.Fprintf(w, "  %-22s %10d\n", k, r.Tables[k])
	}
	if len(r.Notes) > 0 {
		fmt.Fprintf(w, "\nnotes (%d)\n  %s\n", len(r.Notes), strings.Join(r.Notes[:min(len(r.Notes), 30)], "\n  "))
	}
	// An -only rerun counts no texts, so OK() would say false. The nightly
	// appends these reruns to the full build's report and the gate reads the
	// last publishable: line, so a rerun must not write one (28 Oct 2026:
	// the nightly's -only indexing turned a publishable build into a held one).
	if r.Only != "" {
		_, _ = fmt.Fprintf(w, "\n-only %s rerun: no publishing verdict\n", r.Only)
		return
	}
	ok := r.OK()
	_, _ = fmt.Fprintf(w, "\npublishable: %v\n", ok)
	if !ok {
		_, _ = fmt.Fprintf(w, "not publishable because: %s\n", r.reason())
	}
}

func keys(m map[string]int) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func keys64(m map[string]int64) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
