// Package site serves Lebanese Legislation from the Postgres corpus.
//
// Every page is rendered from the database on request; templates and static
// files are compiled into the binary. The design is the Lebanese family's
// (styles.css, main.js) plus legislation.css for the law itself.
package site

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/site/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Base is the site's public address, for canonical links and structured data.
// A copy hosted elsewhere sets its own (internal/app reads PUBLIC_URL).
var Base = "https://lebaneselegislation.com"

// BuildID is set at link time by the deployment build and exposed by /healthz.
var BuildID = "unknown"

// Server holds the database pool and the parsed templates.
type Server struct {
	vector        vectorSearcher
	meaning       *meaningSearch // explicitly requested natural language interpretation
	scache        searchCache    // answers to recent searches
	db            *pgxpool.Pool
	pages         map[string]*template.Template // English, at the plain paths
	pagesAr       map[string]*template.Template // Arabic, at the root and /ar/...; s.pages is English (/en/...); pagesAr is nil when isStatic
	handler       http.Handler                  // mux wrapped in the /ar language middleware
	mux           *http.ServeMux
	isStatic      bool // rendering a static copy: see NewStatic
	ev            *Events
	analytics     bool                  // load the browser analytics (ga.js, ph.js): SetAnalytics
	mcpRegistered bool                  // mcpRoutes has run
	scanDir       string                // page images of texts read from Gazette scans: see scans.go
	rulingDir     string                // the judgment PDFs, one per ruling: see rulingfiles.go
	ph            *phClient             // server errors to PostHog: see phcapture.go
	titles        titlesEn              // unofficial English titles for the JSON API: see apititles.go
	vocab         atomic.Pointer[vocab] // the title words, for typos and Latin spellings: see vocab.go

	// Protection against overload (limiter.go), shared by /mcp (not wired
	// yet, see mcp.go) and, once wired, /api/v1.
	ipLim  *ipLimiter
	dbSem  *dbSemaphore
	ttlLRU *ttlCache

	// Admission control for the pages (admit.go): pageSlots at a time, a wait of admitWait for one.
	pageSem   *dbSemaphore
	heavySem  *dbSemaphore  // the whole-text routes among them: heavyRoute
	admitWait time.Duration // zero: the default, admitWait; tests shorten it

	// Seams for tests of the sitemap cache (sitemap_sets.go); nil in production.
	versionFn func(context.Context) string
	buildSet  func(context.Context, sitemapSet) ([]sitemapShard, error)

	memo      memoCache                                         // sitemap.go
	sets      setCache                                          // the big sitemap families, built once: sitemap_sets.go
	coreExtra []func(ctx context.Context) ([]SitemapURL, error) // more core sitemap entries: AddCoreSitemap
}

// SetEvents turns on the anonymous usage record (see events.go).
func (s *Server) SetEvents(e *Events) { s.ev = e }

// SetAnalytics turns on the browser analytics: pages load ga.js and ph.js,
// which send to LebaneseLegislation.com's own Google Analytics and PostHog
// projects. internal/app turns it on unless ANALYTICS=off, which the
// published images set, so a copy hosted elsewhere sends nothing to them.
func (s *Server) SetAnalytics(on bool) { s.analytics = on }

// New prepares a server on an open pool.
func New(db *pgxpool.Pool) (*Server, error) { return newServer(db, false) }

// NewStatic prepares a server whose pages can be saved as a static site:
// articles are anchors on their law's page rather than pages of their own,
// the long lists are browsed by year, search runs in the browser over law
// titles, and every page is marked noindex (it is a preview).
func NewStatic(db *pgxpool.Pool) (*Server, error) { return newServer(db, true) }

// StaticFiles are the stylesheets, scripts and images the pages load.
func StaticFiles() fs.FS { f, _ := fs.Sub(staticFS, "static"); return f }

func newServer(db *pgxpool.Pool, static bool) (*Server, error) {
	if db != nil {
		// The word in each text's address (/law/, /decree/, /text/): words.go.
		if err := loadTextWords(context.Background(), db); err != nil {
			return nil, err
		}
	}
	s := &Server{db: db, pages: map[string]*template.Template{}, mux: http.NewServeMux(), isStatic: static,
		ipLim: newIPLimiter(60, 20), dbSem: newDBSemaphore(4), ttlLRU: newTTLCache(256, mcpCacheTTL), pageSem: newDBSemaphore(pageSlots), heavySem: newDBSemaphore(heavySlots)}
	if db != nil && !static {
		go func() { // a few seconds at most; until it is there, typos and Arabizi are simply not mended
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if v, err := loadVocab(ctx, db); err == nil {
				s.vocab.Store(v)
			}
		}()
	}
	funcs := template.FuncMap{}
	for k, v := range baseFuncs {
		funcs[k] = v
	}
	funcs["static"] = func() bool { return static }
	funcs["analytics"] = func() bool { return s.analytics }
	funcs["meaningEnabled"] = func() bool { return s.meaning != nil && !static }
	funcs["meaningProvider"] = func() string {
		if s.meaning != nil {
			return s.meaning.provider
		}
		return ""
	}
	funcs["meaningShares"] = func() bool { return s.meaning != nil && s.meaning.shares }
	if static {
		funcs["artURL"] = func(id int, number string) string { return fmt.Sprintf("/law/%d/#a-%s", id, Slug(number)) }
	}
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		base := strings.TrimSuffix(strings.TrimPrefix(n, "templates/"), ".html")
		if base == "layout" || strings.HasPrefix(base, "_") { // _*.html are partials, parsed into every page
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", n)
		if err == nil {
			_, err = t.ParseFS(templateFS, "templates/_*.html")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		s.pages[base] = t
	}
	if !static { // the English and Arabic sets are Clones of every page, with
		// language-specific funcs (t, u, lawURL, artURL...) bound over the
		// same parsed templates; see i18n_funcs.go.
		s.pagesAr = map[string]*template.Template{}
		enFuncs, arFuncs := englishFuncs(), arabicFuncs()
		for name, t := range s.pages {
			c, err := t.Clone()
			if err != nil {
				return nil, fmt.Errorf("ar clone %s: %w", name, err)
			}
			s.pagesAr[name] = c.Funcs(arFuncs)
			s.pages[name] = t.Funcs(enFuncs)
		}
	}
	files := http.FileServerFS(StaticFiles())
	for _, p := range []string{"/styles.css", "/legislation.css", "/main.js", "/legislation.js", "/language.js", "/ga.js", "/ph.js", "/assets/"} {
		s.mux.Handle("GET "+p, cache(files))
	}
	s.routes()
	s.handler = s.mux
	if !static {
		s.handler = s.withLang(s.oneHop(s.mux))
	}
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			log.Printf("panic %s: %v", r.URL.RequestURI(), v)
			s.captureError(r, "panic", v)
			http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
		}
	}()
	release, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer release()
	s.handler.ServeHTTP(w, r)
	if d := time.Since(start); d > 800*time.Millisecond {
		log.Printf("slow %s %s", r.URL.RequestURI(), d.Round(time.Millisecond))
	}
}

func cache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/fonts/") { // never changed under the same name
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		h.ServeHTTP(w, r)
	})
}

// Page is what the layout needs around a page's own content.
type Page struct {
	Title   string
	Desc    string
	Path    string // canonical, from the site root, with leading slash
	NoIndex bool
	OGType  string // "article" for documents; "website" when empty
	LD      []any  // schema.org objects
	Data    any
	Lang    string // the main content's language ("ar" for a text's pages); "en" when empty

	// Translated says an Arabic-content page also has real English content (a
	// principal code's summary, a topic page): it stays self-canonical in both
	// languages with an hreflang pair. Any other page with Lang "ar" is the
	// same Arabic text in both interfaces: the English address names the Arabic
	// one as canonical and the pair carries no hreflang (indexing.go).
	Translated bool
	// Set by render: the canonical path with its /en prefix (none for Arabic), and whether the
	// page is one of an en/ar pair.
	Canon string
	Pair  bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, code int, p Page) {
	out, err := s.execute(r, name, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, code, out)
}

// execute renders a page to its HTML.
func (s *Server) execute(r *http.Request, name string, p Page) (string, error) {
	if s.isStatic {
		p.NoIndex = true
	}
	p.Canon, p.Pair = s.canonical(r, p)
	p.Title, p.Desc = finishMeta(i18n.New(s.lang(r)), p.Title, p.Desc)
	pages := s.pages
	if s.lang(r) == "ar" && s.pagesAr != nil {
		pages = s.pagesAr
	} else if s.pagesAr != nil && p.Pair {
		p.LD = englishLD(p.LD)
	}
	var b strings.Builder
	err := pages[name].ExecuteTemplate(&b, "layout.html", p)
	return b.String(), err
}

func (s *Server) write(w http.ResponseWriter, r *http.Request, code int, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", htmlCacheControl(r, code))
	w.WriteHeader(code)
	fmt.Fprint(w, html)
}

// The edge (Cloudflare) holds a page for up to 12 hours and is purged after
// each nightly load, so the data on a page is as fresh as the last build.
// Browsers keep it five minutes. Search and the form's return page are never
// kept (a search is counted when it is run, and its answer is for one visitor
// to read); a missing page is kept briefly, an error not at all.
const (
	htmlCacheOK  = "public, max-age=300, s-maxage=43200, stale-while-revalidate=300"
	htmlCache404 = "public, max-age=60, s-maxage=600"
)

func htmlCacheControl(r *http.Request, code int) string {
	p := stripLang(r.URL.Path)
	switch {
	case code >= 500, strings.HasPrefix(p, "/search"), r.URL.Query().Has("q"):
		return "no-store"
	case code == http.StatusNotFound:
		return htmlCache404
	case code != http.StatusOK:
		return "no-store"
	}
	return htmlCacheOK
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("error %s: %v", r.URL.RequestURI(), err)
	s.captureError(r, "error", err)
	if overloaded(err) { // out of time or the database away: ask the client to come back, not a bug
		busy(w, busyAfter)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "notfound", http.StatusNotFound, Page{
		Title: s.tr(r, "notfound.title") + " · " + s.tr(r, "brand.name"),
		Desc:  s.tr(r, "notfound.desc"), Path: r.URL.Path, NoIndex: true})
}

// probeCache: nothing lives under /.well-known/. MCP clients ask for OAuth
// metadata there (/.well-known/oauth-authorization-server,
// /.well-known/oauth-protected-resource/mcp, openid-configuration) many
// thousand times a day; a short 404 the edge and the client may keep a day
// answers them without a redirect or a rendered page.
const probeCache = "public, max-age=86400, s-maxage=86400"

func probe404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", probeCache)
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintln(w, "not found: this site needs no sign-in, and its MCP server (POST /mcp) needs no OAuth")
}

// ---------------------------------------------------------------- template funcs

var baseFuncs = template.FuncMap{
	"assetVersion": func() string { return BuildID },
	"paras":        paras,
	"marks":        marks,
	"date":         dmy,
	"slug":         Slug,
	"num":          func(n int) string { return thousands(n) },
	"plural":       plural,
	"add":          func(a, b int) int { return a + b },
	"mul100":       func(f float64) float64 { return f * 100 },
	"lawURL":       lawPath,
	"artURL": func(id int, number string) string {
		return artPath(id, number)
	},
	"rulingsURL": func(id int, number string) string {
		return artPath(id, number) + "rulings/"
	},
	"absURL": func(p string) string { return Base + p },
	// citeLaw and citeArt are the permanent (root, Arabic) address of a text
	// or an article, the one a citation copies whatever the interface.
	"citeLaw":  lawPath,
	"citeArt":  artPath,
	"min":      func(a, b int) int { return min(a, b) },
	"pageNums": pageNums,
	"depth":    func(d int) int { return min(d, 4) },
	"join":     strings.Join,
	"query":    query,
	"safeURL":  func(s string) template.URL { return template.URL(s) },
	"ptr":      func(s string) *string { return &s },
	"str":      str,
	"img":      imageURL,
	"heading":  Heading,
	"deref":    func(n *int) int { return *n },
	"safe":     func(s string) template.HTML { return template.HTML(s) },
	// dict2 hands the shared article block its law, article and whether it
	// stands alone on its own page.
	"dict2": func(law any, a any, standalone bool) map[string]any {
		return map[string]any{"Law": law, "A": a, "Standalone": standalone}
	},

	// ------------------------------------------------------------ language
	// English defaults, unprefixed (a static copy uses them as they are). On
	// the live site the English set is rebound with englishFuncs to prefix
	// internal URLs with /en, and the Arabic set with arabicFuncs (root URLs,
	// strings from ar.json, the direction-dependent details flipped).
	"t":           func(key string, args ...any) template.HTML { return template.HTML(enBundle.T(key, args...)) },
	"u":           func(p string) string { return p },
	"uilang":      func() string { return "en" },
	"dir":         func() string { return "ltr" },
	"switchLabel": func() string { return "العربية" },
	"altURL":      func(p string) string { return Base + p },         // the Arabic version of this same page
	"enURL":       func(p string) string { return Base + "/en" + p }, // always the English absolute URL (hreflang)
	"arURL":       func(p string) string { return Base + p },         // always the Arabic (root) absolute URL (hreflang)
	"enPath":      func(p string) string { return "/en" + p },        // the English path of a page, for the link at the top of every Arabic page
	"linkVerb":    func(k Link) template.HTML { return template.HTML(k.Verb()) },
	"changeVerb":  func(c *Change) template.HTML { return template.HTML(c.Verb()) },
	"statusText": func(status string) template.HTML {
		if status == "repealed" {
			return template.HTML(enBundle.T("status.repealed"))
		}
		return template.HTML(enBundle.T("status.in_force"))
	},
	"backArrow":  func() template.HTML { return "&larr;" },
	"fwdArrow":   func() template.HTML { return "&rarr;" },
	"datasetURL": func() string { return DatasetURL },
}

// paras splits stored text (paragraphs separated by blank lines) for <p>s.
func paras(s *string) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(*s, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dmy(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("02/01/2006")
}

var (
	colonDash = regexp.MustCompile(`\s*:\s*[-:]?\s*-?\s*`)
	doubleDsh = regexp.MustCompile(`\s*-\s*-\s*`)
)

// Heading tidies a chapter heading for display: the Centre prints
// "الكتاب الاول : - في انواع الموجبات" and "1 - - مفاعيل"; we show
// "الكتاب الاول: في انواع الموجبات" and "1 - مفاعيل". The stored text
// stays as printed.
func Heading(h string) string {
	h = colonDash.ReplaceAllString(h, ": ")
	h = doubleDsh.ReplaceAllString(h, " - ")
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(h), ":"))
}

// Slug turns an article number as printed into its address: "12 مكرر" -> "12-مكرر".
func Slug(number string) string { return strings.Join(strings.Fields(number), "-") }

func thousands(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return thousands(n) + " " + many
}

// query rebuilds a list page's query string with one parameter changed
// ("" removes it); the page number always resets unless it is the one set.
func query(q url.Values, key, value string) template.URL {
	c := url.Values{}
	for k, v := range q {
		if k != "page" {
			c[k] = v
		}
	}
	if value == "" {
		c.Del(key)
	} else {
		c.Set(key, value)
	}
	if enc := c.Encode(); enc != "" {
		return template.URL("?" + enc)
	}
	return "?"
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}
