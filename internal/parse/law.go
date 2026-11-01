// Package parse turns the Centre's raw pages into rows. One file per page
// type; every parser is a pure function of the page's bytes.
package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/taktekhq/lebanese-legislation/internal/page"
	"github.com/taktekhq/lebanese-legislation/internal/text"
)

// Law is Law.aspx: a text's identity, status and publication. Its articles
// come from LawView.aspx (see LawText).
type Law struct {
	ID              int
	Kind            string
	Number          *string
	NumberInt       *int
	IssuedOn        *time.Time
	Title           string
	Repealed        bool
	GazetteNumber   *string
	GazetteDate     *time.Time
	GazettePages    *string
	Preamble        *string
	ExplanatoryNote *string
	RepealedBy      *Ref // the text that repealed this one, when the page names it
}

// Ref is a link from one document to a text, with the words that name it.
type Ref struct {
	LawID int
	Label string
}

var (
	kindRe   = regexp.MustCompile(`نوع النص\s*:\s*([^|]+?)\s*\|`)
	numberRe = regexp.MustCompile(`رقم\s*(.*?)\s*تاريخ`)
	lawHref  = regexp.MustCompile(`(?i)(?:^|/)Law(?:\.aspx\?lawId=|/)(\d+)`)
	repealed = regexp.MustCompile(`\(?\s*ملغى\s*\)?\s*$`)
)

// ParseLaw reads a Law.aspx page.
func ParseLaw(id int, raw []byte) (*Law, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, err
	}
	l := &Law{ID: id}

	// A repealed text carries a red ملغى after its title and after its date.
	titleNode, sub := d.ID("MainContent_spanTitle"), d.ID("MainContent_subdetails")
	l.Repealed = hasRedMark(titleNode) || hasRedMark(sub)
	l.Title = strings.TrimSpace(repealed.ReplaceAllString(page.Inline(titleNode), ""))

	details := page.Inline(sub)
	if m := kindRe.FindStringSubmatch(details); m != nil {
		l.Kind = strings.TrimSpace(m[1])
	}
	if m := numberRe.FindStringSubmatch(details); m != nil {
		// The Centre prints رقم 0 for a text without a number (the codes of
		// the 1930s, for one): that is no number, not number zero.
		if n := strings.TrimSpace(m[1]); n != "0" {
			l.Number = text.Opt(n)
		}
		if l.Number != nil {
			l.NumberInt = text.LeadingInt(*l.Number)
		}
	}
	if i := strings.Index(details, "تاريخ"); i >= 0 {
		l.IssuedOn = text.DMY(details[i:])
	}

	l.GazetteNumber = text.Opt(d.Text("MainContent_divOJNumber"))
	l.GazetteDate = text.DMY(d.Text("MainContent_divOJPublishDate"))
	l.GazettePages = text.Opt(d.Text("MainContent_divOJPage"))
	l.Preamble = paras(d.ID("MainContent_divNotes"))
	l.ExplanatoryNote = paras(d.ID("MainContent_divNotes2"))

	// On a repealed text, this block names the text that repealed it.
	if a := d.ID("MainContent_cancellaw_cancellawLink"); a != nil {
		if m := lawHref.FindStringSubmatch(page.Attr(a, "href")); m != nil {
			to, _ := strconv.Atoi(m[1])
			label := strings.Join(nonEmpty(
				d.Text("MainContent_cancellaw_lblClassification"),
				prefixed("رقم ", d.Text("MainContent_cancellaw_lblNumber")),
				prefixed("سنة ", d.Text("MainContent_cancellaw_lblYear")),
				prefixed("تاريخ ", d.Text("MainContent_cancellaw_lblProductionDate")),
			), " ")
			if name := page.Inline(a); name != "" {
				label = strings.TrimSpace(label + " — " + name)
			}
			l.RepealedBy = &Ref{LawID: to, Label: label}
		}
	}
	if l.Kind == "" {
		l.Kind = "غير محدد" // the page names no kind
	}
	return l, nil
}

// hasRedMark finds the Centre's red ملغى span (color #c41e32) under n.
func hasRedMark(n *html.Node) bool {
	found := false
	page.Walk(n, func(c *html.Node) bool {
		if c.Type == html.ElementNode && strings.Contains(strings.ToLower(page.Attr(c, "style")), "c41e32") &&
			strings.Contains(page.Inline(c), "ملغى") {
			found = true
		}
		return !found
	})
	return found
}

func paras(n *html.Node) *string {
	if n == nil {
		return nil
	}
	var ps []string
	for _, p := range page.Paragraphs(n, nil) {
		ps = append(ps, p.Text)
	}
	return text.Opt(page.Join(ps))
}

func prefixed(p, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return p + v
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
