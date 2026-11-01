package parse

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/taktekhq/lebanese-legislation/internal/page"
	"github.com/taktekhq/lebanese-legislation/internal/text"
)

// Cite is a link the Centre's editors made from a document to a text or to
// one of its articles.
type Cite struct {
	LawID           int
	ArticleCentreID *int
	ArticleNumber   *string
	Label           string
}

type Ruling struct {
	ID        int
	Court     *string
	Number    *string
	Year      *int
	DecidedOn *time.Time
	Title     string
	President *string
	Members   []string
	TextAr    string
	Cites     []Cite
}

type Opinion struct {
	ID         int
	Number     *string
	Year       *int
	IssuedOn   *time.Time
	Requester  *string
	FileNumber *string
	FileYear   *int
	Subject    *string
	TextAr     *string
	Cites      []Cite
}

type Agreement struct {
	ID             int
	Title          string
	Scope          *string // bilateral | international
	SignedOn       *time.Time
	RatifiedBy     *string
	RatifyingLawID *int
	TextAr         *string
}

type Doctrine struct {
	ID       int
	Title    string
	Authors  []string
	Series   *string
	Pages    *string
	Abstract *string
	Cites    []Cite
}

type Research struct {
	ID          int
	Title       string
	Author      *string
	PublishedOn *time.Time
	TextAr      *string
	Cites       []Cite
}

// GazetteIssue is OfficialJournal.aspx: an issue's contents. The page never
// prints the issue's own number or date; they are read off the texts it lists.
type GazetteIssue struct {
	ID      int
	Entries []GazetteEntry
}

type GazetteEntry struct {
	LawID int
	Pages *string
}

var (
	articleLink = regexp.MustCompile(`(?i)LawArticles\.aspx\?LawArticleID=(\d+)&(?:amp;)?LawId=(\d+)`)
	lawLink     = regexp.MustCompile(`(?i)Law(?:\.aspx\?lawId=|/)(\d+)`)
	citedNumber = regexp.MustCompile(`مادة\s*(?:رقم)?\s*([0-9]+(?:\s*مكرر)?)`)
	unavailable = "غير متوفر"
	rulingFile  = regexp.MustCompile(`RulingFile\.aspx\?RuliID=([0-9]+)`)
)

// ParseRuling reads ViewRulePage.aspx. Some real rulings have no summary
// text, only the judgment's file; ok is false only for the Centre's blank
// records, which have neither text, nor a year and date, nor a link to their
// own judgment's file. 42 records give year 0 and 01/01/1900 with no text but
// link their PDF, which we hold and read.
func ParseRuling(id int, raw []byte) (r *Ruling, ok bool, err error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	r = &Ruling{ID: id,
		Title:     d.Text("MainContent_lblTitle"),
		Court:     text.Opt(d.Text("MainContent_lblcourtName")),
		Number:    nonZero(d.Text("MainContent_lblNumber")),
		Year:      text.Year(d.Text("MainContent_lblyear")),
		DecidedOn: text.DMY(d.Text("MainContent_lblDate")),
		President: text.Opt(d.Text("MainContent_lblJudge")),
		Members:   names(d.Text("MainContent_lblMembers")),
		TextAr:    body(d.ID("MainContent_RulingText")),
	}
	r.Cites = cites(d.Root)
	return r, r.TextAr != "" || (r.Year != nil && r.DecidedOn != nil) || linksOwnFile(id, raw), nil
}

// linksOwnFile says whether a ruling page links the judgment file of the same
// ruling (RulingFile.aspx?RuliID=<id>), as check-rulings' file-link rule reads it.
func linksOwnFile(id int, raw []byte) bool {
	for _, m := range rulingFile.FindAllSubmatch(raw, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n == id {
			return true
		}
	}
	return false
}

// ParseOpinion reads ViewOpinionPage.aspx (its date is month first).
func ParseOpinion(id int, raw []byte) (*Opinion, bool, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	o := &Opinion{ID: id,
		Number:     nonZero(d.Text("MainContent_lbl_opinion_number")),
		Year:       text.Year(d.Text("MainContent_lbl_opinion_year")),
		IssuedOn:   text.MDY(d.Text("MainContent_lbl_opinion_date")),
		Requester:  text.Opt(d.Text("MainContent_lbl_opinion_requester")),
		FileNumber: nonZero(d.Text("MainContent_lbl_foundation_numb")),
		FileYear:   text.Year(d.Text("MainContent_lbl_foundation_year")),
		Subject:    text.Opt(body(d.ID("MainContent_TitleText"))),
		TextAr:     text.Opt(body(d.ID("MainContent_OpinionText"))),
	}
	o.Cites = cites(d.Root)
	return o, o.TextAr != nil || o.Subject != nil, nil
}

// ParseAgreement reads ViewAgreementPage.aspx. About one in ten are blank
// records titled «عنوان الاتفاقية غير متوفر» with no text.
func ParseAgreement(id int, raw []byte) (*Agreement, bool, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	a := &Agreement{ID: id,
		Title:      d.Text("MainContent_lblTitle"),
		SignedOn:   text.DMY(d.Text("MainContent_lblProductionDate")),
		RatifiedBy: text.Opt(d.Text("MainContent_lblLegislationTool")),
		TextAr:     text.Opt(body(d.ID("MainContent_AgreementText"))),
	}
	switch strings.TrimSpace(d.Text("MainContent_lblType")) {
	case "ثنائية":
		s := "bilateral"
		a.Scope = &s
	case "دولية":
		s := "international"
		a.Scope = &s
	}
	if m := lawLink.FindStringSubmatch(page.Attr(d.ID("MainContent_law_Link"), "href")); m != nil {
		v, _ := strconv.Atoi(m[1])
		a.RatifyingLawID = &v
	}
	if a.RatifiedBy != nil && strings.Contains(*a.RatifiedBy, "رقم 0 سنة 0") {
		a.RatifiedBy = nil
	}
	blank := strings.Contains(a.Title, unavailable) && a.TextAr == nil
	return a, !blank && a.Title != "", nil
}

// ParseDoctrine reads RefPageDetails.aspx, which reuses other pages' element
// ids: lblNumber holds the authors, RulingText the abstract.
func ParseDoctrine(id int, raw []byte) (*Doctrine, bool, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	x := &Doctrine{ID: id,
		Title:    d.Text("MainContent_lblTitle"),
		Authors:  names(d.Text("MainContent_lblNumber")),
		Series:   text.Opt(d.Text("MainContent_lblseqtitle")),
		Pages:    text.Opt(d.Text("MainContent_Label1")),
		Abstract: text.Opt(body(d.ID("MainContent_RulingText"))),
	}
	x.Cites = cites(d.Root)
	return x, x.Title != "", nil
}

// ParseResearch reads ViewResearchPage.aspx (RulingText holds the study).
func ParseResearch(id int, raw []byte) (*Research, bool, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	x := &Research{ID: id,
		Title:       d.Text("MainContent_lblTitle"),
		Author:      text.Opt(d.Text("MainContent_lblpublisher")),
		PublishedOn: text.DMY(d.Text("MainContent_lbldate")),
		TextAr:      text.Opt(body(d.ID("MainContent_RulingText"))),
	}
	x.Cites = cites(d.ID("MainContent_RulingText"))
	return x, x.Title != "", nil
}

// ParseGazette reads OfficialJournal.aspx: rows of (text, page).
func ParseGazette(id int, raw []byte) (*GazetteIssue, bool, error) {
	d, err := page.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	g := &GazetteIssue{ID: id}
	seen := map[int]bool{}
	page.Walk(d.ID("MainContent_results"), func(n *html.Node) bool {
		if !(page.Is(n, atom.Div) && strings.Contains(page.Attr(n, "class"), "tbl-row")) {
			return true
		}
		var law int
		var pages string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			cls := page.Attr(c, "class")
			page.Walk(c, func(a *html.Node) bool {
				if page.Is(a, atom.A) {
					if m := lawLink.FindStringSubmatch(page.Attr(a, "href")); m != nil {
						law, _ = strconv.Atoi(m[1])
					}
				}
				return true
			})
			if strings.Contains(cls, "second") {
				pages = page.Inline(c)
			}
		}
		if law != 0 && !seen[law] {
			seen[law] = true
			g.Entries = append(g.Entries, GazetteEntry{LawID: law, Pages: text.Opt(pages)})
		}
		return false
	})
	return g, len(g.Entries) > 0, nil
}

// cites collects the editors' links to texts and articles under n: the
// cited-laws list on rulings and opinions, and links inside a study's text.
func cites(n *html.Node) []Cite {
	var out []Cite
	seen := map[string]bool{}
	page.Walk(n, func(a *html.Node) bool {
		if !page.Is(a, atom.A) {
			return true
		}
		href := page.Attr(a, "href")
		label := page.Inline(a)
		var c Cite
		if m := articleLink.FindStringSubmatch(href); m != nil {
			art, _ := strconv.Atoi(m[1])
			c.LawID, _ = strconv.Atoi(m[2])
			c.ArticleCentreID = &art
			if n := citedNumber.FindStringSubmatch(label); n != nil {
				c.ArticleNumber = text.Opt(strings.Join(strings.Fields(n[1]), " "))
			}
		} else if m := lawLink.FindStringSubmatch(href); m != nil && strings.Contains(strings.ToLower(href), "law") &&
			!strings.Contains(strings.ToLower(href), "related") {
			c.LawID, _ = strconv.Atoi(m[1])
		} else {
			return true
		}
		c.Label = label
		key := strconv.Itoa(c.LawID) + "|" + label
		if c.ArticleCentreID != nil {
			key += "|" + strconv.Itoa(*c.ArticleCentreID)
		}
		if c.LawID != 0 && !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
		return false
	})
	return out
}

// body is an element's text as paragraphs.
func body(n *html.Node) string {
	if n == nil {
		return ""
	}
	var ps []string
	for _, p := range page.Paragraphs(n, nil) {
		ps = append(ps, p.Text)
	}
	return page.Join(ps)
}

// names splits a list of people as the Centre prints it: "a - b" for two,
// "/a//b//c/" for three or more, "a /b/ c" for authors.
func names(s string) []string {
	s = strings.ReplaceAll(s, " - ", "/")
	var out []string
	for _, p := range strings.Split(s, "/") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func nonZero(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return nil
	}
	return &s
}
