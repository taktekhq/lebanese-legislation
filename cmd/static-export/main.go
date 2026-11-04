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
//	static-export indexnow   [-all] [-max N] [-dry-run] [CHANGED_DIR...]
//
// See docs/static-export.md for the design and the measured results.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
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
	staticexport.LayoutSchemas = site.LayoutSchemas
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "static-export:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: static-export export|changes|sample|compare|redirects|old-urls|check-old|indexnow [flags]")
	}
	switch args[0] {
	case "changes":
		return changes(ctx, args[1:], stdout, stderr)
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
	case "indexnow":
		return indexNow(ctx, args[1:], stdout, stderr)
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
	edge := fs.String("edge-layout", "auto", "store HTML pages as their own parts, for the Worker to put into the shared frame (docs/static-export.md): on, off, or auto (with -r2-bucket, on once the live Worker answers <base>/_build/layout; off without)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *edge != "on" && *edge != "off" && *edge != "auto" {
		return fmt.Errorf("export: -edge-layout wants on, off or auto, got %q", *edge)
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
	if *r2bucket != "" && (*sampleN > 0 || *limit > 0) && shards <= 1 && !*dry { // a shard is whole for its own manifest
		return errors.New("export: unsharded -r2-bucket does not support -sample or -limit unless -dry-run")
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
	edgeOn, err := edgeLayoutOn(ctx, *edge, *r2bucket != "", site.Base, stderr)
	if err != nil {
		return err
	}
	o := staticexport.Options{Out: *out, Workers: *workers, Crawl: *crawl, Sample: *sampleN, Seed: *seed, Limit: *limit, Shard: shard, Shards: shards,
		Extra:  []string{"/", "/en/", "/robots.txt", "/llms.txt", "/openapi.json", "/ai/", "/en/ai/", "/run-it-yourself/", "/en/run-it-yourself/", "/about/", "/en/about/", "/data/", "/search/", "/en/search/"},
		Assets: site.StaticFiles(),
		Record: site.RecordDeps, Group: srv.Route,
		Log: func(f string, a ...any) { fmt.Fprintf(stderr, time.Now().Format("15:04:05 ")+f+"\n", a...) }}
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
			// What each route's pages used, for the planner (changes -r2-bucket).
			switch b, err := r2.Get(ctx, meta+"deps.json"); {
			case errors.Is(err, staticexport.ErrNotFound):
				o.PrevDeps = staticexport.Deps{}
			case err != nil:
				return fmt.Errorf("reading the previous deps.json: %w", err)
			default:
				if o.PrevDeps, err = staticexport.ParseDeps(b, meta+"deps.json"); err != nil {
					return err
				}
			}
		}
		if exchangePrefix != "" {
			o.Exchange = r2.PathExchange(exchangePrefix, shard, shards, *exchangeTimeout)
		}
		if *paths != "" {
			o.Paths, err = staticexport.ExpandChangedPaths(srv, o.Paths, o.PrevEntries)
			if err != nil {
				return err
			}
		}
		up = staticexport.NewUploader(ctx, r2, *r2workers)
		up.DryRun = *dry || r2 == nil
		o.Sink = up.Sink(ctx)
	}
	if edgeOn {
		// The frames go up before any page: a page in parts that finds no
		// frame is answered by the origin, never shown as parts. Only one
		// shard writes the list of frames, so shards never race on it.
		writer := r2
		if up != nil && up.DryRun || shards > 1 && shard != 0 {
			writer = nil
		}
		if o.Layout, err = publishLayout(ctx, srv, writer, *out, stdout); err != nil {
			return err
		}
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
	for _, name := range []string{"redirects.json", "stats.json", "removed.txt", "deps.json", "manifest.jsonl"} { // the manifest last
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

// edgeLayoutOn resolves -edge-layout. Pages in parts are garbage to a Worker
// that cannot put them together, so auto asks the live Worker first: a 404
// means it is older, and pages are stored whole as before. Any other answer
// stops the export rather than guess, so one network error cannot rewrite
// every page in the other form.
func edgeLayoutOn(ctx context.Context, mode string, r2 bool, base string, stderr io.Writer) (bool, error) {
	switch {
	case mode == "on":
		return true, nil
	case mode == "off", !r2:
		return false, nil
	}
	u := strings.TrimRight(base, "/") + "/_build/layout"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false, fmt.Errorf("edge layout: asking the Worker (%s): %w; pass -edge-layout on or off", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	switch {
	case resp.StatusCode == http.StatusOK && strings.HasPrefix(string(b), "LLP1"):
		fmt.Fprintln(stderr, "edge layout: the Worker puts pages together; storing pages in parts")
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		fmt.Fprintln(stderr, "edge layout: the live Worker predates it; storing pages whole")
		return false, nil
	}
	return false, fmt.Errorf("edge layout: %s answered %d; pass -edge-layout on or off", u, resp.StatusCode)
}

// publishLayout writes this build's frames under <out>/.export (_layout/ and
// layout.json, as in the bucket) and, when r2 is set, stores in the bucket
// any frame the bucket's list does not have, then the list. A build whose
// frames are already there writes nothing.
func publishLayout(ctx context.Context, srv *site.Server, r2 *staticexport.R2, out string, stdout io.Writer) (*staticexport.Layout, error) {
	frames, err := srv.LayoutFrames()
	if err != nil {
		return nil, err
	}
	layout, err := staticexport.NewLayout(frames)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(out, ".export")
	var refs []staticexport.LayoutRef
	bodies := map[string][]byte{}
	for _, f := range frames {
		b, id := staticexport.FrameObject(f)
		refs = append(refs, staticexport.LayoutRef{Key: f.Lang + "/" + f.Schema, ID: id})
		bodies[id] = b
		if err := os.MkdirAll(filepath.Join(dir, "_layout"), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(staticexport.FrameKey(id))), b, 0o644); err != nil {
			return nil, err
		}
	}
	var prev []byte
	if r2 != nil {
		prev, err = r2.Get(ctx, staticexport.LayoutPointerKey)
		if err != nil && !errors.Is(err, staticexport.ErrNotFound) {
			return nil, fmt.Errorf("reading %s: %w", staticexport.LayoutPointerKey, err)
		}
	} else if b, err := os.ReadFile(filepath.Join(dir, "layout.json")); err == nil {
		prev = b
	}
	ptr, changed, err := staticexport.MergeLayoutPointer(prev, refs)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), ptr, 0o644); err != nil {
		return nil, err
	}
	if r2 == nil || !changed {
		return layout, nil
	}
	known := map[string]bool{}
	if len(prev) > 0 {
		var p staticexport.LayoutPointer
		if err := json.Unmarshal(prev, &p); err == nil {
			for _, r := range p.Layouts {
				known[r.ID] = true
			}
		}
	}
	for _, r := range refs {
		if known[r.ID] {
			continue
		}
		if err := r2.Put(ctx, staticexport.FrameKey(r.ID), bodies[r.ID], staticexport.PutOptions{ContentType: "application/json", CacheControl: "public, max-age=31536000, immutable"}); err != nil {
			return nil, err
		}
	}
	if err := r2.Put(ctx, staticexport.LayoutPointerKey, ptr, staticexport.PutOptions{ContentType: "application/json"}); err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "layout: stored %d frames and %s\n", len(refs), staticexport.LayoutPointerKey)
	return layout, nil
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
	if st.Parts > 0 {
		fmt.Fprintf(w, "in parts:         %d pages   stored: %.1f MB\n", st.Parts, float64(st.Stored)/1e6)
	}
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
	layout, err := staticexport.ReadLayoutDir(filepath.Join(*out, ".export"))
	if err != nil {
		return err
	}
	read := func(e staticexport.Entry) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(*out, filepath.FromSlash(e.File)))
		if err != nil {
			return nil, err
		}
		return layout.Join(b) // a page in parts, put back into its frame
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
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// changes writes a reusable plan; no database or R2 access is required.
func changes(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.String("since", "", "last successfully rendered git commit; empty means full")
	full := fs.Bool("full", false, "force a full render")
	out := fs.String("out", "plan", "plan directory")
	r2bucket := fs.String("r2-bucket", "", "bucket holding the shards' manifests and render records, to map changed templates, strings and topic guides to pages (without it they render everything)")
	r2endpoint := fs.String("r2-endpoint", os.Getenv("R2_ENDPOINT"), "R2 endpoint (default $R2_ENDPOINT)")
	shards := fs.Int("shards", 24, "shards whose manifests and records to read")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := staticexport.PlanChanges(ctx, ".", strings.TrimSpace(*since), *full)
	if err != nil {
		return err
	}
	var why []string
	if p.Why != "" {
		why = append(why, p.Why)
	}
	assets := false
	if !p.Full && len(p.Content) > 0 {
		cp, err := planContent(ctx, strings.TrimSpace(*since), p.Content, *r2bucket, *r2endpoint, *shards)
		if err != nil {
			return err
		}
		why = append(why, cp.Why...)
		if cp.Full {
			p = staticexport.ChangePlan{Full: true}
		} else {
			seen := map[string]bool{}
			for _, q := range p.Paths {
				seen[q] = true
			}
			for _, q := range cp.Paths {
				if !seen[q] {
					seen[q] = true
					p.Paths = append(p.Paths, q)
				}
			}
			sort.Strings(p.Paths)
			assets = cp.Assets
		}
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	mode := "paths"
	if p.Full {
		mode = "full"
	}
	if err := os.WriteFile(filepath.Join(*out, "mode"), []byte(mode+"\n"), 0644); err != nil {
		return err
	}
	body := strings.Join(p.Paths, "\n")
	if len(p.Paths) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(filepath.Join(*out, "paths.txt"), []byte(body), 0644); err != nil {
		return err
	}
	if assets { // no page changes, but the run must upload the assets
		if err := os.WriteFile(filepath.Join(*out, "assets"), nil, 0644); err != nil {
			return err
		}
	}
	whyText := ""
	if len(why) > 0 {
		whyText = strings.Join(why, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(*out, "why.txt"), []byte(whyText), 0644); err != nil {
		return err
	}
	for _, w := range why {
		fmt.Fprintln(stdout, w)
	}
	fmt.Fprintf(stdout, "%s: %d paths\n", mode, len(p.Paths))
	return nil
}

// planContent maps changed templates, strings, topic guides and assets to
// pages, from every shard's manifest and render record in the bucket.
func planContent(ctx context.Context, since string, files []string, bucket, endpoint string, shards int) (staticexport.ContentPlan, error) {
	if bucket == "" {
		return staticexport.ContentPlan{Full: true, Why: []string{"content changed and no -r2-bucket to read the render record from: render everything"}}, nil
	}
	r2, err := staticexport.NewR2(endpoint, bucket, os.Getenv)
	if err != nil {
		return staticexport.ContentPlan{}, err
	}
	manifest := map[string]staticexport.Entry{}
	deps := staticexport.Deps{}
	for i := 0; i < shards; i++ {
		meta := fmt.Sprintf("_export/shard-%d-of-%d/", i, shards)
		b, err := r2.Get(ctx, meta+"deps.json")
		if errors.Is(err, staticexport.ErrNotFound) {
			return staticexport.ContentPlan{Full: true, Why: []string{fmt.Sprintf("shard %d has no render record yet: render everything once", i)}}, nil
		}
		if err != nil {
			return staticexport.ContentPlan{}, fmt.Errorf("%sdeps.json: %w", meta, err)
		}
		d, err := staticexport.ParseDeps(b, meta+"deps.json")
		if err != nil {
			return staticexport.ContentPlan{}, err
		}
		deps.Merge(d)
		if b, err = r2.Get(ctx, meta+"manifest.jsonl"); err != nil {
			return staticexport.ContentPlan{}, fmt.Errorf("%smanifest.jsonl: %w", meta, err)
		}
		m, err := staticexport.ParseManifest(bytes.NewReader(b), meta+"manifest.jsonl")
		if err != nil {
			return staticexport.ContentPlan{}, err
		}
		for p, e := range m {
			manifest[p] = e
		}
	}
	srv, err := site.New(nil) // only its routes: no page is rendered here
	if err != nil {
		return staticexport.ContentPlan{}, err
	}
	return staticexport.PlanContent(ctx, staticexport.ContentInput{Dir: ".", Since: since, Files: files, Deps: deps, Manifest: manifest, Route: srv.Route})
}

// indexNow submits to IndexNow the pages one render changed. Each argument is
// a directory holding an export's changed.txt and removed.txt (one per shard
// artifact). With -all it submits every URL in the live sitemaps instead,
// sitemap order (core pages first), for a first backfill.
func indexNow(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("indexnow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "https://lebaneselegislation.com", "the live site")
	endpoint := fs.String("endpoint", "https://api.indexnow.org/indexnow", "IndexNow endpoint")
	key := fs.String("key", site.IndexNowKey, "IndexNow key, served at BASE/KEY.txt")
	all := fs.Bool("all", false, "submit every sitemap URL, not the changed pages")
	skip := fs.Int("skip", 0, "with -all, leave out the first N sitemap URLs (sent on an earlier day)")
	max := fs.Int("max", 50000, "submit at most this many URLs (0 means no limit)")
	dry := fs.Bool("dry-run", false, "print the URLs, send nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	host, err := staticexport.HostOf(*base)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	get := func(ctx context.Context, u string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
	listed, err := staticexport.SitemapURLs(ctx, strings.TrimRight(*base, "/")+"/sitemap.xml", get)
	if err != nil {
		return err
	}
	var urls []string
	if *all {
		urls = listed[min(*skip, len(listed)):]
		if *max > 0 && len(urls) > *max {
			urls = urls[:*max]
		}
	} else {
		inSitemap := make(map[string]bool, len(listed))
		for _, u := range listed {
			inSitemap[u] = true
		}
		var changed, removed []string
		for _, dir := range fs.Args() {
			for name, dst := range map[string]*[]string{"changed.txt": &changed, "removed.txt": &removed} {
				lines, err := readLines(filepath.Join(dir, name))
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				*dst = append(*dst, lines...)
			}
		}
		sort.Strings(changed)
		sort.Strings(removed)
		urls = staticexport.IndexNowURLs(*base, changed, removed, inSitemap, *max)
		fmt.Fprintf(stderr, "indexnow: %d changed pages, %d removed, %d in the sitemaps\n", len(changed), len(removed), len(listed))
	}
	if *dry {
		for _, u := range urls {
			fmt.Fprintln(stdout, u)
		}
		return nil
	}
	n := &staticexport.IndexNow{Endpoint: *endpoint, Host: host, Key: *key, Client: client}
	results, err := n.Submit(ctx, urls)
	sent := 0
	for _, r := range results {
		fmt.Fprintf(stdout, "indexnow %d urls status %d tries %d %s\n", r.URLs, r.Status, r.Tries, r.Body)
		if r.Status == http.StatusOK || r.Status == http.StatusAccepted {
			sent += r.URLs
		}
	}
	fmt.Fprintf(stdout, "indexnow: sent %d of %d URLs\n", sent, len(urls))
	return err
}
