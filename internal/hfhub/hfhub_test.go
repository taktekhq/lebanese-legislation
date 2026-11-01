package hfhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMultipartUploadAndVerify serves an LFS batch answer with three part
// URLs, a completion URL and a verify action, as the Hub does for big files.
func TestMultipartUploadAndVerify(t *testing.T) {
	content := []byte(strings.Repeat("abcdefghij", 25)) // 250 bytes, chunk 100: 3 parts
	sum := sha256.Sum256(content)
	oid := hex.EncodeToString(sum[:])
	var mu sync.Mutex
	parts := map[string][]byte{}
	var completed, verified, committed map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/datasets/o/d.git/info/lfs/objects/batch":
			if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Accept") != "application/vnd.git-lfs+json" {
				http.Error(w, "headers", 400)
				return
			}
			header := map[string]string{"chunk_size": "100"}
			for i := 1; i <= 3; i++ {
				header[fmt.Sprintf("%05d", i)] = fmt.Sprintf("%s/part/%d", srv.URL, i)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "multipart", "objects": []any{map[string]any{
				"oid": oid, "size": len(content), "actions": map[string]any{
					"upload": map[string]any{"href": srv.URL + "/complete", "header": header},
					"verify": map[string]any{"href": srv.URL + "/verify"}}}}})
		case strings.HasPrefix(r.URL.Path, "/part/"):
			if r.Header.Get("Authorization") != "" || r.ContentLength <= 0 || len(r.TransferEncoding) > 0 {
				http.Error(w, "storage wants a length and no token", 400)
				return
			}
			b, _ := io.ReadAll(r.Body)
			parts[r.URL.Path] = b
			w.Header().Set("ETag", `"e`+strings.TrimPrefix(r.URL.Path, "/part/")+`"`)
		case r.URL.Path == "/complete":
			_ = json.NewDecoder(r.Body).Decode(&completed)
		case r.URL.Path == "/verify":
			user, pass, ok := r.BasicAuth()
			if !ok || user != "USER" || pass != "tok" {
				http.Error(w, "auth", 401)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&verified)
		case r.URL.Path == "/api/datasets/o/d/commit/main":
			dec := json.NewDecoder(r.Body)
			committed = map[string]any{}
			for {
				var line map[string]any
				if dec.Decode(&line) != nil {
					break
				}
				committed[line["key"].(string)] = line["value"]
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"commitOid": strings.Repeat("c", 40)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "big.dump")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(srv.URL, "tok")
	oidOut, err := c.CreateCommit(context.Background(), Dataset, "o/d", Commit{Parent: strings.Repeat("p", 40), Message: "m",
		Files: []File{{PathInRepo: "big.dump", Path: path, LFS: true}, {PathInRepo: "small.json", Content: []byte("{}")}}})
	if err != nil {
		t.Fatal(err)
	}
	if oidOut != strings.Repeat("c", 40) {
		t.Fatal(oidOut)
	}
	if string(parts["/part/1"])+string(parts["/part/2"])+string(parts["/part/3"]) != string(content) {
		t.Fatal("parts do not add up to the file")
	}
	if completed["oid"] != oid || len(completed["parts"].([]any)) != 3 {
		t.Fatalf("completion %v", completed)
	}
	if verified["oid"] != oid {
		t.Fatalf("verify %v", verified)
	}
	lfs := committed["lfsFile"].(map[string]any)
	if lfs["oid"] != oid || lfs["path"] != "big.dump" || committed["file"].(map[string]any)["path"] != "small.json" {
		t.Fatalf("commit %v", committed)
	}
	if committed["header"].(map[string]any)["parentCommit"] != strings.Repeat("p", 40) {
		t.Fatal("no parent commit")
	}
}

func TestGetRetriesButCommitDoesNot(t *testing.T) {
	var mu sync.Mutex
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			gets++
			if gets < 3 {
				http.Error(w, "busy", 503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": strings.Repeat("a", 40)})
			return
		}
		posts++
		http.Error(w, "busy", 503)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	c.Backoff = time.Millisecond
	sha, err := c.Revision(context.Background(), Dataset, "o/d", "main")
	if err != nil || sha != strings.Repeat("a", 40) || gets != 3 {
		t.Fatalf("%s %v after %d", sha, err, gets)
	}
	if _, err := c.CreateCommit(context.Background(), Dataset, "o/d", Commit{Message: "m", Files: []File{{PathInRepo: "a", Content: []byte("x")}}}); err == nil {
		t.Fatal("commit passed")
	}
	if posts != 1 {
		t.Fatalf("commit sent %d times", posts)
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 403)
	}))
	defer srv.Close()
	c := New(srv.URL, "very-secret-token")
	c.Retries = 0
	_, err := c.Revision(context.Background(), Dataset, "o/d", "main")
	if err == nil || strings.Contains(err.Error(), "very-secret-token") {
		t.Fatalf("got %v", err)
	}
	if _, err := New(srv.URL, "").CreateCommit(context.Background(), Dataset, "o/d", Commit{}); err == nil {
		t.Fatal("anonymous commit attempted")
	}
	if ValidRepoID("../x") || !ValidRepoID("taktekhq/lebanese-legislation") {
		t.Fatal("repo id check")
	}
}

func TestListFollowsPages(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link", `<`+srv.URL+r.URL.Path+`?recursive=true&cursor=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "a"}, {"type": "directory", "path": "d"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "d/b", "lfs": map[string]any{"oid": "x", "size": 1}}})
	}))
	defer srv.Close()
	files, err := New(srv.URL, "").List(context.Background(), Dataset, "o/d", "main", "")
	if err != nil || len(files) != 2 || files[1].Path != "d/b" || files[1].LFS == nil {
		t.Fatalf("%v %v", files, err)
	}
}
