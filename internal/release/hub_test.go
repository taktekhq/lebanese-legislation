package release

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/hfhub"
)

// fakeHub is an in-memory dataset served over the Hub's HTTP API: commits,
// LFS (basic uploads), listings, paths-info and resolve with ranges.
type fakeHub struct {
	mu      sync.Mutex
	files   map[string][]byte
	lfs     map[string][]byte // sha256 -> content
	head    int
	commits int
	srv     *httptest.Server
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{files: map[string][]byte{}, lfs: map[string][]byte{}, head: 1}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) sha() string { return strings.Repeat(strconv.Itoa(h.head%10), 40) }

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := r.URL.Path
	authed := r.Header.Get("Authorization") == "Bearer secret"
	switch {
	case strings.HasPrefix(p, "/api/datasets/o/d/revision/"):
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": h.sha()})
	case strings.HasPrefix(p, "/api/datasets/o/d/tree/"):
		var out []map[string]any
		for name, b := range h.files {
			out = append(out, map[string]any{"type": "file", "path": name, "size": len(b)})
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(p, "/api/datasets/o/d/paths-info/"):
		_ = r.ParseForm()
		var out []map[string]any
		for _, name := range r.Form["paths"] {
			if b, ok := h.files[name]; ok {
				sum := sha256.Sum256(b)
				out = append(out, map[string]any{"type": "file", "path": name, "size": len(b),
					"lfs": map[string]any{"oid": hex.EncodeToString(sum[:]), "size": len(b)}})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case p == "/datasets/o/d.git/info/lfs/objects/batch":
		if !authed {
			http.Error(w, "auth", 401)
			return
		}
		var req struct {
			Objects []struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"objects"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var objs []map[string]any
		for _, o := range req.Objects {
			objs = append(objs, map[string]any{"oid": o.OID, "size": o.Size, "actions": map[string]any{
				"upload": map[string]any{"href": h.srv.URL + "/upload/" + o.OID}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": objs})
	case strings.HasPrefix(p, "/upload/"):
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "storage must not get the token", 400)
			return
		}
		b, _ := io.ReadAll(r.Body)
		h.lfs[strings.TrimPrefix(p, "/upload/")] = b
	case p == "/api/datasets/o/d/commit/main":
		if !authed {
			http.Error(w, "auth", 401)
			return
		}
		dec := json.NewDecoder(r.Body)
		staged := map[string][]byte{}
		for {
			var line struct {
				Key   string         `json:"key"`
				Value map[string]any `json:"value"`
			}
			if err := dec.Decode(&line); err != nil {
				break
			}
			switch line.Key {
			case "header":
				if line.Value["parentCommit"] != h.sha() {
					http.Error(w, "parent moved", 412)
					return
				}
			case "file":
				b, _ := base64.StdEncoding.DecodeString(line.Value["content"].(string))
				staged[line.Value["path"].(string)] = b
			case "lfsFile":
				b, ok := h.lfs[line.Value["oid"].(string)]
				if !ok {
					http.Error(w, "lfs object missing", 400)
					return
				}
				staged[line.Value["path"].(string)] = b
			}
		}
		for k, v := range staged {
			h.files[k] = v
		}
		h.head++
		h.commits++
		_ = json.NewEncoder(w).Encode(map[string]string{"commitOid": h.sha()})
	case strings.HasPrefix(p, "/datasets/o/d/resolve/"):
		parts := strings.SplitN(strings.TrimPrefix(p, "/datasets/o/d/resolve/"), "/", 2)
		b, ok := h.files[parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "", timeZero, strings.NewReader(string(b)))
	default:
		http.NotFound(w, r)
	}
}

func TestPublishOneCommitThenVerifyAndRefuseOverwrite(t *testing.T) {
	ctx := context.Background()
	h := newFakeHub(t)
	h.files["README.md"] = []byte("---\nlicense: other\n---\n# Data\n\nText.\n")
	h.files[".gitattributes"] = []byte("*.parquet filter=lfs diff=lfs merge=lfs -text\n")
	dump := []byte(strings.Repeat("x", 70000) + "end")
	dir := writeRelease(t, "r1", dump)
	hub := hfhub.New(h.srv.URL, "secret")
	oid, r, err := Publish(ctx, hub, dir, PublishOptions{Repo: "o/d", ExpectLatest: "none", Card: true})
	if err != nil {
		t.Fatal(err)
	}
	if h.commits != 1 || string(h.files["releases/r1/corpus.dump"]) != string(dump) {
		t.Fatalf("commits %d", h.commits)
	}
	if !strings.Contains(string(h.files[".gitattributes"]), "*.dump filter=lfs") || !strings.Contains(string(h.files[".gitattributes"]), "*.parquet") {
		t.Fatalf("gitattributes %s", h.files[".gitattributes"])
	}
	if !strings.Contains(string(h.files["README.md"]), CardStart) {
		t.Fatal("card block missing")
	}
	if err := VerifyPublished(ctx, hfhub.New(h.srv.URL, ""), "o/d", oid, r, filepath.Join(dir, "corpus.dump")); err != nil {
		t.Fatal(err)
	}
	// The same id again is refused, even as a retry.
	if _, _, err := Publish(ctx, hub, dir, PublishOptions{Repo: "o/d"}); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v", err)
	}
	// A publish expecting an older latest is refused.
	dir2 := writeRelease(t, "r2", []byte("second"))
	if _, _, err := Publish(ctx, hub, dir2, PublishOptions{Repo: "o/d", ExpectLatest: "r0"}); err == nil || !strings.Contains(err.Error(), "latest release is r1") {
		t.Fatalf("got %v", err)
	}
	if _, _, err := Publish(ctx, hub, dir2, PublishOptions{Repo: "o/d", ExpectLatest: "r1"}); err != nil {
		t.Fatal(err)
	}
	// A local dump that differs from the published one fails verification.
	other := filepath.Join(t.TempDir(), "corpus.dump")
	_ = os.WriteFile(other, []byte(strings.Repeat("y", len(dump))), 0o644)
	if err := VerifyPublished(ctx, hfhub.New(h.srv.URL, ""), "o/d", oid, r, other); err == nil {
		t.Fatal("a different local dump passed")
	}
}

func TestFetchFollowsPointerAndDownloadVerifies(t *testing.T) {
	ctx := context.Background()
	h := newFakeHub(t)
	dump := []byte("the dump")
	dir := writeRelease(t, "r1", dump)
	if _, _, err := Publish(ctx, hfhub.New(h.srv.URL, "secret"), dir, PublishOptions{Repo: "o/d"}); err != nil {
		t.Fatal(err)
	}
	anon := hfhub.New(h.srv.URL, "")
	r, path, err := Fetch(ctx, anon, "o/d", "main", LatestPath)
	if err != nil {
		t.Fatal(err)
	}
	if path != "releases/r1/manifest.json" || r.ArtifactRevision() != h.sha() {
		t.Fatalf("%s %s", path, r.ArtifactRevision())
	}
	cache := t.TempDir()
	file, err := Download(ctx, anon, "o/d", r, path, cache)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != string(dump) {
		t.Fatal("wrong content")
	}
	// The cached file is verified, not trusted.
	_ = os.WriteFile(file, []byte("changed!"), 0o644)
	if _, err := Download(ctx, anon, "o/d", r, path, cache); !errors.Is(err, ErrChecksum) {
		t.Fatalf("got %v", err)
	}
	// A pointer whose manifest checksum differs is refused.
	h.mu.Lock()
	h.files[LatestPath] = []byte(strings.Replace(string(h.files[LatestPath]), `"manifest_sha256": "`, `"manifest_sha256": "0`, 1)[:len(h.files[LatestPath])])
	h.mu.Unlock()
	if _, _, err := Fetch(ctx, anon, "o/d", "main", LatestPath); err == nil {
		t.Fatal("a bad pointer was accepted")
	}
	// A dump bigger than declared leaves no partial file.
	h.mu.Lock()
	h.files["releases/r1/corpus.dump"] = []byte("the dump plus more")
	h.mu.Unlock()
	cache2 := t.TempDir()
	if _, err := Download(ctx, anon, "o/d", r, path, cache2); err == nil {
		t.Fatal("an oversized dump was accepted")
	}
	entries, _ := os.ReadDir(filepath.Join(cache2, r.ArtifactRevision()))
	if len(entries) != 0 {
		t.Fatalf("partial files left: %v", entries)
	}
}
