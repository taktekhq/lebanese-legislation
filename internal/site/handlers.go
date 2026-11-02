package site

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

const perPage = 40

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(w, `{"status":"ok","build":%q}`, BuildID)
	})
	m.HandleFunc("GET /{$}", s.home)
	if s.isStatic {
		// no server to filter or search: the lists are browsed by year and
		// search runs in the browser over the titles
		m.HandleFunc("GET /laws/{$}", s.yearIndex("laws"))
		m.HandleFunc("GET /rulings/{$}", s.yearIndex("rulings"))
		m.HandleFunc("GET /gazette/{$}", s.yearIndex("gazette"))
		m.HandleFunc("GET /search/{$}", s.static("search_static", "Search · Lebanese Legislation",
			"Find any Lebanese law by its name, or by its number and year."))
	} else {
		m.HandleFunc("GET /laws/{$}", s.laws)
		m.HandleFunc("GET /rulings/{$}", s.rulings)
		m.HandleFunc("GET /gazette/{$}", s.gazettes)
		m.HandleFunc("GET /search/{$}", s.search)
		m.HandleFunc("POST /search/{$}", s.search)
		m.HandleFunc("POST /search/answer/{$}", s.answer)
		m.HandleFunc("POST /search/ai/{$}", s.aiAnswer)
		m.HandleFunc("POST /search/ai/deep/{$}", s.aiDeepAnswer)
	}
	m.HandleFunc("GET /ul/uploads/images/{name...}", s.ulImage)
	m.HandleFunc("GET /scan/{id}/{file}", s.scanFile)
	s.seoRoutes()
	s.apiRoutes()
	s.mcpRoutes()
	for _, k := range []string{"laws", "rulings", "gazette"} {
		m.HandleFunc("GET /"+k+"/years/{$}", s.yearIndex(k))
	}
	m.HandleFunc("GET /laws/year/{year}/{$}", s.lawsYear)
	m.HandleFunc("GET /laws/year/{year}/page/{page}/{$}", s.lawsYear)
	m.HandleFunc("GET /rulings/year/{year}/{$}", s.docYear("rulings"))
	m.HandleFunc("GET /rulings/year/{year}/page/{page}/{$}", s.docYear("rulings"))
	m.HandleFunc("GET /gazette/year/{year}/{$}", s.docYear("gazette"))
	m.HandleFunc("GET /gazette/year/{year}/page/{page}/{$}", s.docYear("gazette"))
	m.HandleFunc("GET /codes/{$}", s.codes)
	m.HandleFunc("GET /changes/{$}", s.recentChanges)
	m.HandleFunc("GET /changes/feed.xml", s.changesFeed)
	for _, word := range addressWords {
		m.HandleFunc("GET /"+word+"/{id}/feed.xml", s.lawFeed)
		m.HandleFunc("GET /"+word+"/{id}/{$}", s.law)
		m.HandleFunc("GET /"+word+"/{id}/part/{part}/{$}", s.lawPart)
		m.HandleFunc("GET /"+word+"/{id}/article/{n}/{$}", s.article)
		m.HandleFunc("GET /"+word+"/{id}/article/{n}/rulings/{$}", s.articleRulings)
		m.HandleFunc("GET /"+word+"/{id}/go/{$}", s.gotoArticle)
	}
	m.HandleFunc("GET /changes/review/{$}", s.review)
	m.HandleFunc("GET /code/{slug}/{$}", s.code)
	m.HandleFunc("GET /ruling/{id}/{$}", s.ruling)
	m.HandleFunc("GET /gazette/{id}/{$}", s.gazette)
	if !s.isStatic {
		m.HandleFunc("GET /official/{$}", s.officialList)
		m.HandleFunc("GET /official/{id}/{$}", s.official)
		m.HandleFunc("GET /official-files/{id}/{name}", s.officialFiles)
		m.HandleFunc("GET /ruling-files/{name}", s.rulingFile)
	}
	for _, k := range []string{"opinion", "agreement", "doctrine", "research"} {
		kind := docKinds[k]
		m.HandleFunc("GET /"+kind.list+"/{$}", s.docList(kind))
		m.HandleFunc("GET /"+kind.list+"/page/{page}/{$}", s.docList(kind))
		m.HandleFunc("GET /"+k+"/{id}/{$}", s.doc(kind))
	}
	s.topicRoutes()
	s.hubRoutes()
	s.blogRoutes()
	m.HandleFunc("GET /.well-known/", probe404)
	// A GET to /mcp gets the MCP server's answer (mcpGet), not a redirect to
	// a 404 page; POST and OPTIONS are mcpRoutes'.
	m.HandleFunc("GET /mcp", s.mcpGet)
	m.HandleFunc("GET /mcp/", s.mcpGet)
	m.HandleFunc("GET /ai/{$}", s.aiPage)
	// the API's "docs" link, the README and the launch posts send developers
	// here; /ai/ documents the JSON API and the MCP server, so serve it (its
	// canonical stays /ai/)
	m.HandleFunc("GET /developers/{$}", s.aiPage)
	m.HandleFunc("GET /run-it-yourself/{$}", s.localPage)
	m.HandleFunc("GET /data/{$}", s.data)
	m.HandleFunc("GET /about/{$}", s.about)
	m.HandleFunc("GET /contribute/{$}", s.contribute)
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		if to := s.finalPath(r.Context(), p); to != p { // /laws: to /laws/, in one hop to wherever it moved
			movedTo(w, r, to)
			return
		}
		s.notFound(w, r)
	})
}

func (s *Server) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 15*time.Second)
}

func pathID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	return id, err == nil && id > 0
}

// ---------------------------------------------------------------- shared rows

// LawRow is a text in a list.
type LawRow struct {
	ID       int
	Kind     string
	KindEn   *string
	Number   *string
	IssuedOn *time.Time
	Title    string
	TitleEn  string // existing unofficial title, when English search matched
	Status   string
	Articles int
	Snippet  string // search excerpt, with \x02 \x03 around matches
	Article  string // search hits: the article that matched (Number stays the text's own number)
	Code     *string
}

const lawRowCols = `l.id, l.kind, k.name_en, l.number, l.issued_on, l.title, l.status,
	(SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id)::int, (SELECT name_en FROM code c WHERE c.law_id = l.id)`

func scanLawRows(rows pgx.Rows) ([]LawRow, error) {
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (LawRow, error) {
		var x LawRow
		err := row.Scan(lawRowDest(&x)...)
		return x, err
	})
}

// lawRowDest is where lawRowCols scan to.
func lawRowDest(x *LawRow) []any {
	return []any{&x.ID, &x.Kind, &x.KindEn, &x.Number, &x.IssuedOn, &x.Title, &x.Status, &x.Articles, &x.Code}
}

// Ident is how a text is identified in Lebanon: kind, number, date.
func (l LawRow) Ident() string {
	s := l.Kind
	if l.Number != nil {
		s += " رقم " + *l.Number
	}
	if l.IssuedOn != nil {
		s += " تاريخ " + dmy(l.IssuedOn)
	}
	return s
}

// CiteName is how its articles are cited: a code by its name, anything else
// by kind, number and date.
func (l LawRow) CiteName() string {
	t := strings.TrimSpace(l.Title)
	if f := strings.Fields(t); len(f) > 0 && len([]rune(t)) <= 60 {
		switch f[0] {
		case "قانون", "نظام", "الدستور", "اصول", "أصول", "مجلة":
			return t
		}
	}
	return l.Ident()
}

// Stats are the corpus totals the home and about pages print.
type Stats struct {
	Laws, Rulings, Gazette, Opinions, Agreements, Doctrine, Articles, WithText int
	Since                                                                      int
}

func (s *Server) stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM legislation), (SELECT count(*) FROM ruling), (SELECT count(*) FROM gazette_issue),
		(SELECT count(*) FROM opinion), (SELECT count(*) FROM agreement), (SELECT count(*) FROM doctrine_article),
		(SELECT count(*) FROM legislation_article), (SELECT count(DISTINCT law_id) FROM legislation_article),
		(SELECT coalesce(min(extract(year FROM issued_on))::int, 1900) FROM legislation WHERE issued_on > '1850-01-01')`).
		Scan(&st.Laws, &st.Rulings, &st.Gazette, &st.Opinions, &st.Agreements, &st.Doctrine, &st.Articles, &st.WithText, &st.Since)
	return st, err
}

// ---------------------------------------------------------------- home, lists

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	st, err := s.stats(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	codes, err := s.codeRows(ctx, 12)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE l.issued_on IS NOT NULL ORDER BY l.issued_on DESC, l.id DESC LIMIT 8`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	recent, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	homeTitle := "Lebanese Legislation · Every Lebanese law, one search away"
	if s.lang(r) == "ar" {
		homeTitle = s.tr(r, "brand.name") + " · " + s.tr(r, "meta.home_tagline")
	}
	s.render(w, r, "home", 200, Page{
		Title: homeTitle,
		Desc: s.trOr(r, fmt.Sprintf("Search %s Lebanese laws, decrees and rulings in Arabic or French, read every code article by article, and cite any article in one click.", thousands(st.Laws)),
			"meta.home_desc", thousands(st.Laws)),
		Path: "/",
		LD: []any{map[string]any{"@context": "https://schema.org", "@type": "WebSite", "name": "Lebanese Legislation",
			"url": Base + "/", "inLanguage": []string{"ar", "fr", "en"},
			"publisher":       map[string]any{"@type": "Organization", "name": "Taktek, LLC", "url": "https://taktek.io/"},
			"potentialAction": map[string]any{"@type": "SearchAction", "target": Base + "/search/?q={q}", "query-input": "required name=q"}},
			map[string]any{"@context": "https://schema.org", "@type": "Organization", "@id": Base + "/#org", "name": "Lebanese Legislation",
				"url": Base + "/", "logo": Base + "/assets/mark.png", "description": "A free public library of Lebanese law."}},
		Data: map[string]any{"Stats": st, "Codes": codes, "Recent": recent, "Topics": homeTopicRefs(s.lang(r))},
	})
}

// CodeRow is a curated code.
type CodeRow struct {
	LawRow
	Slug   string
	NameEn string
	NameFr *string
}

func (s *Server) codeRows(ctx context.Context, limit int) ([]CodeRow, error) {
	rows, err := s.db.Query(ctx, `SELECT c.slug, c.name_en, c.name_fr, `+lawRowCols+`
		FROM code c JOIN legislation l ON l.id = c.law_id JOIN legislation_kind k ON k.name_ar = l.kind
		ORDER BY c.position LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CodeRow, error) {
		var c CodeRow
		err := row.Scan(&c.Slug, &c.NameEn, &c.NameFr, &c.ID, &c.Kind, &c.KindEn, &c.Number, &c.IssuedOn, &c.Title,
			&c.Status, &c.Articles, &c.Code)
		return c, err
	})
}

func (s *Server) codes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	cs, err := s.codeRows(ctx, 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var items []any
	for i, c := range cs {
		items = append(items, map[string]any{"@type": "ListItem", "position": i + 1, "name": c.NameEn + " (" + c.Title + ")",
			"url": Base + lawPath(c.ID)})
	}
	s.render(w, r, "codes", 200, Page{Title: s.brandTitle(r, s.trOr(r, "The Lebanese codes", "codes.title_suffix")), Path: "/codes/",
		LD:   []any{map[string]any{"@context": "https://schema.org", "@type": "ItemList", "name": "The principal Lebanese codes", "itemListElement": items}},
		Desc: s.trOr(r, "The Code of Obligations and Contracts, the Penal Code, the Code of Commerce, the Labour Code and the other principal Lebanese codes, article by article.", "codes.desc"),
		Data: map[string]any{"Codes": cs, "Updated": s.updated(r, "laws")}})
}

func (s *Server) code(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	var id int
	if err := s.db.QueryRow(ctx, "SELECT law_id FROM code WHERE slug = $1", r.PathValue("slug")).Scan(&id); err != nil {
		s.notFound(w, r)
		return
	}
	http.Redirect(w, r, lawPath(id), http.StatusFound)
}

// Chip is a filter option.
type Chip struct {
	Value, Label string
	Count        int
}

// List is a page of documents with its filters.
type List struct {
	Eyebrow, Heading string // when set, replace the page's default headings
	Back, BackLabel  string
	Q                url.Values
	Total            int
	Page             int
	Pages            int
	Filters          [][]Chip
	Selected         map[string]string
	Params           []string // filter parameter per row of chips
	Rows             any
	Updated          *Updated // "Last updated": when the source records last changed
	Base             string   // the list's path, when its pages are paths (/opinions/page/2/); empty: ?page=
	Subhubs          []Chip   // links to the lists this one divides into (a year's kinds); Value is a path
}

// Path is page n's address when the list paginates by path.
func (l List) Path(n int) string { return pagePath(l.Base, n) }

// Query is page n's address when it paginates by ?page= (a filtered list).
func (l List) Query(n int) template.URL { return query(l.Q, "page", strconv.Itoa(n)) }

func pageNo(q url.Values) int {
	n, _ := strconv.Atoi(q.Get("page"))
	return max(n, 1)
}

func (s *Server) laws(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.ctx(r)
	defer cancel()
	q := r.URL.Query()
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }
	if v := q.Get("kind"); v != "" {
		where = append(where, "l.kind = "+arg(v))
	}
	if v := q.Get("status"); v == "in_force" || v == "repealed" {
		where = append(where, "l.status = "+arg(v))
	}
	if v, err := strconv.Atoi(q.Get("decade")); err == nil && v >= 1800 && v < 2100 {
		where = append(where, "l.issued_on >= make_date("+arg(v)+", 1, 1) AND l.issued_on < make_date("+arg(v+10)+", 1, 1)")
	}
	if q.Get("text") == "1" {
		where = append(where, "EXISTS (SELECT 1 FROM legislation_article a WHERE a.law_id = l.id)")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l"+cond, args...).Scan(&total); err != nil {
		s.fail(w, r, err)
		return
	}
	// /laws/ is browsed by year (the years' lists are paged by path); its own
	// pages stay ?page=, which robots.txt blocks: 2,500 deep pages cost seconds each
	page := min(pageNo(q), max(1, (total+perPage-1)/perPage))
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind`+cond+
		fmt.Sprintf(" ORDER BY l.issued_on DESC NULLS LAST, l.id DESC LIMIT %d OFFSET %d", perPage, (page-1)*perPage), args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := scanLawRows(rows)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	kindLabelSQL := "kind || coalesce(' · ' || k.name_en, '')"
	if s.lang(r) == "ar" { // the Arabic data already names the kind; no English needed too
		kindLabelSQL = "kind"
	}
	kinds, err := s.chips(ctx, `SELECT kind, `+kindLabelSQL+`, count(*) FROM legislation l
		JOIN legislation_kind k ON k.name_ar = l.kind GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 10`)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var decades []Chip
	for y := 2020; y >= 1920; y -= 10 {
		decades = append(decades, Chip{Value: strconv.Itoa(y), Label: fmt.Sprintf("%ds", y)})
	}
	decades = append(decades, Chip{Value: "1900", Label: s.trOr(r, "Before 1930", "chip.before_1930")})
	s.render(w, r, "laws", 200, Page{Title: s.brandTitle(r, s.trOr(r, "All Lebanese legislation", "laws.title_suffix")), Path: "/laws/",
		NoIndex: r.URL.RawQuery != "",
		Desc: s.trOr(r, fmt.Sprintf("Browse %s Lebanese laws, decrees and decisions by kind, decade and status. Free.", thousands(total)),
			"laws.desc", thousands(total)),
		Data: List{Q: q, Total: total, Page: page, Pages: (total + perPage - 1) / perPage, Rows: list, Updated: s.updated(r, "laws"),
			Params:   []string{"kind", "status", "decade"},
			Selected: map[string]string{"kind": q.Get("kind"), "status": q.Get("status"), "decade": q.Get("decade")},
			Filters: [][]Chip{prepend(Chip{Label: s.trOr(r, "All kinds", "chip.all_kinds")}, kinds),
				{{Label: s.trOr(r, "Any status", "chip.any_status")}, {Value: "in_force", Label: s.trOr(r, "In force", "chip.in_force")},
					{Value: "repealed", Label: s.trOr(r, "Repealed", "chip.repealed")}},
				prepend(Chip{Label: s.trOr(r, "Any year", "chip.any_year")}, decades)}},
	})
}

func prepend(c Chip, cs []Chip) []Chip { return append([]Chip{c}, cs...) }

func (s *Server) chips(ctx context.Context, sql string, args ...any) ([]Chip, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Chip, error) {
		var c Chip
		err := row.Scan(&c.Value, &c.Label, &c.Count)
		return c, err
	})
}

// ---------------------------------------------------------------- one law

// Section is a chapter heading in a law.
type Section struct {
	ID      int
	Depth   int
	Heading string
	Note    *string // the Centre's editorial note ("اضيف هذا الجزء … بموجب …")
}

// Article is one article, ready to print.
type Article struct {
	Position    int
	Number      string
	SectionID   *int // the chapter it sits under
	Note        *string
	TextAr      string
	TextFr      *string
	Images      []string
	From        *time.Time
	Versions    []Version   // oldest first; none when never amended
	Unsure      []Change    // changes that may predate the printed text or not: being checked
	Added       bool        // only an amendment carries it, not the Centre
	Links       []Link      // texts that changed it, with their wording (links.go)
	Affected    []Affected  // general repeals that may touch it: to check
	Rulings     []RulingRef // rulings that applied it, newest first (at most maxRulings)
	RulingCount int         // how many rulings applied it
	CentreError *CentreError
	Council     []CouncilRuling // the Constitutional Council's decisions on this article
}

// CouncilRuling is a Constitutional Council decision on a text or one of
// its articles (db/council_rulings.tsv). The wording stays as published.
type CouncilRuling struct {
	Number    string     `json:"article,omitempty"` // "" for the decision on the text as a whole
	Effect    string     `json:"effect"`            // annulled, annulled_in_part, not_in_force, suspended, upheld
	Decision  string     `json:"decision"`          // the Council's number, as "5/2025"
	On        *time.Time `json:"-"`
	SourceURL string     `json:"source_url"`
	En        string     `json:"note_en"`
	Ar        string     `json:"note_ar"`
}

// CouncilDecision is one decision on a law's page: the row for the text as
// a whole and the articles it names.
type CouncilDecision struct {
	CouncilRuling
	Articles []CouncilRuling
}

// Annulled reports whether a Constitutional Council decision annulled the
// whole text.
func (l *Law) Annulled() bool {
	for _, d := range l.Council {
		if d.Effect == "annulled" {
			return true
		}
	}
	return false
}

// NotInForce reports whether the Constitutional Council declared the text
// not in force: it found the text was never validly promulgated, so did not
// rule on its content (as decision 5/2025 on Law 1/2025).
func (l *Law) NotInForce() bool {
	for _, d := range l.Council {
		if d.Effect == "not_in_force" {
			return true
		}
	}
	return false
}

// AnnulledWhole reports whether a Constitutional Council decision annulled
// the whole article.
func (a *Article) AnnulledWhole() bool {
	for _, c := range a.Council {
		if c.Effect == "annulled" {
			return true
		}
	}
	return false
}

// CentreError is an error in the Centre's own text of an article
// (db/centre_errors.tsv), shown next to it.
type CentreError struct {
	En string `json:"en"`
	Ar string `json:"ar"`
}

// Item is a chapter heading or an article, in the text's order.
type Item struct {
	Chapter *Section
	Article *Article
}

// Law is everything on a law's page.
type Law struct {
	LawRow
	GazetteNumber    *string
	GazetteDate      *time.Time
	GazettePages     *string
	GazetteIssue     *int
	Preamble         *string
	ExplanatoryNote  *string
	Closing          *string
	Source           string
	Sections         []Section
	Items            []Item
	List             []*Article // the articles, in order (Articles is their count)
	HasFr            bool
	RepealedBy       []LawRow
	RepealedByChange []Change   // whole-text repeals read from the repealing texts
	Changes          []Change   // to this text, by later texts (applied or in part)
	Links            []Link     // texts that changed the text as a whole, with their wording (links.go)
	Affected         []Affected // general repeals that may touch the text: to check
	Amends           []Change   // this text's changes to others
	CitedBy          []DocRow
	CitedCount       int
	Related          []LawRow // same kind and year, nearest by date
	PrevLaw, NextLaw *LawRow  // the texts of the same year and kind word issued just before and just after (hubs.go)
	CodeSlug         *string
	TitleEn          *string // unofficial English title of the subject line (db/titles_en.tsv)
	Answer           string  // the answer line at the top of the page, from record fields only
	Scan             *Scan   // set when the text was read from Gazette scans
	DateFixes        []DateFix
	Council          []CouncilDecision // the Constitutional Council's decisions on it, newest first
	Part             int               // the part shown, when the text is cut into parts (lawparts.go)
	Parts            []LawPart         // all of them; nil when one page holds the text
	SectionPart      map[int]int       // chapter id -> the part it is in
}

// DateFix is a date the Centre printed wrong and what the text shows instead
// (date_correction, internal/build/dates.go).
type DateFix struct {
	Field     string // issued_on or gazette_date
	Printed   *time.Time
	Corrected *time.Time
	Method    string // signature, gazette-issue, year-typo, placeholder, model, guard
}

// DateFix returns the correction of one date field, or nil.
func (l *Law) DateFix(field string) *DateFix {
	for i := range l.DateFixes {
		if l.DateFixes[i].Field == field {
			return &l.DateFixes[i]
		}
	}
	return nil
}

// Scan describes a text read from scans of the Gazette's own pages, for one
// the Lebanese University has not published yet (internal/build/scans.go).
type Scan struct {
	Source    string
	SourceURL string
	Pages     int
	Agreement float64
	Flagged   int
}

// PageNumbers counts the page images, from 1, for the template.
func (c *Scan) PageNumbers() []int {
	ns := make([]int, c.Pages)
	for i := range ns {
		ns[i] = i + 1
	}
	return ns
}

// AgreementPct is the share of words both readings agree on, in percent.
func (c *Scan) AgreementPct() string { return fmt.Sprintf("%.1f", 100*c.Agreement) }

func (s *Server) loadLaw(ctx context.Context, id int, withText bool) (*Law, error) {
	return s.loadLawWith(ctx, id, withText, true)
}

// loadLawWith is loadLaw; rulings says whether every article gets the rulings
// that cite it. A text's own page shows them for all its articles. An article
// page shows them for one article (attachRulingsFor), and a feed for none.
func (s *Server) loadLawWith(ctx context.Context, id int, withText, rulings bool) (*Law, error) {
	l := &Law{}
	err := s.db.QueryRow(ctx, `SELECT `+lawRowCols+`, l.gazette_number, l.gazette_date, l.gazette_pages,
		(SELECT min(issue_id) FROM gazette_entry e WHERE e.law_id = l.id), l.preamble, l.explanatory_note, l.closing,
		coalesce((SELECT url FROM source_page p WHERE p.doc_type = 'law' AND p.centre_id = l.id), ''),
		(SELECT slug FROM code c WHERE c.law_id = l.id)
		FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id).Scan(
		&l.ID, &l.Kind, &l.KindEn, &l.Number, &l.IssuedOn, &l.Title, &l.Status, &l.Articles, &l.Code,
		&l.GazetteNumber, &l.GazetteDate, &l.GazettePages, &l.GazetteIssue, &l.Preamble, &l.ExplanatoryNote, &l.Closing,
		&l.Source, &l.CodeSlug)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(l.Title) == "" { // six texts have no title at the source
		l.Title = l.Ident()
	}
	// An older database has no date_correction: the text shows without it.
	if rows, err := s.db.Query(ctx, `SELECT field, printed, corrected, method FROM date_correction WHERE law_id = $1 ORDER BY field`, id); err == nil {
		for rows.Next() {
			var f DateFix
			if rows.Scan(&f.Field, &f.Printed, &f.Corrected, &f.Method) == nil {
				l.DateFixes = append(l.DateFixes, f)
			}
		}
		rows.Close()
	}
	if l.ID >= scanIDBase {
		c := &Scan{}
		// An older database has no legislation_scan: the text shows without it.
		if err := s.db.QueryRow(ctx, `SELECT source, source_url, pages, coalesce(agreement, 0), coalesce(flagged, 0)
			FROM legislation_scan WHERE law_id = $1`, id).Scan(&c.Source, &c.SourceURL, &c.Pages, &c.Agreement, &c.Flagged); err == nil {
			l.Scan = c
		}
	}
	if !withText {
		return l, nil
	}
	rows, err := s.db.Query(ctx, `SELECT id, depth, heading, note FROM legislation_section WHERE law_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	if l.Sections, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Section]); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT position, number, note, text_ar, text_fr, images, in_force_from, section_id
		FROM legislation_article WHERE law_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	// Chapters are printed where they begin: before the first article in
	// them, along with any chapter between the previous article's and it.
	printed := map[int]bool{}
	order := map[int]int{}
	for i, sec := range l.Sections {
		order[sec.ID] = i
	}
	next := 0
	for rows.Next() {
		a := &Article{}
		var sec *int
		if err := rows.Scan(&a.Position, &a.Number, &a.Note, &a.TextAr, &a.TextFr, &a.Images, &a.From, &sec); err != nil {
			return nil, err
		}
		a.SectionID = sec
		if sec != nil {
			for ; next <= order[*sec] && next < len(l.Sections); next++ {
				if !printed[l.Sections[next].ID] {
					printed[l.Sections[next].ID] = true
					l.Items = append(l.Items, Item{Chapter: &l.Sections[next]})
				}
			}
		}
		if a.TextFr != nil {
			l.HasFr = true
		}
		l.Items = append(l.Items, Item{Article: a})
		l.List = append(l.List, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// An older database has no centre_error: the articles show without it.
	if rows, err := s.db.Query(ctx, `SELECT number, note_en, note_ar FROM centre_error WHERE law_id = $1`, id); err == nil {
		byNumber := map[string]*Article{}
		for _, a := range l.List {
			if byNumber[a.Number] == nil {
				byNumber[a.Number] = a
			}
		}
		for rows.Next() {
			var n string
			var e CentreError
			if rows.Scan(&n, &e.En, &e.Ar) == nil && byNumber[n] != nil {
				byNumber[n].CentreError = &e
			}
		}
		rows.Close()
	}
	// An older database has no council_ruling: the page shows without it.
	if rows, err := s.db.Query(ctx, `SELECT number, effect, decision, decided_on, source_url, note_en, note_ar
		FROM council_ruling WHERE law_id = $1 ORDER BY decided_on, decision, number = '' DESC, number`, id); err == nil {
		var all []CouncilRuling
		for rows.Next() {
			var c CouncilRuling
			if rows.Scan(&c.Number, &c.Effect, &c.Decision, &c.On, &c.SourceURL, &c.En, &c.Ar) == nil {
				all = append(all, c)
			}
		}
		rows.Close()
		l.attachCouncil(all)
	}
	// An older database has no legislation_title_en: the page shows without it.
	var te string
	if s.db.QueryRow(ctx, `SELECT title_en FROM legislation_title_en WHERE law_id = $1`, id).Scan(&te) == nil && te != "" {
		l.TitleEn = &te
	}
	if err := s.attachVersions(ctx, l, rulings); err != nil {
		return nil, err
	}
	return l, nil
}

// AmendedBy groups the changes to this text by amending text, newest first.
func (l *Law) AmendedBy() []ChangeGroup { return group(l.Changes) }

// AmendsGroups groups this text's changes to others by text.
func (l *Law) AmendsGroups() []ChangeGroup { return group(l.Amends) }

func (s *Server) law(w http.ResponseWriter, r *http.Request) { s.lawPage(w, r, 0) }

// lawPage serves a text, or its part n when it is cut into parts (lawparts.go).
func (s *Server) lawPage(w http.ResponseWriter, r *http.Request, part int) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if s.rightWord(w, r, id) {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	l, err := s.loadLaw(ctx, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		if !s.aliasRedirect(ctx, w, r, id) {
			s.notFound(w, r)
		}
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM reference x JOIN legislation l ON l.id = x.from_id
		JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE x.law_id = $1 AND x.from_type = 'legislation' AND x.relation = 'repeals'`, id)
	if err == nil {
		l.RepealedBy, err = scanLawRows(rows)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.db.QueryRow(ctx, `SELECT count(DISTINCT from_id) FROM reference WHERE law_id = $1 AND from_type = 'ruling'`, id).
		Scan(&l.CitedCount); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err = s.db.Query(ctx, `SELECT 'ruling', r.id, r.title, coalesce(r.court, ''), r.decided_on,
		string_agg(DISTINCT x.label, ' · ')
		FROM reference x JOIN ruling r ON r.id = x.from_id
		WHERE x.law_id = $1 AND x.from_type = 'ruling' GROUP BY r.id ORDER BY r.decided_on DESC NULLS LAST LIMIT 12`, id)
	if err == nil {
		l.CitedBy, err = scanDocRows(rows)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if l.Amends, err = s.changes(ctx, "x.by_law_id = $1 AND x.status <> 'review'", "x.law_id", id); err != nil {
		s.fail(w, r, err)
		return
	}
	if l.IssuedOn != nil {
		rows, err = s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
			WHERE l.kind = $1 AND l.id <> $2 AND l.issued_on >= make_date($3, 1, 1) AND l.issued_on < make_date($3 + 1, 1, 1)
			ORDER BY abs(l.issued_on - $4::date), l.id LIMIT 6`, l.Kind, id, l.IssuedOn.Year(), *l.IssuedOn)
		if err == nil {
			l.Related, err = scanLawRows(rows)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if l.PrevLaw, l.NextLaw, err = s.neighbours(ctx, l); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.trackRead(r, l, "")
	bn := i18n.New(s.lang(r))
	l.Answer = lawAnswer(bn, l)
	ld := legislationLD(l, s.u(r, lawPath(l.ID)), l.Answer)
	crumbs := breadcrumbs("Legislation", "/laws/", cut(l.Title, 80), lawPath(l.ID))
	if l.IssuedOn != nil {
		y := l.IssuedOn.Year()
		crumbs = breadcrumbs("Legislation", "/laws/", strconv.Itoa(y), fmt.Sprintf("/laws/year/%d/", y), cut(l.Title, 80), lawPath(l.ID))
	}
	p := Page{Title: lawTitle(bn, l), Desc: lawDesc(bn, l), Lang: "ar", Translated: true,
		Path: lawPath(l.ID), OGType: "article", LD: []any{ld, crumbs}, Data: l}
	s.weakText(ctx, r, &p, l.ID)
	s.serveLaw(w, r, l, p, part)
}

// legislationLD describes a text as schema.org Legislation. path is the
// page's own canonical address (with /en only on a page with English of its own); description is the
// record-field answer line. legislationLegalForce is set only when the record
// knows it: a repeal. "In force" is the absence of a recorded repeal, so it
// is not claimed.
func legislationLD(l *Law, path, description string) map[string]any {
	langs := []string{"ar"}
	if l.HasFr {
		langs = append(langs, "fr")
	}
	ld := map[string]any{"@context": "https://schema.org", "@type": "Legislation",
		"@id": Base + path, "url": Base + path,
		"name": l.Title, "legislationIdentifier": l.Ident(), "legislationType": l.Kind,
		"inLanguage": langs, "isAccessibleForFree": true,
		"legislationJurisdiction": map[string]any{"@type": "Country", "name": "Lebanon", "identifier": "LB"},
		"isBasedOn":               l.Source,
		"publisher":               map[string]any{"@type": "Organization", "name": "Lebanese Legislation", "url": Base + "/"}}
	if force := legislationForce(l.Status); force != "" {
		ld["legislationLegalForce"] = force
	}
	if description != "" {
		ld["description"] = description
	}
	var alt []string
	if l.Code != nil {
		alt = append(alt, *l.Code)
	}
	if l.TitleEn != nil {
		alt = append(alt, *l.TitleEn)
	}
	switch len(alt) {
	case 0:
	case 1:
		ld["alternateName"] = alt[0]
	default:
		ld["alternateName"] = alt
	}
	if l.IssuedOn != nil {
		ld["legislationDate"] = l.IssuedOn.Format("2006-01-02")
	}
	if l.GazetteDate != nil {
		ld["datePublished"] = l.GazetteDate.Format("2006-01-02")
	}
	if l.Source == "" {
		delete(ld, "isBasedOn")
	}
	return ld
}

// breadcrumbs builds a BreadcrumbList from name, path pairs, after Home.
func breadcrumbs(pairs ...string) map[string]any {
	items := []any{map[string]any{"@type": "ListItem", "position": 1, "name": "Lebanese Legislation", "item": Base + "/"}}
	for i := 0; i+1 < len(pairs); i += 2 {
		items = append(items, map[string]any{"@type": "ListItem", "position": len(items) + 1, "name": pairs[i], "item": Base + pairs[i+1]})
	}
	return map[string]any{"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": items}
}

func (s *Server) article(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if s.rightWord(w, r, id) {
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	l, err := s.loadLawWith(ctx, id, true, false)
	if errors.Is(err, pgx.ErrNoRows) {
		if !s.aliasRedirect(ctx, w, r, id) {
			s.notFound(w, r)
		}
		return
	} else if err != nil {
		s.fail(w, r, err)
		return
	}
	want := r.PathValue("n")
	for i, a := range l.List {
		if Slug(a.Number) != want {
			continue
		}
		if err := s.attachRulingsFor(ctx, l, a.Number); err != nil {
			s.fail(w, r, err)
			return
		}
		p := s.articlePage(r, l, i)
		s.trackRead(r, l, a.Number)
		p.NoIndex = s.noindexed(ctx, "article", l.ID, a.Position)
		s.weakText(ctx, r, &p, l.ID)
		p.Data.(map[string]any)["Topics"] = s.topicsFor(r, l.ID, a.Number)
		if l.CodeSlug != nil && !s.isStatic {
			p.Data.(map[string]any)["Hub"] = codeHubPath(*l.CodeSlug, codeHubPage(i)) + "#a-" + Slug(a.Number)
		}
		s.render(w, r, "article", 200, p)
		return
	}
	// Another spelling of a number the text has ("12/1", "12.1", "٥", "12-bis"):
	// to the address of the article it names.
	numbers := make([]string, len(l.List))
	for i, a := range l.List {
		numbers[i] = a.Number
	}
	if n := articleNamed(want, numbers); n != "" && Slug(n) != want {
		http.Redirect(w, r, artPath(l.ID, n), http.StatusMovedPermanently)
		return
	}
	s.notFound(w, r)
}

// articlePage is the page of article l.List[i], all but its noindex flag and
// its topics, which read the database. It is a translation pair like the
// text's own page: the English interface is self-canonical, and both name
// each other with hreflang.
func (s *Server) articlePage(r *http.Request, l *Law, i int) Page {
	a := l.List[i]
	var prev, next *Article
	if i > 0 {
		prev = l.List[i-1]
	}
	if i+1 < len(l.List) {
		next = l.List[i+1]
	}
	name := "المادة " + a.Number + " من " + l.CiteName()
	path := artPath(l.ID, a.Number)
	bn := i18n.New(s.lang(r))
	answer := articleAnswer(bn, l, a.Number)
	parent := legislationLD(l, lawPath(l.ID), "")
	ld := map[string]any{"@context": "https://schema.org", "@type": "Legislation", "@id": Base + path, "url": Base + path,
		"name": name, "legislationIdentifier": name, "inLanguage": "ar", "isAccessibleForFree": true,
		"description": answer, "legislationJurisdiction": parent["legislationJurisdiction"],
		"isPartOf": map[string]any{"@type": "Legislation", "@id": parent["@id"], "name": l.Title, "legislationIdentifier": l.Ident()}}
	if force, ok := parent["legislationLegalForce"]; ok {
		ld["legislationLegalForce"] = force
	}
	if l.GazetteDate != nil {
		ld["datePublished"] = l.GazetteDate.Format("2006-01-02")
	}
	if a.From != nil {
		ld["legislationDate"] = a.From.Format("2006-01-02")
	}
	text := a.Shown()
	if strings.TrimSpace(text) == "" { // a repealed article: say what the Centre says, else the text it belongs to
		if a.Note != nil && strings.TrimSpace(*a.Note) != "" {
			text = *a.Note
		} else {
			text = l.Title
		}
	}
	desc := articleDesc(bn, l, a.Number, strings.ReplaceAll(text, "\n", " "))
	return Page{Title: artTitle(bn, l, a.Number), Desc: desc, Path: path, OGType: "article", Lang: "ar", Translated: true,
		LD:   []any{ld, breadcrumbs(cut(l.Title, 80), lawPath(l.ID), "المادة "+a.Number, path)},
		Data: map[string]any{"Law": l, "A": a, "Prev": prev, "Next": next, "Answer": answer}}
}

// articleNamed is the article of numbers that a spelling names (any the
// Centre's numbers can be written as: amend.Canon), "" if none.
func articleNamed(raw string, numbers []string) string {
	set := make(map[string]bool, len(numbers))
	for _, n := range numbers {
		set[n] = true
	}
	for _, try := range []string{raw, strings.ReplaceAll(raw, "-", " ")} { // an address is dashes for spaces
		for _, n := range amend.Resolve(try, func(s string) bool { return set[s] }) {
			if n.Found {
				return n.Article
			}
		}
	}
	return ""
}

// gotoArticle answers the "go to article" form without JavaScript: it
// redirects to the article on the law's page (exact number first, then the
// first article whose number starts with the same digits).
func (s *Server) gotoArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if s.rightWord(w, r, id) {
		return
	}
	want := normalizeNumber(r.URL.Query().Get("n"))
	ctx, cancel := s.ctx(r)
	defer cancel()
	rows, err := s.db.Query(ctx, "SELECT number FROM legislation_article WHERE law_id = $1 ORDER BY position", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	numbers, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := articleNamed(r.URL.Query().Get("n"), numbers)
	for _, n := range numbers {
		if target != "" {
			break
		}
		if normalizeNumber(n) == want {
			target = n
			break
		}
	}
	if lead := leadDigits(want); target == "" && lead != "" {
		for _, n := range numbers {
			if leadDigits(normalizeNumber(n)) == lead {
				target = n
				break
			}
		}
	}
	dest := lawPath(id)
	if target != "" {
		dest += "#a-" + Slug(target)
		if r.URL.Query().Get("s") == "1" { // a text read in parts: the article may be in another
			dest = artPath(id, target)
		}
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

var arabicDigits = strings.NewReplacer("٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9")

var (
	articleWord = regexp.MustCompile(`^\s*(ال)?ماد[ةه]\s*`)
	bisWord     = regexp.MustCompile(`(?i)(^|\d|\s)bis\b`)
	bisNumber   = regexp.MustCompile(`مكرر\s*(\d+)`)
	numberBis   = regexp.MustCompile(`(\d)\s*مكرر`)
	dashes      = regexp.MustCompile(`\s*-\s*`)
)

// normalizeNumber reads an article number the way the go-to box does: Latin
// digits, no "المادة", "bis" as مكرر, single spaces.
func normalizeNumber(s string) string {
	s = arabicDigits.Replace(s)
	s = articleWord.ReplaceAllString(s, "")
	s = bisWord.ReplaceAllString(s, "${1}مكرر")
	s = dashes.ReplaceAllString(s, " ")
	s = bisNumber.ReplaceAllString(s, "مكرر $1")
	s = numberBis.ReplaceAllString(s, "$1 مكرر")
	return strings.Join(strings.Fields(s), " ")
}

func leadDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// articlesAr counts articles the way Arabic does: مادة واحدة، مادتان،
// 3 to 10 مواد, then مادة again (11 مادة، 100 مادة).
func articlesAr(n int) string {
	switch {
	case n == 1:
		return "مادة واحدة"
	case n == 2:
		return "مادتان"
	case n%100 >= 3 && n%100 <= 10:
		return thousands(n) + " مواد"
	}
	return thousands(n) + " مادة"
}

// attachCouncil groups Constitutional Council rows by decision, the text's
// row first, and gives each named article its rows. all comes ordered by
// date and decision, the text's row first in each.
func (l *Law) attachCouncil(all []CouncilRuling) {
	byNumber := map[string]*Article{}
	for _, a := range l.List {
		if byNumber[a.Number] == nil {
			byNumber[a.Number] = a
		}
	}
	for _, c := range all {
		if c.Number == "" {
			l.Council = append(l.Council, CouncilDecision{CouncilRuling: c})
			continue
		}
		for i := range l.Council {
			if l.Council[i].Decision == c.Decision {
				l.Council[i].Articles = append(l.Council[i].Articles, c)
			}
		}
		if a := byNumber[c.Number]; a != nil {
			a.Council = append(a.Council, c)
		}
	}
	// Group decisions and their article references before sorting for display.
	sort.SliceStable(l.Council, func(i, j int) bool {
		a, b := l.Council[i].On, l.Council[j].On
		return a != nil && (b == nil || a.After(*b))
	})
	for _, article := range l.List {
		sort.SliceStable(article.Council, func(i, j int) bool {
			a, b := article.Council[i].On, article.Council[j].On
			return a != nil && (b == nil || a.After(*b))
		})
	}
}
