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

	"github.com/taktekhq/lebanese-legislation/web/internal/site/i18n"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var Base = "http://localhost:8080"

var BuildID = "unknown"

type Server struct {
	mcpRegistered bool
	vector        vectorSearcher
	meaning       *meaningSearch
	scache        searchCache
	db            *pgxpool.Pool
	pages         map[string]*template.Template
	pagesAr       map[string]*template.Template
	handler       http.Handler
	mux           *http.ServeMux
	isStatic      bool
	scanDir       string
	rulingDir     string
	titles        titlesEn
	vocab         atomic.Pointer[vocab]

	ipLim  *ipLimiter
	dbSem  *dbSemaphore
	ttlLRU *ttlCache

	memo      memoCache
	coreExtra []func(ctx context.Context) ([]SitemapURL, error)
}

func New(db *pgxpool.Pool) (*Server, error) { return newServer(db, false) }

func NewStatic(db *pgxpool.Pool) (*Server, error) { return newServer(db, true) }

func StaticFiles() fs.FS { f, _ := fs.Sub(staticFS, "static"); return f }

func newServer(db *pgxpool.Pool, static bool) (*Server, error) {
	if db != nil {

		if err := loadTextWords(context.Background(), db); err != nil {
			return nil, err
		}
	}
	s := &Server{db: db, pages: map[string]*template.Template{}, mux: http.NewServeMux(), isStatic: static,
		ipLim: newIPLimiter(60, 20), dbSem: newDBSemaphore(4), ttlLRU: newTTLCache(256, mcpCacheTTL)}
	if db != nil && !static {
		go func() {
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
		if base == "layout" || strings.HasPrefix(base, "_") {
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
	if !static {

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
	for _, p := range []string{"/styles.css", "/legislation.css", "/main.js", "/legislation.js", "/assets/"} {
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
	s.handler.ServeHTTP(w, r)
	if d := time.Since(start); d > 800*time.Millisecond {
		log.Printf("slow %s %s", r.URL.RequestURI(), d.Round(time.Millisecond))
	}
}

func cache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		h.ServeHTTP(w, r)
	})
}

type Page struct {
	Title   string
	Desc    string
	Path    string
	NoIndex bool
	OGType  string
	LD      []any
	Data    any
	Lang    string

	Translated bool

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

const (
	htmlCacheOK  = "public, max-age=300, s-maxage=43200, stale-while-revalidate=300"
	htmlCache404 = "public, max-age=60, s-maxage=600"
)

func htmlCacheControl(r *http.Request, code int) string {
	p := stripLang(r.URL.Path)
	switch {
	case code >= 500, strings.HasPrefix(p, "/search"), r.URL.Query().Has("q"), r.URL.Query().Has("notify"):
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
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "notfound", http.StatusNotFound, Page{
		Title: s.tr(r, "notfound.title") + " · " + s.tr(r, "brand.name"),
		Desc:  s.tr(r, "notfound.desc"), Path: r.URL.Path, NoIndex: true})
}

const probeCache = "public, max-age=86400, s-maxage=86400"

func probe404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", probeCache)
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintln(w, "not found: this site needs no sign-in, and its MCP server (POST /mcp) needs no OAuth")
}

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

	"dict2": func(law any, a any, standalone bool) map[string]any {
		return map[string]any{"Law": law, "A": a, "Standalone": standalone}
	},

	"t":           func(key string, args ...any) template.HTML { return template.HTML(enBundle.T(key, args...)) },
	"u":           func(p string) string { return p },
	"uilang":      func() string { return "en" },
	"dir":         func() string { return "ltr" },
	"switchLabel": func() string { return "العربية" },
	"altURL":      func(p string) string { return Base + p },
	"enURL":       func(p string) string { return Base + "/en" + p },
	"arURL":       func(p string) string { return Base + p },
	"enPath":      func(p string) string { return "/en" + p },
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

func Heading(h string) string {
	h = colonDash.ReplaceAllString(h, ": ")
	h = doubleDsh.ReplaceAllString(h, " - ")
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(h), ":"))
}

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
