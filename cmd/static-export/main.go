// static-export writes the live Lebanese Legislation site as plain files, by
// calling the site's own handler in process against a Postgres corpus, and
// checks the result against the live site.
//
//	static-export export     -out DIR [-db DSN] [-workers N] [-crawl] [-sample N | -paths FILE | -limit N]
//	static-export sample     -live URL -n N [-seed S] > paths.txt
//	static-export compare    -out DIR -live URL -paths FILE
//	static-export redirects  -out DIR [-db DSN]
//	static-export old-urls   -out DIR -n N [-seed S] > old.txt
//	static-export check-old  -out DIR -live URL -paths old.txt
//
// See docs/static-export.md for the design and the measured results.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
	"github.com/taktekhq/lebanese-legislation/internal/site"
	"github.com/taktekhq/lebanese-legislation/internal/staticexport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "static-export:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: static-export export|sample|compare|old-urls|check-old [flags]")
	}
	switch args[0] {
	case "export":
		return export(ctx, args[1:], stdout, stderr)
	case "sample":
		return sample(ctx, args[1:], stdout)
	case "compare":
		return compare(ctx, args[1:], stdout)
	case "redirects":
		return redirects(ctx, args[1:], stdout, stderr)
	case "old-urls":
		return oldURLs(args[1:], stdout)
	case "check-old":
		return checkOld(ctx, args[1:], stdout)
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func export(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", os.Getenv("LEBLEG_DSN"), "Postgres connection string (default $LEBLEG_DSN; the password comes from PGPASSWORD)")
	out := fs.String("out", "", "directory to write the site into")
	workers := fs.Int("workers", 4, "pages rendered in parallel")
	crawl := fs.Bool("crawl", true, "follow internal links to pages the sitemaps do not list")
	sampleN := fs.Int("sample", 0, "export a random sample of this many sitemap paths instead of everything")
	seed := fs.Uint64("seed", 1, "seed of the sample")
	limit := fs.Int("limit", 0, "stop after this many pages (0: all)")
	paths := fs.String("paths", "", "file of paths, one per line: export exactly these")
	base := fs.String("base", envOr("PUBLIC_URL", "https://lebaneselegislation.com"), "the site's public address, for canonical links and sitemaps (default $PUBLIC_URL)")
	build := fs.String("build", "static", "build id for the asset links (?v=...); use the live one to compare byte for byte")
	searchShell := fs.Bool("search-shell", false, "serve /search/ as a static page that calls the API in the browser (see LEBLEG_API_BASE)")
	r2bucket := fs.String("r2-bucket", "", "upload changed pages to this R2 bucket instead of keeping them on disk (keys in $R2_ACCESS_KEY_ID and $R2_SECRET_ACCESS_KEY)")
	r2endpoint := fs.String("r2-endpoint", os.Getenv("R2_ENDPOINT"), "R2 endpoint, https://<account-id>.r2.cloudflarestorage.com (default $R2_ENDPOINT)")
	r2workers := fs.Int("r2-workers", 96, "uploads in flight")
	maxErrors := fs.Int("max-errors", 0, "pages that may fail before the export counts as failed")
	exchangeTimeout := fs.Duration("exchange-timeout", 30*time.Minute, "maximum wait for shards in each path exchange round (does not limit rendering)")
	exchangeID := fs.String("exchange-id", "", "unique coordinated crawl run id shared by every shard (default GitHub run id and attempt; requires R2)")
	shardArg := fs.String("shard", "", "render only slice i of n of the site, as i/n (for splitting a full export across jobs)")
	meaningNotice := fs.Bool("meaning-notice", false, "render the search forms as the live site does when search by meaning is on (the notice under the box); no request is ever sent to the provider")
	dry := fs.Bool("dry-run", false, "with -r2-bucket: render everything and count what would be uploaded, send nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *exchangeTimeout <= 0 {
		return errors.New("export: -exchange-timeout must be positive")
	}
	if *out == "" || *dsn == "" {
		return errors.New("export: -out and -db (or $LEBLEG_DSN) are required")
	}
	var shard, shards int
	if *shardArg != "" {
		if n, err := fmt.Sscanf(*shardArg, "%d/%d", &shard, &shards); n != 2 || err != nil || shards < 2 || shard < 0 || shard >= shards {
			return fmt.Errorf("export: -shard wants i/n with 0 <= i < n and n >= 2, got %q", *shardArg)
		}
	}
	exchangePrefix := ""
	if shards > 1 && *crawl && *paths == "" && *sampleN == 0 {
		id := *exchangeID
		if id == "" && os.Getenv("GITHUB_RUN_ID") != "" && os.Getenv("GITHUB_RUN_ATTEMPT") != "" {
			id = os.Getenv("GITHUB_RUN_ID") + "-" + os.Getenv("GITHUB_RUN_ATTEMPT")
		}
		if *r2bucket == "" || id == "" || *dry || *limit > 0 {
			return errors.New("sharded crawl requires R2, no page limit or dry run, and a fresh shared -exchange-id (automatic in GitHub Actions); use -crawl=false for independent sitemap-only shards")
		}
		exchangePrefix = fmt.Sprintf("_export/discovery/%s/of-%d", id, shards)
	}

	partial := *paths != "" || *sampleN > 0 || *limit > 0 || shards > 1
	// Each shard keeps its own manifest in the bucket, so shards never overwrite one another's.
	meta := "_export/"
	if shards > 1 {
		meta = fmt.Sprintf("_export/shard-%d-of-%d/", shard, shards)
	}
	if *r2bucket != "" && partial && shards <= 1 && !*dry { // a shard is whole for its own manifest
		return errors.New("export: -r2-bucket needs a full export (no -paths, -sample or -limit) unless -dry-run, or the stored manifest would forget pages")
	}
	pcfg, err := pgxpool.ParseConfig(*dsn)
	if err != nil {
		return err
	}
	pcfg.MaxConns = int32(*workers) + 2
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := os.MkdirAll(filepath.Join(*out, ".export"), 0o755); err != nil {
		return err
	}
	if lf, err := os.Create(filepath.Join(*out, ".export", "server.log")); err == nil { // the site logs slow pages
		defer lf.Close()
		log.SetOutput(lf)
	}
	site.BuildID = *build
	site.Base = strings.TrimRight(*base, "/")
	srv, err := newSite(pool, os.Getenv)
	if err != nil {
		return err
	}
	o := staticexport.Options{Out: *out, Workers: *workers, Crawl: *crawl, Sample: *sampleN, Seed: *seed, Limit: *limit, Shard: shard, Shards: shards,
		Extra:  []string{"/", "/en/", "/robots.txt", "/llms.txt", "/openapi.json", "/ai/", "/en/ai/", "/run-it-yourself/", "/en/run-it-yourself/", "/about/", "/en/about/", "/data/", "/search/", "/en/search/"},
		Assets: site.StaticFiles(),
		Log:    func(f string, a ...any) { fmt.Fprintf(stderr, time.Now().Format("15:04:05 ")+f+"\n", a...) }}
	if *meaningNotice {
		// Only the flag matters for rendering: the Gemini client is never called,
		// because the export makes no search, so the key is a placeholder.
		rw, err := nlsearch.New(nlsearch.Config{Provider: "gemini", Model: "gemini-3.8-flash", Key: "static-export-never-called"})
		if err != nil {
			return err
		}
		srv.SetMeaningSearch(rw, "Google Gemini", false, 1)
	}
	if *paths != "" {
		if o.Paths, err = readLines(*paths); err != nil {
			return err
		}
	}
	if *searchShell {
		o.Transform = staticexport.SearchShell(os.Getenv("LEBLEG_API_BASE"))
		o.Files = staticexport.SearchShellFiles()
	}
	var r2 *staticexport.R2
	var up *staticexport.Uploader
	if *r2bucket != "" {
		if r2, err = staticexport.NewR2(*r2endpoint, *r2bucket, os.Getenv); err != nil {
			if !*dry {
				return err
			}
			fmt.Fprintf(stderr, "dry run without R2 access (%v): every page counts as new\n", err)
			r2 = nil
		}
		o.PrevEntries = map[string]staticexport.Entry{}
		if r2 != nil {
			b, err := r2.Get(ctx, meta+"manifest.jsonl")
			switch {
			case errors.Is(err, staticexport.ErrNotFound):
				fmt.Fprintln(stderr, "no previous manifest in the bucket: a first build uploads everything")
			case err != nil:
				return fmt.Errorf("reading the previous manifest: %w", err)
			default:
				if o.PrevEntries, err = staticexport.ParseManifest(bytes.NewReader(b), meta+"manifest.jsonl"); err != nil {
					return err
				}
			}
		}
		if exchangePrefix != "" {
			o.Exchange = r2.PathExchange(exchangePrefix, shard, shards, *exchangeTimeout)
		}
		up = staticexport.NewUploader(ctx, r2, *r2workers)
		up.DryRun = *dry || r2 == nil
		o.Sink = up.Sink(ctx)
	}
	st, err := staticexport.Run(ctx, srv, o)
	if up != nil {
		if werr := up.Wait(); err == nil {
			err = werr
		}
	}
	if st != nil {
		printStats(stdout, st)
		if up != nil {
			fmt.Fprintf(stdout, "\nuploaded:         %d objects, %.1f MB%s   failed: %d   to delete: %d\n", up.Uploaded.Load(), float64(up.Bytes.Load())/1e6,
				map[bool]string{true: " (dry run, nothing sent)"}[up.DryRun], up.Failed.Load(), len(st.RemovedKeys))
		}
	}
	if err != nil {
		return err
	}
	if st.Errors > int64(*maxErrors) {
		return fmt.Errorf("%d pages failed, first: %s", st.Errors, strings.Join(st.FirstErrs[:min(3, len(st.FirstErrs))], "; "))
	}
	if !partial { // the redirect data needs the whole manifest
		m, err := staticexport.ReadManifest(filepath.Join(*out, ".export", "manifest.jsonl"))
		if err != nil {
			return err
		}
		d, err := staticexport.BuildRedirectData(ctx, m, pool)
		if err != nil {
			return err
		}
		if err := staticexport.WriteRedirectData(filepath.Join(*out, ".export", "redirects.json"), d); err != nil {
			return err
		}
	}
	if r2 != nil && !up.DryRun {
		return publish(ctx, r2, *out, meta, st.RemovedKeys, stdout)
	}
	return nil
}

// publish sends what a run kept on disk (the assets, the 404 page, the search
// script), deletes the pages that are gone, and last stores the manifest, so a
// run that stops half way leaves the previous manifest, and the next run
// repeats what this one did not finish.
func publish(ctx context.Context, r2 *staticexport.R2, out, meta string, removed []string, stdout io.Writer) error {
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	fail := func(err error) {
		mu.Lock()
		if first == nil {
			first = err
		}
		mu.Unlock()
	}
	do := func(f func() error) {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := f(); err != nil {
				fail(err)
			}
		}()
	}
	n := 0
	err := filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && d.Name() == ".export" {
				return filepath.SkipDir
			}
			return err
		}
		rel, _ := filepath.Rel(out, p)
		key := filepath.ToSlash(rel)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		n++
		do(func() error {
			return r2.Put(ctx, key, b, staticexport.PutOptions{ContentType: contentType(key), CacheControl: assetCache(key)})
		})
		return nil
	})
	wg.Wait()
	if err != nil {
		return err
	}
	if first != nil {
		return first
	}
	for _, k := range removed {
		do(func() error { return r2.Delete(ctx, k) })
	}
	wg.Wait()
	if first != nil {
		return first
	}
	for _, name := range []string{"redirects.json", "stats.json", "removed.txt", "manifest.jsonl"} { // the manifest last
		p := filepath.Join(out, ".export", name)
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := r2.Put(ctx, meta+name, b, staticexport.PutOptions{ContentType: contentType(name)}); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "published: %d support files, %d deletions, manifest stored\n", n, len(removed))
	return nil
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".json", ".jsonl":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func assetCache(key string) string {
	if strings.HasPrefix(key, "assets/fonts/") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=300"
}

func printStats(w io.Writer, st *staticexport.Stats) {
	secs := st.Elapsed.Seconds()
	fmt.Fprintf(w, "pages (200):      %d\n", st.Pages)
	fmt.Fprintf(w, "written:          %d   unchanged: %d   removed: %d\n", st.Written, st.Unchanged, st.Removed)
	fmt.Fprintf(w, "redirects:        %d   not found: %d   errors: %d\n", st.Redirects, st.NotFound, st.Errors)
	fmt.Fprintf(w, "raw bytes:        %.1f MB   gzip(6): %.1f MB\n", float64(st.Raw)/1e6, float64(st.Gzip)/1e6)
	fmt.Fprintf(w, "elapsed:          %s   pages/s: %.1f\n", st.Elapsed.Round(time.Second), float64(st.Pages+st.Redirects+st.NotFound)/max(secs, 0.001))
	fmt.Fprintf(w, "peak heap/sys:    %.0f MB / %.0f MB\n", float64(st.PeakHeap)/1e6, float64(st.PeakSys)/1e6)
	kinds := make([]string, 0, len(st.ByKind))
	for k := range st.ByKind {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return st.ByKind[kinds[i]].Raw > st.ByKind[kinds[j]].Raw })
	fmt.Fprintf(w, "\n%-22s %9s %11s %11s %9s %9s\n", "kind", "pages", "raw MB", "gzip MB", "avg raw", "avg gz")
	for _, k := range kinds {
		s := st.ByKind[k]
		fmt.Fprintf(w, "%-22s %9d %11.1f %11.1f %8.1fK %8.1fK\n", k, s.Pages, float64(s.Raw)/1e6, float64(s.Gzip)/1e6,
			float64(s.Raw)/float64(s.Pages)/1024, float64(s.Gzip)/float64(s.Pages)/1024)
	}
}

func sample(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	live := fs.String("live", "https://lebaneselegislation.com", "live site")
	n := fs.Int("n", 2000, "paths to draw from the live sitemaps")
	seed := fs.Uint64("seed", 1, "seed")
	pause := fs.Duration("pause", 300*time.Millisecond, "pause between requests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ps, err := staticexport.NewClient(*live, 1, *pause).SamplePaths(ctx, *n, *seed)
	if err != nil {
		return err
	}
	for _, p := range ps {
		fmt.Fprintln(stdout, p)
	}
	return nil
}

func compare(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	out := fs.String("out", "", "export directory")
	live := fs.String("live", "https://lebaneselegislation.com", "live site")
	paths := fs.String("paths", "", "file of paths to compare")
	workers := fs.Int("workers", 3, "live requests in parallel")
	pause := fs.Duration("pause", 150*time.Millisecond, "pause between a worker's requests")
	examples := fs.Int("examples", 2, "examples shown per category")
	sigTop := fs.Int("groups", 25, "difference groups to show")
	fromDir := fs.String("from-dir", "", "compare with live pages kept by an earlier run's -save-live, without asking the live site")
	saveLive := fs.String("save-live", "", "keep the live pages in this directory, for studying the differences offline")
	maxDiff := fs.Int("max-diff", -1, "fail when more than this many pages differ in more than the Cloudflare edge rewrites (-1: only report)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ps, err := readLines(*paths)
	if err != nil {
		return err
	}
	m, err := staticexport.ReadManifest(filepath.Join(*out, ".export", "manifest.jsonl"))
	if err != nil {
		return err
	}
	read := func(e staticexport.Entry) ([]byte, error) {
		return os.ReadFile(filepath.Join(*out, filepath.FromSlash(e.File)))
	}
	cl := staticexport.NewClient(*live, *workers, *pause)
	cl.FromDir = *fromDir
	if *saveLive != "" {
		if err := os.MkdirAll(*saveLive, 0o755); err != nil {
			return err
		}
		cl.SaveDir = *saveLive
	}
	rs := cl.Report(ctx, ps, m, read)
	byCat := map[string][]staticexport.Result{}
	for _, r := range rs {
		key := strings.Join(r.Cats, " + ")
		byCat[key] = append(byCat[key], r)
	}
	fmt.Fprintf(stdout, "compared %d pages against %s\n\n", len(rs), *live)
	counts := map[string]int{}
	for k, v := range byCat {
		counts[k] = len(v)
	}
	fmt.Fprint(stdout, staticexport.FormatCounts(counts))
	cats := make([]string, 0, len(byCat))
	for k := range byCat {
		if k != staticexport.CatIdentical {
			cats = append(cats, k)
		}
	}
	sort.Strings(cats)
	for _, c := range cats {
		fmt.Fprintf(stdout, "\n== %s (%d)\n", c, len(byCat[c]))
		for i, r := range byCat[c] {
			if i >= *examples {
				break
			}
			fmt.Fprintf(stdout, "  %s %s\n  %s\n", r.Path, r.Detail, r.Excerpt)
		}
	}
	bySig := map[string][]staticexport.Result{}
	for _, r := range rs {
		if r.Sig != "" {
			bySig[r.Sig] = append(bySig[r.Sig], r)
		}
	}
	if len(bySig) > 0 {
		sigs := make([]string, 0, len(bySig))
		for k := range bySig {
			sigs = append(sigs, k)
		}
		sort.Slice(sigs, func(i, j int) bool {
			return len(bySig[sigs[i]]) > len(bySig[sigs[j]]) || len(bySig[sigs[i]]) == len(bySig[sigs[j]]) && sigs[i] < sigs[j]
		})
		fmt.Fprintf(stdout, "\n== differences grouped by what differs (%d groups)\n", len(sigs))
		for i, k := range sigs {
			if i >= *sigTop {
				break
			}
			fmt.Fprintf(stdout, "\n%5d pages  e.g. %s\n    %s\n", len(bySig[k]), bySig[k][0].Path, k)
		}
	}
	bad := 0
	for k, v := range byCat {
		if k != staticexport.CatIdentical && k != staticexport.CatEdge {
			bad += len(v)
		}
	}
	if *maxDiff >= 0 && bad > *maxDiff {
		return fmt.Errorf("%d of %d pages differ from the live site beyond the edge rewrites (allowed: %d)", bad, len(rs), *maxDiff)
	}
	return nil
}

// redirects writes <out>/.export/redirects.json from the sitemaps alone, for
// checking the table without a full export.
func redirects(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("redirects", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("db", os.Getenv("LEBLEG_DSN"), "Postgres connection string (default $LEBLEG_DSN)")
	out := fs.String("out", "", "export directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	srv, err := site.New(pool)
	if err != nil {
		return err
	}
	ps, err := staticexport.SitemapPaths(ctx, srv, "lebaneselegislation.com")
	if err != nil {
		return err
	}
	m := map[string]staticexport.Entry{}
	for _, p := range ps {
		m[p] = staticexport.Entry{Path: p, Status: 200}
	}
	d, err := staticexport.BuildRedirectData(ctx, m, pool)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d sitemap paths: %d decree ranges, %d text ranges, %d aliases\n", len(ps), len(d.Decree)/2, len(d.Text)/2, len(d.Aliases))
	return staticexport.WriteRedirectData(filepath.Join(*out, ".export", "redirects.json"), d)
}

func oldURLs(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("old-urls", flag.ContinueOnError)
	out := fs.String("out", "", "export directory")
	n := fs.Int("n", 300, "addresses to derive")
	seed := fs.Uint64("seed", 1, "seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, d, err := loadTable(*out)
	if err != nil {
		return err
	}
	for _, p := range staticexport.OldVariants(m, d, *n, *seed) {
		fmt.Fprintln(stdout, p)
	}
	return nil
}

func checkOld(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("check-old", flag.ContinueOnError)
	out := fs.String("out", "", "export directory")
	live := fs.String("live", "https://lebaneselegislation.com", "live site")
	paths := fs.String("paths", "", "file of old addresses")
	workers := fs.Int("workers", 3, "live requests in parallel")
	pause := fs.Duration("pause", 150*time.Millisecond, "pause between a worker's requests")
	examples := fs.Int("examples", 3, "examples shown per category")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ps, err := readLines(*paths)
	if err != nil {
		return err
	}
	m, d, err := loadTable(*out)
	if err != nil {
		return err
	}
	exists := func(p string) bool { e, ok := m[p]; return ok && e.Status == 200 }
	rs := staticexport.CheckOld(ctx, staticexport.NewClient(*live, *workers, *pause), ps, d, exists)
	fmt.Fprintf(stdout, "checked %d old addresses\n\n", len(rs))
	fmt.Fprint(stdout, staticexport.FormatCounts(staticexport.Summary(rs, func(r staticexport.OldResult) string { return r.Category })))
	shown := map[string]int{}
	for _, r := range rs {
		if r.Category == staticexport.OldMatch || shown[r.Category] >= *examples {
			continue
		}
		shown[r.Category]++
		fmt.Fprintf(stdout, "\n[%s]\n  %s  live=%d %s\n  table=%s\n", r.Category, r.Path, r.Live, r.LiveTo, r.Table)
	}
	return nil
}

func loadTable(out string) (map[string]staticexport.Entry, *staticexport.RedirectData, error) {
	m, err := staticexport.ReadManifest(filepath.Join(out, ".export", "manifest.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	d, err := staticexport.ReadRedirectData(filepath.Join(out, ".export", "redirects.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("%w (a full export writes it)", err)
	}
	return m, d, nil
}

// newSite is the site the export renders. Its pages are what visitors read,
// so they load the browser analytics (GA4, PostHog) exactly as the live
// server does (internal/app): on unless ANALYTICS=off.
func newSite(pool *pgxpool.Pool, getenv func(string) string) (*site.Server, error) {
	srv, err := site.New(pool)
	if err != nil {
		return nil, err
	}
	srv.SetAnalytics(getenv("ANALYTICS") != "off")
	return srv, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func readLines(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}
