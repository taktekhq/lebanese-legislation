package provenance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/parse"
)

// SourceStore returns the archived text a source names, as text.
type SourceStore interface {
	Text(kind, id string) (string, error)
}

var (
	// ErrNotInArchive: the source names nothing the archive holds.
	ErrNotInArchive = errors.New("not in archive")
	// ErrChanged: the file's bytes are not the ones its manifest recorded.
	ErrChanged = errors.New("source file changed")
	// ErrUnknownKind: no such kind of source.
	ErrUnknownKind = errors.New("unknown source kind")
	// ErrNoManifest: the SaderLex bundle has no manifest, so none of its
	// files can be trusted (it also Is ErrNotInArchive).
	ErrNoManifest = fmt.Errorf("no bundle manifest: %w", ErrNotInArchive)
)

// ManifestPath is where a SaderLex bundle folder's manifest is kept: beside
// the folder, named after it (../lebgazette-bundle.manifest.tsv for
// ../lebgazette-bundle). One line per file, its path inside the bundle and
// the SHA-256 of its bytes, recorded the first time the nightly sees it
// (record-scan-manifest, RecordManifest). It sits outside the folder
// because the nightly replaces the folder with a fresh copy
// every night: a manifest inside it would be lost with it, and its hashes
// re-recorded from whatever bytes came next.
func ManifestPath(bundle string) string {
	return filepath.Clean(bundle) + ".manifest.tsv"
}

// ArchiveSources reads sources from the raw archive the fetchers stored.
//
//   - ul, id lawtext/<law id>: the Centre's page, found through the
//     archive's index (archive/lawtext*.tsv), checked against the SHA-256
//     the index recorded, and turned to text by internal/parse.
//   - saderlex, id <SaderLex id>: the bundle folder's meta.json and
//     text.json, checked against the SHA-256 the bundle's manifest
//     recorded when it first arrived, and cut into articles the way the
//     build cuts them.
//   - amendment, id <amending law id>/<article>: the amending article,
//     read from its law's own ul or saderlex source.
//
// An edit has no archived text: it passes by its hash (Check).
type ArchiveSources struct {
	Root     string // the archive/ directory
	Bundle   string // the lebgazette sader-bundle folder
	Manifest string // the bundle's manifest; empty: ManifestPath(Bundle)

	once     sync.Once
	index    map[int]archive.Entry // lawtext pages
	indexErr error

	monce       sync.Once
	manifest    map[string]string
	manifestErr error

	mu   sync.Mutex
	laws map[string][]srcArticle // parsed sources, a few at a time
	lru  []string
}

type srcArticle struct{ number, ar, fr string }

// Text returns a source's text: its articles' wordings, one after another.
func (a *ArchiveSources) Text(kind, id string) (string, error) {
	var arts []srcArticle
	var err error
	switch kind {
	case build.SourceUL, build.SourceSaderlex:
		arts, err = a.articles(kind, id)
	case build.SourceAmendment:
		arts, err = a.amending(id)
	default:
		return "", fmt.Errorf("%s/%s: %w", kind, id, ErrUnknownKind)
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, x := range arts {
		b.WriteString(x.ar)
		b.WriteString("\n")
		if x.fr != "" {
			b.WriteString(x.fr)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// amending is an amending article: the articles with that number in its
// law's own source.
func (a *ArchiveSources) amending(id string) ([]srcArticle, error) {
	law, number, ok := strings.Cut(id, "/")
	n, err := strconv.Atoi(law)
	if !ok || err != nil || n <= 0 || number == "" {
		return nil, fmt.Errorf("amendment/%s: %w", id, ErrNotInArchive)
	}
	kind, sid := build.LawSource(n)
	arts, err := a.articles(kind, sid)
	if err != nil {
		return nil, err
	}
	var out []srcArticle
	for _, x := range arts {
		if x.number == number {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("amendment/%s: no article %s in %s/%s: %w", id, number, kind, sid, ErrNotInArchive)
	}
	return out, nil
}

// articles parses a law's source, keeping the last few parsed: an amending
// law's articles are read one after another.
func (a *ArchiveSources) articles(kind, id string) ([]srcArticle, error) {
	key := kind + "/" + id
	a.mu.Lock()
	if arts, ok := a.laws[key]; ok {
		a.mu.Unlock()
		return arts, nil
	}
	a.mu.Unlock()
	var arts []srcArticle
	var err error
	if kind == build.SourceUL {
		arts, err = a.ul(id)
	} else {
		arts, err = a.saderlex(id)
	}
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.laws == nil {
		a.laws = map[string][]srcArticle{}
	}
	if _, ok := a.laws[key]; !ok {
		a.laws[key] = arts
		a.lru = append(a.lru, key)
		if len(a.lru) > 32 {
			delete(a.laws, a.lru[0])
			a.lru = a.lru[1:]
		}
	}
	return arts, nil
}

func (a *ArchiveSources) ul(id string) ([]srcArticle, error) {
	typ, num, _ := strings.Cut(id, "/")
	n, err := strconv.Atoi(num)
	if typ != "lawtext" || err != nil {
		return nil, fmt.Errorf("ul/%s: %w", id, ErrNotInArchive)
	}
	a.once.Do(func() {
		var es []archive.Entry
		es, a.indexErr = archive.Archive{Dir: a.Root}.Index("lawtext")
		a.index = make(map[int]archive.Entry, len(es))
		for _, e := range es {
			a.index[e.ID] = e
		}
	})
	if a.indexErr != nil {
		return nil, fmt.Errorf("ul/%s: archive index: %v: %w", id, a.indexErr, ErrNotInArchive)
	}
	e, ok := a.index[n]
	if !ok {
		return nil, fmt.Errorf("ul/%s: %w", id, ErrNotInArchive)
	}
	raw, err := archive.Archive{Dir: a.Root}.Read("lawtext", n)
	if err != nil {
		return nil, fmt.Errorf("ul/%s: %v: %w", id, err, ErrNotInArchive)
	}
	if sum(raw) != e.SHA256 {
		return nil, fmt.Errorf("ul/%s: %w", id, ErrChanged)
	}
	t, err := parse.ParseLawText(raw)
	if err != nil {
		return nil, fmt.Errorf("ul/%s: %v: %w", id, err, ErrNotInArchive)
	}
	out := make([]srcArticle, len(t.Articles))
	for i, x := range t.Articles {
		out[i] = srcArticle{number: x.Number, ar: x.TextAr}
		if x.TextFr != nil {
			out[i].fr = *x.TextFr
		}
	}
	return out, nil
}

func (a *ArchiveSources) saderlex(id string) ([]srcArticle, error) {
	if n, err := strconv.Atoi(id); err != nil || n <= 0 || strconv.Itoa(n) != id {
		return nil, fmt.Errorf("saderlex/%s: %w", id, ErrNotInArchive)
	}
	a.monce.Do(func() { a.manifest, a.manifestErr = ReadManifest(a.manifestPath()) })
	if errors.Is(a.manifestErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("saderlex/%s: %s: %w", id, a.manifestPath(), ErrNoManifest)
	}
	if a.manifestErr != nil {
		return nil, fmt.Errorf("saderlex/%s: bundle manifest: %v: %w", id, a.manifestErr, ErrNotInArchive)
	}
	var files [2][]byte
	for i, name := range []string{"meta.json", "text.json"} {
		rel := id + "/" + name
		want, ok := a.manifest[rel]
		if !ok {
			return nil, fmt.Errorf("saderlex/%s: %s not in the manifest: %w", id, rel, ErrNotInArchive)
		}
		raw, err := os.ReadFile(filepath.Join(a.Bundle, id, name))
		if err != nil {
			return nil, fmt.Errorf("saderlex/%s: %v: %w", id, err, ErrNotInArchive)
		}
		if sum(raw) != want {
			return nil, fmt.Errorf("saderlex/%s: %s: %w", id, rel, ErrChanged)
		}
		files[i] = raw
	}
	numbers, texts, err := build.ScanArticles(files[0], files[1])
	if err != nil {
		return nil, fmt.Errorf("saderlex/%s: %v: %w", id, err, ErrNotInArchive)
	}
	out := make([]srcArticle, len(texts))
	for i := range texts {
		out[i] = srcArticle{number: numbers[i], ar: texts[i]}
	}
	return out, nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (a *ArchiveSources) manifestPath() string {
	if a.Manifest != "" {
		return a.Manifest
	}
	return ManifestPath(a.Bundle)
}

// ReadManifest reads a bundle manifest: path, then SHA-256.
func ReadManifest(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		t := sc.Text()
		if t == "" || strings.HasPrefix(t, "#") || t == "path\tsha256" {
			continue
		}
		p, h, ok := strings.Cut(t, "\t")
		if !ok || len(h) != 64 {
			return nil, fmt.Errorf("%s line %d unreadable", filepath.Base(path), line)
		}
		out[p] = h
	}
	return out, sc.Err()
}

// ManifestResult is what RecordManifest did.
type ManifestResult struct {
	Added    int      // files recorded for the first time
	Kept     int      // files recorded before, their bytes unchanged
	Accepted int      // files re-recorded because a person named their bundle
	Changed  []string // files recorded before whose bytes differ now: their old hash is kept
}

// RecordManifest records in manifest the SHA-256 of every file of every
// bundle in the folder (<id>/meta.json, text.json, source.pdf, page-*.png)
// that it does not hold yet. The nightly runs it right after it copies the
// folder, so a bundle is pinned the first night it arrives
// (trust on first use: the lebgazette fetcher, which this repository does
// not control, writes no manifest of its own). The scan's PDF and page images
// are pinned with the text read from them; the guard itself reads only
// meta.json and text.json.
//
// A hash once recorded is never rewritten: a recorded file whose bytes
// changed since is listed in Changed and keeps its old hash, so the guard
// reads it as "source file changed", until a person who has checked the new
// bytes names its bundle (the folder, a SaderLex id) in accept. A recorded
// file no longer on disk keeps its line. Files at the folder's top level
// (issues.tsv, .version) are not a bundle's and are left out.
func RecordManifest(bundle, manifest string, accept []string) (ManifestResult, error) {
	var r ManifestResult
	old, err := ReadManifest(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		old, err = map[string]string{}, nil
	}
	if err != nil {
		return r, err
	}
	ok := map[string]bool{}
	for _, a := range accept {
		ok[strings.TrimSpace(a)] = true
	}
	var paths []string
	err = filepath.WalkDir(bundle, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(bundle, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.Count(rel, "/") != 1 {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return r, err
	}
	next := make(map[string]string, len(old)+len(paths))
	for p, h := range old {
		next[p] = h
	}
	for _, p := range paths {
		now, err := sumFile(filepath.Join(bundle, filepath.FromSlash(p)))
		if err != nil {
			return r, err
		}
		h, had := old[p]
		dir, _, _ := strings.Cut(p, "/")
		switch {
		case !had:
			r.Added++
		case h == now:
			r.Kept++
		case ok[dir]:
			r.Accepted++
		default:
			r.Changed = append(r.Changed, p)
			continue
		}
		next[p] = now
	}
	keys := make([]string, 0, len(next))
	for p := range next {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("path\tsha256\n")
	for _, p := range keys {
		fmt.Fprintf(&b, "%s\t%s\n", p, next[p])
	}
	tmp := manifest + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return r, err
	}
	return r, os.Rename(tmp, manifest)
}

// sumFile is a file's SHA-256, read a block at a time (a scan's PDF can be
// large).
func sumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
