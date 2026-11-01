package provenance

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/parse"
)

func fixtureSources() *ArchiveSources {
	return &ArchiveSources{Root: "testdata/archive", Bundle: "testdata/bundle"}
}

// The UL source is the page's articles as internal/parse reads them, so the
// build's own text of every article is a span of it.
func TestULSource(t *testing.T) {
	src := fixtureSources()
	for _, id := range []int{8225, 9307, 326577, 327389} {
		raw, err := archive.Archive{Dir: "testdata/archive"}.Read("lawtext", id)
		if err != nil {
			t.Fatal(err)
		}
		lt, err := parse.ParseLawText(raw)
		if err != nil {
			t.Fatal(err)
		}
		var texts []ArticleText
		for _, a := range lt.Articles {
			texts = append(texts, ArticleText{LawID: int64(id), Article: a.Number, Text: a.TextAr, Source: Source{build.SourceUL, "lawtext/" + strconv.Itoa(id)}})
		}
		if len(texts) == 0 {
			t.Fatalf("law %d: no articles in the fixture page", id)
		}
		if vs := Check(texts, src, ApprovedEdits{}, Known{}); len(vs) != 0 {
			t.Errorf("law %d: %v", id, vs)
		}
	}
	if _, err := src.Text(build.SourceUL, "lawtext/1"); !errors.Is(err, ErrNotInArchive) {
		t.Errorf("a page not in the index: %v", err)
	}
	if _, err := src.Text(build.SourceUL, "law/8225"); !errors.Is(err, ErrNotInArchive) {
		t.Errorf("a page of another type: %v", err)
	}
}

// A page whose bytes are not the ones the archive's index recorded.
func TestULPageChanged(t *testing.T) {
	root := copyDir(t, "testdata/archive")
	p := filepath.Join(root, "lawtext", "0008", "8225.html")
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, append(raw, ' '), 0o644)
	src := &ArchiveSources{Root: root}
	if _, err := src.Text(build.SourceUL, "lawtext/8225"); !errors.Is(err, ErrChanged) {
		t.Fatalf("want source file changed, got %v", err)
	}
	vs := Check([]ArticleText{{LawID: 8225, Article: "1", Text: "x", Source: Source{build.SourceUL, "lawtext/8225"}}}, src, ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].Reason != "source file changed ul/lawtext/8225" {
		t.Fatalf("got %+v", vs)
	}
}

func TestSaderlexSource(t *testing.T) {
	meta, _ := os.ReadFile("testdata/bundle/197135/meta.json")
	text, _ := os.ReadFile("testdata/bundle/197135/text.json")
	numbers, texts, err := build.ScanArticles(meta, text)
	if err != nil || len(texts) < 2 {
		t.Fatalf("fixture scan: %d articles, %v", len(texts), err)
	}
	var arts []ArticleText
	for i := range texts {
		arts = append(arts, ArticleText{LawID: build.ScanIDBase + 197135, Article: numbers[i], Text: texts[i], Source: Source{build.SourceSaderlex, "197135"}})
	}
	if vs := Check(arts, fixtureSources(), ApprovedEdits{}, Known{}); len(vs) != 0 {
		t.Fatalf("the scan's own articles: %v", vs)
	}
	// One word of a scanned article rewritten.
	w := strings.Fields(texts[0])
	w[0] = "ويلغى"
	vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: strings.Join(w, " "), Source: Source{build.SourceSaderlex, "197135"}}},
		fixtureSources(), ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].Reason != "not in source saderlex/197135" {
		t.Fatalf("got %+v", vs)
	}
}

// A SaderLex file edited after the manifest recorded it.
func TestSaderlexFileChanged(t *testing.T) {
	bundle := copyBundle(t)
	p := filepath.Join(bundle, "197135", "text.json")
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(raw), "المادة", "الماده", 1)), 0o644)
	src := &ArchiveSources{Bundle: bundle}
	if _, err := src.Text(build.SourceSaderlex, "197135"); !errors.Is(err, ErrChanged) {
		t.Fatalf("want source file changed, got %v", err)
	}
	vs := Check([]ArticleText{{LawID: 1, Article: "1", Text: "x", Source: Source{build.SourceSaderlex, "197135"}}}, src, ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].Reason != "source file changed saderlex/197135" {
		t.Fatalf("got %+v", vs)
	}
	// No manifest: its own reason, so a block names the missing file, not
	// thousands of texts. A file the manifest does not list: not in the archive.
	os.Remove(ManifestPath(bundle))
	nm := &ArchiveSources{Bundle: bundle}
	if _, err := nm.Text(build.SourceSaderlex, "197135"); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("no manifest: %v", err)
	}
	vs = Check([]ArticleText{{LawID: 1, Article: "1", Text: "x", Source: Source{build.SourceSaderlex, "197135"}}}, nm, ApprovedEdits{}, Known{})
	if len(vs) != 1 || vs[0].Reason != "no bundle manifest for saderlex/197135" {
		t.Fatalf("got %+v", vs)
	}
	if msg := (&BlockedError{Violations: vs}).Error(); !strings.Contains(msg, "1 scanned texts unchecked: the SaderLex bundle has no manifest") ||
		!strings.Contains(msg, "lebleg provenance manifest -scans") {
		t.Errorf("block message: %q", msg)
	}
	os.WriteFile(ManifestPath(bundle), []byte("path\tsha256\n"), 0o644)
	if _, err := (&ArchiveSources{Bundle: bundle}).Text(build.SourceSaderlex, "197135"); !errors.Is(err, ErrNotInArchive) {
		t.Fatalf("not in the manifest: %v", err)
	}
	// A manifest kept elsewhere (-manifest), as a calibration run uses.
	elsewhere := filepath.Join(t.TempDir(), "m.tsv")
	m, _ := os.ReadFile(ManifestPath("testdata/bundle"))
	os.WriteFile(elsewhere, m, 0o644)
	os.WriteFile(p, rawOrig(t), 0o644)
	if _, err := (&ArchiveSources{Bundle: bundle, Manifest: elsewhere}).Text(build.SourceSaderlex, "197135"); err != nil {
		t.Fatalf("manifest elsewhere: %v", err)
	}
}

// The manifest is kept beside the bundle folder, never inside it: the
// nightly replaces the folder with a fresh copy, and a
// manifest that came with the copy (or was lost with the old one) would
// pin whatever bytes arrived. One inside the folder is not read.
func TestManifestBesideTheBundle(t *testing.T) {
	if got := ManifestPath("../lebgazette-bundle/"); got != "../lebgazette-bundle.manifest.tsv" {
		t.Errorf("ManifestPath = %q", got)
	}
	bundle := copyBundle(t)
	m, _ := os.ReadFile(ManifestPath(bundle))
	os.Remove(ManifestPath(bundle))
	os.WriteFile(filepath.Join(bundle, "manifest.tsv"), m, 0o644)
	if _, err := (&ArchiveSources{Bundle: bundle}).Text(build.SourceSaderlex, "197135"); !errors.Is(err, ErrNoManifest) {
		t.Errorf("a manifest inside the folder was read: %v", err)
	}
	// From a checkout's root, as the nightly, snapshot.sh and hf.sh run it.
	if got := ManifestPath(DefaultPaths("repo").Bundle); got != "lebgazette-bundle.manifest.tsv" {
		t.Errorf("default manifest %q", got)
	}
}

func rawOrig(t *testing.T) []byte {
	b, err := os.ReadFile("testdata/bundle/197135/text.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// copyBundle copies the fixture bundle folder, and its manifest beside it.
func copyBundle(t *testing.T) string {
	t.Helper()
	b := copyDir(t, "testdata/bundle")
	m, err := os.ReadFile(ManifestPath("testdata/bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(b), m, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// The nightly records each new bundle's files the first night they arrive:
// the text and the scan they were read from. A recorded hash is never
// rewritten: a recorded file whose bytes changed is reported and keeps its
// old hash (so the guard says "source file changed"), until a person names
// its bundle in accept. The folder's own top-level files are left out.
func TestRecordManifest(t *testing.T) {
	bundle := copyBundle(t)
	m := ManifestPath(bundle)
	os.Remove(m)
	r, err := RecordManifest(bundle, m, nil)
	if err != nil || r.Added != 2 || r.Kept != 0 || len(r.Changed) != 0 {
		t.Fatalf("first run: %+v, %v", r, err)
	}
	got, _ := os.ReadFile(m)
	want, _ := os.ReadFile(ManifestPath("testdata/bundle"))
	if string(got) != string(want) {
		t.Errorf("manifest:\n%s\nwant:\n%s", got, want)
	}

	// A new bundle arrives with its scan, the folder's index changes, and an
	// old file is edited after it was recorded.
	nb := filepath.Join(bundle, "200000")
	os.MkdirAll(nb, 0o755)
	os.WriteFile(filepath.Join(nb, "meta.json"), []byte(`{"id":200000}`), 0o644)
	os.WriteFile(filepath.Join(nb, "text.json"), []byte(`{"pages":[]}`), 0o644)
	os.WriteFile(filepath.Join(nb, "source.pdf"), []byte("%PDF-1.4 scan"), 0o644)
	os.WriteFile(filepath.Join(nb, "page-1.png"), []byte("png page 1"), 0o644)
	os.WriteFile(filepath.Join(bundle, "issues.tsv"), []byte("200000\n"), 0o644)
	os.WriteFile(filepath.Join(bundle, ".version"), []byte("3\n"), 0o644)
	p := filepath.Join(bundle, "197135", "text.json")
	os.WriteFile(p, []byte(strings.Replace(string(rawOrig(t)), "المادة", "الماده", 1)), 0o644)
	r, err = RecordManifest(bundle, m, nil)
	if err != nil || r.Added != 4 || r.Kept != 1 || strings.Join(r.Changed, ",") != "197135/text.json" {
		t.Fatalf("second run: %+v, %v", r, err)
	}
	man, _ := ReadManifest(m)
	if man["197135/text.json"] != "8c3b9e193458a2e291435bce6aac37ce4b8d841c3064fed5d0376acf55c5fd61" || len(man) != 6 {
		t.Errorf("the edited file's hash was rewritten, or the new bundle is missing: %v", man)
	}
	if man["200000/source.pdf"] != sha("%PDF-1.4 scan") || man["200000/page-1.png"] != sha("png page 1") {
		t.Errorf("the scan is not pinned: %v", man)
	}
	if _, ok := man["issues.tsv"]; ok {
		t.Errorf("the folder's index was recorded: %v", man)
	}
	if _, err := (&ArchiveSources{Bundle: bundle}).Text(build.SourceSaderlex, "197135"); !errors.Is(err, ErrChanged) {
		t.Errorf("edited file: %v, want source file changed", err)
	}

	// A page image replaced after it was recorded is reported too.
	os.WriteFile(filepath.Join(nb, "page-1.png"), []byte("another page"), 0o644)
	r, err = RecordManifest(bundle, m, nil)
	if err != nil || strings.Join(r.Changed, ",") != "197135/text.json,200000/page-1.png" {
		t.Fatalf("third run: %+v, %v", r, err)
	}

	// A person checked the new bytes and accepts that bundle.
	r, err = RecordManifest(bundle, m, []string{"197135"})
	if err != nil || r.Accepted != 1 || strings.Join(r.Changed, ",") != "200000/page-1.png" {
		t.Fatalf("accept: %+v, %v", r, err)
	}
	if _, err := (&ArchiveSources{Bundle: bundle}).Text(build.SourceSaderlex, "197135"); err != nil {
		t.Errorf("accepted bundle: %v", err)
	}
}

// An amendment's source is the amending article alone, from its law's page.
func TestAmendmentSource(t *testing.T) {
	src := fixtureSources()
	raw, _ := archive.Archive{Dir: "testdata/archive"}.Read("lawtext", 9307)
	lt, _ := parse.ParseLawText(raw)
	got, err := src.Text(build.SourceAmendment, "9307/1")
	if err != nil {
		t.Fatal(err)
	}
	if want := lt.Articles[0].TextAr + "\n"; got != want {
		t.Errorf("amendment 9307/1 is %q, want article 1 alone %q", got, want)
	}
	if _, err := src.Text(build.SourceAmendment, "9307/99"); !errors.Is(err, ErrNotInArchive) {
		t.Errorf("an article the law lacks: %v", err)
	}
	if _, err := src.Text(build.SourceAmendment, "nonsense"); !errors.Is(err, ErrNotInArchive) {
		t.Errorf("a malformed id: %v", err)
	}
	// Article 2 of the amending law is not article 1's source.
	if len(lt.Articles) > 1 {
		vs := Check([]ArticleText{{LawID: 8225, Article: "1", Text: lt.Articles[1].TextAr, Source: Source{build.SourceAmendment, "9307/1"}}},
			src, ApprovedEdits{}, Known{})
		if len(vs) != 1 {
			t.Errorf("another article of the amending law passed: %v", vs)
		}
	}
}

func copyDir(t *testing.T, from string) string {
	t.Helper()
	to := filepath.Join(t.TempDir(), filepath.Base(from))
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return to
}
