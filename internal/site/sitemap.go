package site

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// The sitemaps list what is worth indexing, and say when it last changed.
//
//   - The file names keep their "ar-" marks from when Arabic lived under /ar/:
//     ar-*.xml list the Arabic (root) addresses, the others the /en/ addresses
//     of pages with English of their own.
//   - core.xml and ar-core.xml are small: the list pages, the principal codes,
//     the most-read texts (Arabic only) and the topic pages. Submit these first.
//   - pages.xml and ar-pages.xml hold the year lists and the other list pages.
//   - ar-laws-N, ar-articles-N, ar-rulings-N, ar-gazette-N, ar-documents-N hold
//     the Arabic address of every text. laws-N holds the /en/ address of every
//     text's own page (same rows, same lastmod as its Arabic twin): those are
//     self-canonical with an hreflang pair. There are no English files for
//     articles, rulings, gazette or documents: an article's /en/ twin is
//     found through the hreflang on its Arabic page; the others carry the
//     same Arabic text and name the Arabic one as canonical (indexing.go).
//
// Left out: article pages with nothing to index and placeholder documents
// (noindex_page), the second of two articles whose numbers make the same
// address. lastmod is page_state.changed_at: when the page's content last
// changed here, never the text's own date. A page whose date is not known, or
// is before the page existed or after today, has no lastmod.

const sitemapSize = 45000 // under the 50,000 URLs a sitemap may hold

// sitemapSet is one family of Arabic URLs, split into numbered files.
type sitemapSet struct {
	name string
	rows string // SQL returning (path, lastmod), stably ordered; {ps} and {ni} are the page_state and noindex_page tables
}

var sitemapSets = []sitemapSet{
	{"laws", `SELECT '/' || CASE WHEN l.kind LIKE 'مرسوم%' THEN 'decree' WHEN l.kind LIKE 'قانون%' OR l.kind IN ('الدستور', 'دستور') THEN 'law' ELSE 'text' END
		    || '/' || l.id || '/', ps.changed_at
		 FROM legislation l LEFT JOIN {ps} ps ON ps.kind = 'law' AND ps.id = l.id ORDER BY l.id`},
	{"articles", `SELECT a.law_id::text || chr(31) || a.number, ps.changed_at
		 FROM (SELECT DISTINCT ON (law_id, regexp_replace(btrim(number), '\s+', '-', 'g')) law_id, position, number
		       FROM legislation_article ORDER BY law_id, regexp_replace(btrim(number), '\s+', '-', 'g'), position) a
		 LEFT JOIN {ni} n ON n.kind = 'article' AND n.id = a.law_id AND n.position = a.position
		 LEFT JOIN {ps} ps ON ps.kind = 'law' AND ps.id = a.law_id
		 WHERE n.kind IS NULL ORDER BY a.law_id, a.position`},
	{"rulings", `SELECT '/ruling/' || r.id || '/', ps.changed_at FROM ruling r
		 LEFT JOIN {ps} ps ON ps.kind = 'ruling' AND ps.id = r.id
		 WHERE NOT EXISTS (SELECT 1 FROM {ni} n WHERE n.kind = 'ruling' AND n.id = r.id) ORDER BY r.id`},
	{"gazette", `SELECT '/gazette/' || g.id || '/', ps.changed_at FROM gazette_issue g
		 LEFT JOIN {ps} ps ON ps.kind = 'gazette' AND ps.id = g.id ORDER BY g.id`},
	{"documents", `SELECT path, changed FROM (
		   SELECT 1 AS k, o.id, '/opinion/' || o.id || '/' AS path, ps.changed_at AS changed FROM opinion o
		     LEFT JOIN {ps} ps ON ps.kind = 'opinion' AND ps.id = o.id
		     WHERE NOT EXISTS (SELECT 1 FROM {ni} n WHERE n.kind = 'opinion' AND n.id = o.id)
		   UNION ALL SELECT 2, a.id, '/agreement/' || a.id || '/', ps.changed_at FROM agreement a
		     LEFT JOIN {ps} ps ON ps.kind = 'agreement' AND ps.id = a.id
		     WHERE NOT EXISTS (SELECT 1 FROM {ni} n WHERE n.kind = 'agreement' AND n.id = a.id)
		   UNION ALL SELECT 3, d.id, '/doctrine/' || d.id || '/', ps.changed_at FROM doctrine_article d
		     LEFT JOIN {ps} ps ON ps.kind = 'doctrine' AND ps.id = d.id
		     WHERE NOT EXISTS (SELECT 1 FROM {ni} n WHERE n.kind = 'doctrine' AND n.id = d.id)
		   UNION ALL SELECT 4, x.id, '/research/' || x.id || '/', ps.changed_at FROM research x
		     LEFT JOIN {ps} ps ON ps.kind = 'research' AND ps.id = x.id
		     WHERE NOT EXISTS (SELECT 1 FROM {ni} n WHERE n.kind = 'research' AND n.id = x.id)
		 ) d ORDER BY k, id`},
}

// SitemapURL is one entry; Mod is the zero time when the date is not known.
type SitemapURL struct {
	Path string
	Mod  time.Time
}

// AddCoreSitemap lets another part of the site (the topic pages) put its
// addresses in core.xml. fn returns root paths ("/topics/tax/") and their
// lastmod (zero when unknown); each is listed in both languages.
func (s *Server) AddCoreSitemap(fn func(ctx context.Context) ([]SitemapURL, error)) {
	s.coreExtra = append(s.coreExtra, fn)
}

// indexTables names the two tables the sitemaps join, or an empty stand-in for
// a database from before them, so a new binary on an old database lists every
// page (as the sitemaps once did) rather than failing.
func (s *Server) indexTables(ctx context.Context) *strings.Replacer {
	have := func(name, empty string) string {
		var ok bool
		if err := s.db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&ok); err == nil && ok {
			return name
		}
		return empty
	}
	return strings.NewReplacer(
		"{ps}", have("page_state", `(SELECT NULL::text AS kind, NULL::int AS id, NULL::timestamptz AS changed_at WHERE false)`),
		"{ni}", have("noindex_page", `(SELECT NULL::text AS kind, NULL::int AS id, NULL::int AS position WHERE false)`))
}

// sets shape: per file, how many URLs and the newest change in it.
type fileInfo struct {
	N   int
	Mod time.Time
}

// setFiles measures a set: its files, in one pass over its rows.
func (s *Server) setFiles(ctx context.Context, set sitemapSet) ([]fileInfo, error) {
	q := s.indexTables(ctx).Replace(set.rows)
	rows, err := s.db.Query(ctx, fmt.Sprintf(`SELECT f, count(*)::int, max(m) FROM (
		SELECT (row_number() OVER () - 1) / %d AS f, m FROM (%s) r(p, m)) x GROUP BY f ORDER BY f`, sitemapSize, q))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (fileInfo, error) {
		var f int
		var fi fileInfo
		var mod *time.Time
		err := row.Scan(&f, &fi.N, &mod)
		if mod != nil {
			fi.Mod = *mod
		}
		return fi, err
	})
}

// memoCache holds a computed value for a few minutes: building an index of
// 400,000 articles takes seconds, and crawlers ask often.
type memoCache struct {
	mu    sync.Mutex
	m     map[string]memoEntry
	locks map[string]*sync.Mutex // one build at a time per key (getVersioned)
}

type memoEntry struct {
	v   any
	at  time.Time
	ver string
}

func (c *memoCache) get(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	c.mu.Lock()
	if e, ok := c.m[key]; ok && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]memoEntry{}
	}
	c.m[key] = memoEntry{v, time.Now(), ""}
	c.mu.Unlock()
	return v, nil
}

// dataVersion is a cheap fingerprint of the tables the sitemaps list (rows
// inserted, updated or deleted, from Postgres's own counters): it changes when
// a data load changes anything, so a memoized sitemap is rebuilt after the next
// load and not before. "" when it cannot be read (the memo then expires by its TTL alone).
func (s *Server) dataVersion(ctx context.Context) string {
	var v string
	err := s.db.QueryRow(ctx, `SELECT coalesce(sum(n_tup_ins + n_tup_upd + n_tup_del), 0)::text FROM pg_stat_user_tables
		WHERE relname IN ('legislation', 'legislation_article', 'ruling', 'opinion', 'agreement', 'doctrine_article', 'research',
		                  'gazette_issue', 'code', 'page_state', 'noindex_page', 'legislation_title_en')`).Scan(&v)
	if err != nil {
		return ""
	}
	return v
}

// sitemapMemo keeps a built sitemap in memory until the data changes (see
// dataVersion) or a day passes. A crawler's cold request used to wait for
// the build (up to 30 s on the articles' neighbours); now only the first one
// after a load does, and simultaneous first requests share one build. The
// build runs on its own context, so a client that gives up does not cancel it.
func (s *Server) sitemapMemo(ctx context.Context, key string, build func(context.Context) (any, error)) (any, error) {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	return s.memo.getVersioned(key, s.dataVersion(bctx), 24*time.Hour, func() (any, error) { return build(bctx) })
}

// getVersioned is get that also expires when ver changes; one build per key at a time.
func (c *memoCache) getVersioned(key, ver string, ttl time.Duration, fn func() (any, error)) (any, error) {
	c.mu.Lock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	kl := c.locks[key]
	if kl == nil {
		kl = &sync.Mutex{}
		c.locks[key] = kl
	}
	c.mu.Unlock()
	kl.Lock()
	defer kl.Unlock()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && time.Since(e.at) < ttl && e.ver == ver {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]memoEntry{}
	}
	c.m[key] = memoEntry{v, time.Now(), ver}
	c.mu.Unlock()
	return v, nil
}

// firstFetch is the day the scraper first fetched the Centre's pages: no page
// here existed, so none changed, before it.
var firstFetch = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

// lastmod is t as a sitemap date, or "" when t cannot be when the page last
// changed: unknown, before the page existed (a text's own date, which Search
// Console counted as 113,297 errors on 28 Oct 2026) or after today.
func lastmod(t time.Time) string {
	if t.IsZero() || t.Before(firstFetch) {
		return ""
	}
	d := t.UTC().Format("2006-01-02")
	if d > time.Now().UTC().Format("2006-01-02") {
		return ""
	}
	return d
}

func (s *Server) sitemapIndex(w http.ResponseWriter, r *http.Request) {
	v, err := s.sitemapMemo(r.Context(), "index", func(ctx context.Context) (any, error) { return s.buildIndex(ctx) })
	if err != nil {
		s.fail(w, r, err)
		return
	}
	xmlOut(w, v.(string))
}

func (s *Server) buildIndex(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	entry := func(path string, mod time.Time) {
		b.WriteString("<sitemap><loc>" + Base + path + "</loc>")
		if m := lastmod(mod); m != "" {
			b.WriteString("<lastmod>" + m + "</lastmod>")
		}
		b.WriteString("</sitemap>\n")
	}
	for _, name := range []string{"core", "ar-core", "pages", "ar-pages"} {
		urls, err := s.smallSitemap(ctx, strings.TrimPrefix(name, "ar-"), strings.HasPrefix(name, "ar-"))
		if err != nil {
			return "", err
		}
		var newest time.Time
		for _, u := range urls {
			if u.Mod.After(newest) {
				newest = u.Mod
			}
		}
		entry("/sitemaps/"+name+".xml", newest)
	}
	entry("/blog-sitemap.xml", blogSitemapRows()[0].Mod)
	for _, set := range sitemapSets {
		files, err := s.setFiles(ctx, set)
		if err != nil {
			return "", err
		}
		for i, f := range files {
			entry(fmt.Sprintf("/sitemaps/ar-%s-%d.xml", set.name, i+1), f.Mod)
			if set.name == "laws" {
				entry(fmt.Sprintf("/sitemaps/laws-%d.xml", i+1), f.Mod)
			}
		}
	}
	b.WriteString("</sitemapindex>\n")
	return b.String(), nil
}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	name := strings.TrimSuffix(r.PathValue("name"), ".xml")
	arabic := strings.HasPrefix(name, "ar-")
	name = strings.TrimPrefix(name, "ar-")
	var urls []SitemapURL
	switch name {
	case "core", "pages":
		var err error
		if urls, err = s.smallSitemap(ctx, name, arabic); err != nil {
			s.fail(w, r, err)
			return
		}
	default:
		dash := strings.LastIndex(name, "-")
		n, err := strconv.Atoi(name[dash+1:])
		// the English files hold only the laws set: a text's page has an English twin of its own
		if dash < 0 || err != nil || n < 1 || (!arabic && name[:dash] != "laws") {
			s.notFound(w, r)
			return
		}
		var set *sitemapSet
		for i := range sitemapSets {
			if sitemapSets[i].name == name[:dash] {
				set = &sitemapSets[i]
			}
		}
		if set == nil {
			s.notFound(w, r)
			return
		}
		q := s.indexTables(ctx).Replace(set.rows)
		rows, err := s.db.Query(ctx, fmt.Sprintf("%s LIMIT %d OFFSET %d", q, sitemapSize, (n-1)*sitemapSize))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		urls, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SitemapURL, error) {
			var p string
			var t *time.Time
			if err := row.Scan(&p, &t); err != nil {
				return SitemapURL{}, err
			}
			if set.name == "articles" { // law id and article number, joined by a unit separator
				law, number, _ := strings.Cut(p, "\x1f")
				id, _ := strconv.Atoi(law)
				p = artPath(id, number)
			}
			u := SitemapURL{Path: p}
			if t != nil {
				u.Mod = *t
			}
			return u, nil
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if len(urls) == 0 {
			s.notFound(w, r)
			return
		}
	}
	prefix := "/en" // the English files list /en/ addresses, the ar- files the root (Arabic) ones
	if arabic {
		prefix = ""
	}
	xmlOut(w, urlset(prefix, urls))
}

// urlset writes a sitemap file.
func urlset(prefix string, urls []SitemapURL) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		b.WriteString("<url><loc>")
		xml.EscapeText(&b, []byte(Base+prefix+u.Path))
		b.WriteString("</loc>")
		if m := lastmod(u.Mod); m != "" {
			b.WriteString("<lastmod>" + m + "</lastmod>")
		}
		b.WriteString("</url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// smallSitemap is core.xml or pages.xml, in English (/en/) or Arabic (root). The English
// files list only pages that are their own canonical in English: the
// interface pages, the codes and the topics. The Arabic ones add the most-read
// texts to core.
func (s *Server) smallSitemap(ctx context.Context, name string, arabic bool) ([]SitemapURL, error) {
	v, err := s.sitemapMemo(ctx, fmt.Sprintf("small:%s:%t", name, arabic), func(ctx context.Context) (any, error) {
		return s.buildSmallSitemap(ctx, name, arabic)
	})
	if err != nil {
		return nil, err
	}
	return v.([]SitemapURL), nil
}

func (s *Server) buildSmallSitemap(ctx context.Context, name string, arabic bool) ([]SitemapURL, error) {
	mods, err := s.listMods(ctx)
	if err != nil {
		return nil, err
	}
	var urls []SitemapURL
	if name == "core" {
		for _, p := range []string{"/", "/codes/", "/laws/", "/rulings/", "/gazette/", "/opinions/", "/agreements/", "/doctrine/",
			"/research/", "/changes/", "/laws/years/", "/rulings/years/", "/gazette/years/"} {
			urls = append(urls, SitemapURL{p, mods[p]})
		}
		rows, err := s.db.Query(ctx, `SELECT c.law_id FROM code c ORDER BY c.position`)
		if err != nil {
			return nil, err
		}
		codes, err := pgx.CollectRows(rows, pgx.RowTo[int])
		if err != nil {
			return nil, err
		}
		ids := codes
		if arabic {
			read, err := s.mostRead(ctx, 300)
			if err != nil {
				return nil, err
			}
			ids = append(ids, read...)
		}
		stamp, err := s.lawMods(ctx, ids)
		if err != nil {
			return nil, err
		}
		seen := map[int]bool{}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				urls = append(urls, SitemapURL{lawPath(id), stamp[id]})
			}
		}
		for _, fn := range s.coreExtra {
			extra, err := fn(ctx)
			if err != nil {
				return nil, err
			}
			urls = append(urls, extra...)
		}
		return urls, nil
	}
	// pages: the rest of the interface pages, the year lists and their later pages
	for _, p := range []string{"/data/", "/ai/", "/about/"} {
		urls = append(urls, SitemapURL{Path: p})
	}
	for _, k := range []string{"laws", "rulings", "gazette"} {
		ys, err := s.Years(ctx, k)
		if err != nil {
			return nil, err
		}
		for _, y := range ys {
			base := fmt.Sprintf("/%s/year/%d/", k, y.Year)
			urls = append(urls, SitemapURL{base, mods[base]})
			for n := 2; n <= (y.Count+yearPageSize-1)/yearPageSize; n++ {
				urls = append(urls, SitemapURL{Path: pagePath(base, n)})
			}
		}
	}
	// the lists of opinions, agreements, doctrine and research: later pages are
	// the only way to their documents
	for _, k := range []string{"opinion", "agreement", "doctrine", "research"} {
		kind := docKinds[k]
		var total int
		if err := s.db.QueryRow(ctx, "SELECT count(*) FROM "+kind.table).Scan(&total); err != nil {
			return nil, err
		}
		for n := 2; n <= (total+perPage-1)/perPage; n++ {
			urls = append(urls, SitemapURL{Path: pagePath("/"+kind.list+"/", n)})
		}
	}
	return urls, nil
}

// listMods is the newest change under each list page and each year's list.
func (s *Server) listMods(ctx context.Context) (map[string]time.Time, error) {
	v, err := s.memo.get("listmods", 10*time.Minute, func() (any, error) {
		t := s.indexTables(ctx)
		out := map[string]time.Time{}
		set := func(path string, m *time.Time) {
			if m != nil {
				out[path] = *m
			}
		}
		rows, err := s.db.Query(ctx, t.Replace(`SELECT kind, max(changed_at) FROM {ps} GROUP BY kind`))
		if err != nil {
			return nil, err
		}
		kinds := map[string]*time.Time{}
		for rows.Next() {
			var k string
			var m *time.Time
			if err := rows.Scan(&k, &m); err != nil {
				return nil, err
			}
			kinds[k] = m
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for path, kind := range map[string]string{"/": "law", "/laws/": "law", "/laws/years/": "law", "/changes/": "law", "/codes/": "law",
			"/rulings/": "ruling", "/rulings/years/": "ruling", "/gazette/": "gazette", "/gazette/years/": "gazette",
			"/opinions/": "opinion", "/agreements/": "agreement", "/doctrine/": "doctrine", "/research/": "research"} {
			set(path, kinds[kind])
		}
		for _, q := range []struct{ kind, sql string }{
			{"laws", `SELECT coalesce(extract(year FROM l.issued_on)::int, 0), max(ps.changed_at) FROM legislation l
				JOIN {ps} ps ON ps.kind = 'law' AND ps.id = l.id GROUP BY 1`},
			{"rulings", `SELECT coalesce(r.year, 0)::int, max(ps.changed_at) FROM ruling r
				JOIN {ps} ps ON ps.kind = 'ruling' AND ps.id = r.id GROUP BY 1`},
			{"gazette", `SELECT coalesce(extract(year FROM g.published_on)::int, 0), max(ps.changed_at) FROM gazette_issue g
				JOIN {ps} ps ON ps.kind = 'gazette' AND ps.id = g.id GROUP BY 1`},
		} {
			rows, err := s.db.Query(ctx, t.Replace(q.sql))
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var y int
				var m *time.Time
				if err := rows.Scan(&y, &m); err != nil {
					return nil, err
				}
				set(fmt.Sprintf("/%s/year/%d/", q.kind, y), m)
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]time.Time), nil
}

// lawMods is when each of these texts last changed.
func (s *Server) lawMods(ctx context.Context, ids []int) (map[int]time.Time, error) {
	rows, err := s.db.Query(ctx, s.indexTables(ctx).Replace(`SELECT id, changed_at FROM {ps} WHERE kind = 'law' AND id = ANY($1)`), ids)
	if err != nil {
		return nil, err
	}
	out := map[int]time.Time{}
	for rows.Next() {
		var id int
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// mostRead are the texts people read most in the last two weeks (the usage
// record, people only), then the most cited by rulings and other texts, up to n.
func (s *Server) mostRead(ctx context.Context, n int) ([]int, error) {
	var ids []int
	seen := map[int]bool{}
	add := func(id int) {
		if id > 0 && !seen[id] && len(ids) < n {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if s.ev != nil {
		reads := map[string]int{}
		for d := 0; d < 14; d++ {
			st, err := UsageStats(s.ev.dir, time.Now().UTC().AddDate(0, 0, -d).Format("2006-01-02"))
			if err != nil {
				continue
			}
			for _, c := range st.TopLaws {
				reads[c.Key] += c.N
			}
		}
		for _, c := range top(reads, nil, n) {
			id, _ := strconv.Atoi(c.Key)
			add(id)
		}
	}
	rows, err := s.db.Query(ctx, `SELECT x.law_id FROM reference x JOIN legislation l ON l.id = x.law_id
		GROUP BY x.law_id ORDER BY count(*) DESC, x.law_id LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	cited, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, err
	}
	for _, id := range cited {
		add(id)
	}
	return ids, nil
}

// sitemapCacheControl: the sitemaps change once a night, when the edge is purged
// and the deploy fetches them again, so the edge may hold them for
// 12 hours; crawlers and browsers ask again after an hour.
const sitemapCacheControl = "public, max-age=3600, s-maxage=43200"

func xmlOut(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", sitemapCacheControl)
	fmt.Fprint(w, body)
}
