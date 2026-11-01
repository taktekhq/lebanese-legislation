package site

import (
	"bytes"
	"context"
	"encoding/xml"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The shard files (ar-articles-4.xml and the others) used to run
// "ORDER BY ... LIMIT 45000 OFFSET n" on every request that missed the edge:
// 4.4 s each on average on the Hetzner box (100 of them in three days, 439 s
// of Postgres between them), and the cold /sitemap.xml ran five more passes
// to count the rows (34 and 39 s on 1 Nov 2026). A crawler asking for the
// whole index at once pinned both CPUs.
//
// Now each family (laws, articles, rulings, gazette, documents) is read once,
// in one pass, and cut into its numbered files as the rows arrive; the
// finished files are kept in memory until the data changes (dataVersion), so
// a shard request, and the index, are memory reads.
//   - One build runs at a time, across families (one Postgres connection).
//   - Simultaneous first requests share one build.
//   - After a data load the old files are served while the new ones build
//     behind them: a sitemap that is a few minutes old does no harm, a crawler
//     kept waiting does.
//   - A failed build is not retried for setRetryAfter, so a database in
//     trouble is not asked the same expensive question over and over.

const (
	setMaxAge     = 24 * time.Hour
	setBuildLimit = 5 * time.Minute
	setRetryAfter = 30 * time.Second
	versionTTL    = time.Minute
)

// sitemapShard is one numbered file of a family: its URL count, its newest
// change, and its XML, rendered for the root (Arabic) addresses and, for the
// laws family only, for the /en/ twins.
type sitemapShard struct {
	n   int
	mod time.Time
	ar  []byte
	en  []byte
}

type setEntry struct {
	shards   []sitemapShard
	ver      string
	at       time.Time
	building bool
	done     chan struct{} // closed when the first build ends; nil once there are shards
	err      error
	failedAt time.Time
}

// setCache holds the built families. The zero value is ready to use.
type setCache struct {
	mu      sync.Mutex
	m       map[string]*setEntry
	one     chan struct{} // capacity 1: the build slot
	version string
	vat     time.Time
}

// urlHead and urlFoot open and close a sitemap file.
const (
	urlHead = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n"
	urlFoot = "</urlset>\n"
)

// writeURL appends one <url> entry; the same text urlset has always written.
func writeURL(b *bytes.Buffer, prefix, path string, mod time.Time) {
	b.WriteString("<url><loc>")
	_ = xml.EscapeText(b, []byte(Base+prefix+path))
	b.WriteString("</loc>")
	if m := lastmod(mod); m != "" {
		b.WriteString("<lastmod>" + m + "</lastmod>")
	}
	b.WriteString("</url>\n")
}

// shardWriter cuts a stream of rows into files of at most size URLs.
type shardWriter struct {
	size   int
	withEN bool
	out    []sitemapShard
	cur    *sitemapShard
	ar, en bytes.Buffer
}

func newShardWriter(size int, withEN bool) *shardWriter {
	return &shardWriter{size: size, withEN: withEN}
}

func (w *shardWriter) add(path string, mod time.Time) {
	if w.cur != nil && w.cur.n == w.size {
		w.flush()
	}
	if w.cur == nil {
		w.cur = &sitemapShard{}
		w.ar.WriteString(urlHead)
		if w.withEN {
			w.en.WriteString(urlHead)
		}
	}
	writeURL(&w.ar, "", path, mod)
	if w.withEN {
		writeURL(&w.en, "/en", path, mod)
	}
	w.cur.n++
	if mod.After(w.cur.mod) {
		w.cur.mod = mod
	}
}

func (w *shardWriter) flush() {
	if w.cur == nil {
		return
	}
	w.ar.WriteString(urlFoot)
	w.cur.ar = append([]byte(nil), w.ar.Bytes()...)
	w.ar.Reset()
	if w.withEN {
		w.en.WriteString(urlFoot)
		w.cur.en = append([]byte(nil), w.en.Bytes()...)
		w.en.Reset()
	}
	w.out = append(w.out, *w.cur)
	w.cur = nil
}

func (w *shardWriter) finish() []sitemapShard {
	w.flush()
	return w.out
}

// buildSetFromDB reads one family in a single ordered pass.
func (s *Server) buildSetFromDB(ctx context.Context, set sitemapSet) ([]sitemapShard, error) {
	rows, err := s.db.Query(ctx, s.indexTables(ctx).Replace(set.rows))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	w := newShardWriter(sitemapSize, set.name == "laws")
	for rows.Next() {
		var p string
		var t *time.Time
		if err := rows.Scan(&p, &t); err != nil {
			return nil, err
		}
		if set.name == "articles" { // law id and article number, joined by a unit separator
			law, number, _ := strings.Cut(p, "\x1f")
			id, _ := strconv.Atoi(law)
			p = artPath(id, number)
		}
		var mod time.Time
		if t != nil {
			mod = *t
		}
		w.add(p, mod)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return w.finish(), nil
}

// dataVersionCached is dataVersion, asked at most once a minute. When it
// cannot be read the last answer stands, so a database in trouble does not
// look like a data load.
func (s *Server) dataVersionCached(ctx context.Context) string {
	c := &s.sets
	c.mu.Lock()
	if !c.vat.IsZero() && time.Since(c.vat) < versionTTL {
		v := c.version
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()
	vctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v := s.versionOf(vctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if v != "" || c.vat.IsZero() {
		c.version = v
	}
	c.vat = time.Now()
	return c.version
}

func (s *Server) versionOf(ctx context.Context) string {
	if s.versionFn != nil {
		return s.versionFn(ctx)
	}
	return s.dataVersion(ctx)
}

func (s *Server) builderOf() func(context.Context, sitemapSet) ([]sitemapShard, error) {
	if s.buildSet != nil {
		return s.buildSet
	}
	return s.buildSetFromDB
}

// setShards returns the files of a family: from memory, or (the very first
// time) after waiting for the build, for as long as ctx allows.
func (s *Server) setShards(ctx context.Context, set sitemapSet) ([]sitemapShard, error) {
	ver := s.dataVersionCached(ctx)
	c := &s.sets
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*setEntry{}
		c.one = make(chan struct{}, 1)
	}
	e := c.m[set.name]
	if e == nil {
		e = &setEntry{}
		c.m[set.name] = e
	}
	if e.shards != nil {
		if (e.ver != ver || time.Since(e.at) > setMaxAge) && !e.building && time.Since(e.failedAt) > setRetryAfter {
			e.building = true
			go s.refreshSet(set, e, ver)
		}
		shards := e.shards
		c.mu.Unlock()
		return shards, nil
	}
	if !e.building {
		if e.err != nil && time.Since(e.failedAt) < setRetryAfter {
			err := e.err
			c.mu.Unlock()
			return nil, err
		}
		e.building = true
		e.done = make(chan struct{})
		go s.refreshSet(set, e, ver)
	}
	done := e.done
	c.mu.Unlock()
	select {
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return e.shards, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// refreshSet builds a family on its own context (a client that gives up does
// not cancel it), one family at a time.
func (s *Server) refreshSet(set sitemapSet, e *setEntry, ver string) {
	c := &s.sets
	c.one <- struct{}{}
	bctx, cancel := context.WithTimeout(context.Background(), setBuildLimit)
	start := time.Now()
	shards, err := s.builderOf()(bctx, set)
	cancel()
	<-c.one
	c.mu.Lock()
	defer c.mu.Unlock()
	e.building = false
	if err != nil {
		log.Printf("sitemap %s: build failed after %s: %v", set.name, time.Since(start).Round(time.Millisecond), err)
		e.err, e.failedAt = err, time.Now()
	} else {
		n := 0
		for _, sh := range shards {
			n += sh.n
		}
		log.Printf("sitemap %s: %d files, %d URLs in %s", set.name, len(shards), n, time.Since(start).Round(time.Millisecond))
		e.shards, e.ver, e.at, e.err = shards, ver, time.Now(), nil
	}
	if e.done != nil {
		close(e.done)
		e.done = nil
	}
}

// WarmSitemaps builds every family once, a little after start, so the first
// crawler after a deploy finds them ready. It returns when they are built or
// ctx ends.
func (s *Server) WarmSitemaps(ctx context.Context) {
	select {
	case <-time.After(20 * time.Second):
	case <-ctx.Done():
		return
	}
	for _, set := range sitemapSets {
		if _, err := s.setShards(ctx, set); err != nil {
			log.Printf("sitemap warm %s: %v", set.name, err)
		}
	}
}
