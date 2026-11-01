package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testManifest is a valid manifest for a dump with the given content.
func testManifest(id string, dump []byte) map[string]any {
	sum := sha256.Sum256(dump)
	return map[string]any{
		"format_version": 1, "release_id": id, "generated_at": "2026-11-01T00:00:00.000000Z", "as_of_date": "2026-11-01",
		"source":      map[string]any{"repo_id": "taktekhq/lebanese-legislation", "revision": strings.Repeat("a", 40)},
		"postgres":    map[string]any{"version": "18.6", "vector_version": "0.8.2"},
		"text_search": map[string]any{"extension": "pg_trgm", "version": "1.6", "indexes": SearchIndexes},
		"embedding": map[string]any{"model": Model, "dimensions": 768, "task_type": TaskType, "chunk_size": 900,
			"chunk_overlap": 120, "storage": Storage},
		"counts":        map[string]any{"tables": map[string]any{"legislation": 3}, "source_documents": 5, "semantic_chunks": 7},
		"dataset":       map[string]any{"license": "l", "attribution": "a"},
		"build_version": "0.4.0",
		"dump":          map[string]any{"path": "corpus.dump", "size_bytes": len(dump), "sha256": hex.EncodeToString(sum[:])},
	}
}

func encode(t *testing.T, m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestManifestContract(t *testing.T) {
	good := testManifest("r1", []byte("dump"))
	if _, err := Parse(encode(t, good)); err != nil {
		t.Fatal(err)
	}
	mutate := map[string]func(m map[string]any){
		"format":       func(m map[string]any) { m["format_version"] = 2 },
		"float format": func(m map[string]any) { m["format_version"] = 1.5 },
		"release id":   func(m map[string]any) { m["release_id"] = "../x" },
		"revision":     func(m map[string]any) { m["source"].(map[string]any)["revision"] = "main" },
		"postgres":     func(m map[string]any) { m["postgres"].(map[string]any)["extra"] = "x" },
		"model":        func(m map[string]any) { m["embedding"].(map[string]any)["model"] = "other" },
		"storage":      func(m map[string]any) { m["embedding"].(map[string]any)["storage"] = "vector(768)" },
		"provider":     func(m map[string]any) { m["embedding"].(map[string]any)["provider"] = "other" },
		"text search":  func(m map[string]any) { m["text_search"].(map[string]any)["version"] = "1.5" },
		"dump path":    func(m map[string]any) { m["dump"].(map[string]any)["path"] = "../corpus.dump" },
		"dump size":    func(m map[string]any) { m["dump"].(map[string]any)["size_bytes"] = 0 },
		"sha":          func(m map[string]any) { m["dump"].(map[string]any)["sha256"] = "ABC" },
		"table name":   func(m map[string]any) { m["counts"].(map[string]any)["tables"] = map[string]any{"Bad-Name": 1} },
		"negative":     func(m map[string]any) { m["counts"].(map[string]any)["semantic_chunks"] = -1 },
		"licence":      func(m map[string]any) { m["dataset"].(map[string]any)["license"] = "" },
		"no dump":      func(m map[string]any) { delete(m, "dump") },
	}
	for name, f := range mutate {
		m := testManifest("r1", []byte("dump"))
		f(m)
		if _, err := Parse(encode(t, m)); !errors.Is(err, ErrManifest) && err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// text_search is optional, provider gemini and storage halfvec(768) are allowed.
	m := testManifest("r1", []byte("dump"))
	delete(m, "text_search")
	m["embedding"].(map[string]any)["provider"] = "gemini"
	if _, err := Parse(encode(t, m)); err != nil {
		t.Fatal(err)
	}
}

func TestSafePath(t *testing.T) {
	for _, p := range []string{"", "/abs", "a/../b", "a//b", "./a", `a\b`, "c:x", "a/"} {
		if SafePath(p) == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if SafePath("releases/r1/corpus.dump") != nil {
		t.Error("a normal path refused")
	}
}

func TestChecksumIgnoresArtifactRevisionAndKeyOrder(t *testing.T) {
	a, _ := Parse(encode(t, testManifest("r1", []byte("dump"))))
	b, _ := Parse(encode(t, testManifest("r1", []byte("dump"))))
	b.Raw["artifact_revision"] = strings.Repeat("b", 40)
	if a.Checksum() != b.Checksum() {
		t.Fatal("artifact_revision changed the checksum")
	}
	// Sorted, compact, non-ASCII kept: the same identity Python wrote.
	m := map[string]any{"b": 1, "a": "نص"}
	if got := string(canonicalJSON(normalize(m))); got != `{"a":"نص","b":1}` {
		t.Fatalf("canonical JSON %s", got)
	}
}

func writeRelease(t *testing.T, id string, dump []byte) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "corpus.dump"), dump, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Encode(testManifest(id, dump))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanVerifiesDumpAndPinsPointer(t *testing.T) {
	dir := writeRelease(t, "r1", []byte("dump bytes"))
	r, plan, err := Plan(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if string(plan["releases/r1/manifest.json"].Content) != string(raw) {
		t.Fatal("manifest not published byte for byte")
	}
	var p Pointer
	if err := json.Unmarshal(plan[LatestPath].Content, &p); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if p.ReleaseID != r.ReleaseID || p.ManifestPath != "releases/r1/manifest.json" || p.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("pointer %+v", p)
	}
	if plan["releases/r1/corpus.dump"].Path == "" {
		t.Fatal("dump not planned from disk")
	}
	if err := os.WriteFile(filepath.Join(dir, "corpus.dump"), []byte("tampered!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Plan(dir); !errors.Is(err, ErrChecksum) {
		t.Fatalf("tampered dump: %v", err)
	}
}

func TestCardBlock(t *testing.T) {
	card := "---\nlicense: other\n---\n# Lebanese Legislation\n\nCredit the Centre.\n"
	once, err := UpdateCardBlock(card, "first")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nlicense: other\n---\n# Lebanese Legislation\n\n" + CardStart + "\nfirst\n" + CardEnd + "\n\nCredit the Centre.\n"
	if once != want {
		t.Fatalf("got %q", once)
	}
	twice, _ := UpdateCardBlock(once, "second")
	if strings.Count(twice, CardStart) != 1 || !strings.Contains(twice, "\nsecond\n") || !strings.HasPrefix(twice, "---\nlicense: other\n---\n") {
		t.Fatalf("got %q", twice)
	}
	if again, _ := UpdateCardBlock(twice, "second"); again != twice {
		t.Fatal("not idempotent")
	}
	if _, err := UpdateCardBlock(CardEnd+"\n"+CardStart, "x"); err == nil {
		t.Fatal("a broken block was overwritten")
	}
	if _, err := UpdateCardBlock("no heading", "x"); err == nil {
		t.Fatal("a card without a heading was accepted")
	}
	r, _ := Parse(encode(t, testManifest("r1", []byte("d"))))
	r.Counts.SourceDocuments, r.Counts.SemanticChunks = 1094080, 1547568
	body := CardBody("o/d", r, "")
	if !strings.Contains(body, "1,094,080 source records and 1,547,568 indexed passages") ||
		!strings.Contains(body, "https://huggingface.co/datasets/o/d/resolve/main/releases/r1/corpus.dump?download=true") {
		t.Fatalf("body %s", body)
	}
}
