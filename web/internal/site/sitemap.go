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

const sitemapSize = 45000

type sitemapSet struct {
	name string
	rows string
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

type SitemapURL struct {
	Path string
	Mod  time.Time
}

func (s *Server) AddCoreSitemap(fn func(ctx context.Context) ([]SitemapURL, error)) {
	s.coreExtra = append(s.coreExtra, fn)
}

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

type fileInfo struct {
	N   int
	Mod time.Time
}

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

type memoCache struct {
	mu    sync.Mutex
	m     map[string]memoEntry
	locks map[string]*sync.Mutex
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

func (s *Server) sitemapMemo(ctx context.Context, key string, build func(context.Context) (any, error)) (any, error) {
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	return s.memo.getVersioned(key, s.dataVersion(bctx), 24*time.Hour, func() (any, error) { return build(bctx) })
}

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

var firstFetch = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

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
	entry := func(name string, mod time.Time) {
		b.WriteString("<sitemap><loc>" + Base + "/sitemaps/" + name + ".xml</loc>")
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
		entry(name, newest)
	}
	for _, set := range sitemapSets {
		files, err := s.setFiles(ctx, set)
		if err != nil {
			return "", err
		}
		for i, f := range files {
			entry(fmt.Sprintf("ar-%s-%d", set.name, i+1), f.Mod)
			if set.name == "laws" {
				entry(fmt.Sprintf("laws-%d", i+1), f.Mod)
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
			if set.name == "articles" {
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
	prefix := "/en"
	if arabic {
		prefix = ""
	}
	xmlOut(w, urlset(prefix, urls))
}

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
			"/research/", "/changes/", "/contribute/", "/laws/years/", "/rulings/years/", "/gazette/years/"} {
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

func (s *Server) mostRead(ctx context.Context, n int) ([]int, error) {
	var ids []int
	seen := map[int]bool{}
	add := func(id int) {
		if id > 0 && !seen[id] && len(ids) < n {
			seen[id] = true
			ids = append(ids, id)
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

const sitemapCacheControl = "public, max-age=3600, s-maxage=43200"

func xmlOut(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", sitemapCacheControl)
	fmt.Fprint(w, body)
}
