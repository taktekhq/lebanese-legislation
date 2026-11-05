package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// dataRepo is a git repository with data/stats.json and three tables, and a
// function that commits a change to it.
func dataRepo(t *testing.T) (dir, base string, commit func(files map[string]string) string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := gitOut(dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	run("init", "-q")
	run("config", "user.email", "fixture@example.org")
	run("config", "user.name", "Fixture")
	commit = func(files map[string]string) string {
		t.Helper()
		for name, body := range files {
			p := filepath.Join(dir, name)
			if body == "" {
				os.Remove(p)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-qm", "data")
		return run("rev-parse", "HEAD")
	}
	var laws, dates strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&laws, "%d\tlaw %d\n", i, i)
		fmt.Fprintf(&dates, "law\t%d\thash%d\t2026-11-01\n", i, i)
	}
	base = commit(map[string]string{
		"data/stats.json":                    `{"legislation": 200, "ruling": 2, "page_state": 200}`,
		"data/tables/legislation/000000.tsv": laws.String(),
		"data/tables/ruling/000000.tsv":      "1\tr\n2\tr\n",
		"data/tables/page_state/000000.tsv":  dates.String(),
	})
	return dir, base, commit
}

func testOptions(dir string) options {
	return options{dir: dir, maxDrop: 0.01, minDrop: 5, maxRemoved: 3, maxDates: 0.05}
}

func TestReviewData(t *testing.T) {
	dir, base, commit := dataRepo(t)
	o := testOptions(dir)
	laws := func(drop map[int]bool) string {
		var b strings.Builder
		for i := 1; i <= 200; i++ {
			if !drop[i] {
				fmt.Fprintf(&b, "%d\tlaw %d\n", i, i)
			}
		}
		return b.String()
	}

	// A normal night: two laws gone, one ruling renamed, three dates moved.
	dates, _ := gitOut(dir, "show", base+":data/tables/page_state/000000.tsv")
	moved := strings.NewReplacer("hash1\t2026-11-01", "hash1x\t2026-11-04", "hash2\t2026-11-01", "hash2x\t2026-11-04", "hash3\t2026-11-01", "hash3x\t2026-11-04").Replace(dates)
	ok := commit(map[string]string{
		"data/stats.json":                    `{"legislation": 198, "ruling": 2, "page_state": 200}`,
		"data/tables/legislation/000000.tsv": laws(map[int]bool{7: true, 9: true}),
		"data/tables/ruling/000000.tsv":      "1\tr\n2\trenamed\n",
		"data/tables/page_state/000000.tsv":  moved,
	})
	r, err := reviewData(o, base, ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.reasons) != 0 {
		t.Fatalf("a normal night was refused: %v", r.reasons)
	}
	want := []string{"legislation: 200 to 198 rows", "legislation removed: 7, 9", "page dates moved: 3"}
	if strings.Join(r.summary, "|") != strings.Join(want, "|") {
		t.Fatalf("summary %q, want %q", r.summary, want)
	}

	// A partial store: most laws gone.
	drop := map[int]bool{}
	for i := 20; i <= 200; i++ {
		drop[i] = true
	}
	bad := commit(map[string]string{
		"data/stats.json":                    `{"legislation": 19, "ruling": 2, "page_state": 200}`,
		"data/tables/legislation/000000.tsv": laws(drop),
	})
	r, err = reviewData(o, ok, bad)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.reasons) != 2 || !strings.Contains(r.reasons[0], "table legislation: 198 rows to 19") || !strings.Contains(r.reasons[1], "181 rows of legislation disappear") {
		t.Fatalf("a partial store passed: %v", r.reasons)
	}

	// Every page's date reset, and a table gone.
	reset := strings.ReplaceAll(moved, "2026-11-0", "2026-11-1")
	gone := commit(map[string]string{
		"data/stats.json":                    `{"legislation": 198, "page_state": 200}`,
		"data/tables/legislation/000000.tsv": laws(map[int]bool{7: true, 9: true}),
		"data/tables/page_state/000000.tsv":  reset,
		"data/tables/ruling/000000.tsv":      "",
	})
	r, err = reviewData(o, ok, gone)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.reasons, "|") != "table ruling is gone (had 2 rows)|200 page dates move (limit 10 of 200)" {
		t.Fatalf("a date reset passed: %v", r.reasons)
	}
}

func TestJudgeChecks(t *testing.T) {
	done := func(name, c string) checkRun { return checkRun{Name: name, Status: "completed", Conclusion: c} }
	pending, why := judgeChecks([]checkRun{done("unit", "success"), {Name: "build", Status: "in_progress"}}, []string{"build", "ci-ok"})
	if strings.Join(pending, ",") != "build,ci-ok" || len(why) != 0 {
		t.Fatalf("pending %v why %v", pending, why)
	}
	pending, why = judgeChecks([]checkRun{done("build", "success"), done("ci-ok", "success"), done("api", "skipped")}, []string{"build", "ci-ok"})
	if len(pending) != 0 || len(why) != 0 {
		t.Fatalf("pending %v why %v", pending, why)
	}
	_, why = judgeChecks([]checkRun{done("build", "failure"), done("ci-ok", "success")}, []string{"build", "ci-ok"})
	if strings.Join(why, "|") != "check build: failure|required check build did not succeed" {
		t.Fatalf("why %v", why)
	}
}

// fakeGitHub answers the calls run makes, for one pull request whose checks
// finish on the second look.
type fakeGitHub struct {
	mu       sync.Mutex
	files    []string
	looks    int
	comments []string
	merged   string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr := `{"number":7,"title":"data: 2026-11-05","state":"open","head":{"sha":"abc"}}`
	switch {
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/o/r/pulls") && r.URL.Query().Get("head") == "o:data/nightly":
		fmt.Fprint(w, "["+pr+"]")
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/pulls/7":
		fmt.Fprint(w, pr)
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/pulls/7/files":
		b, _ := json.Marshal(func() []map[string]string {
			var out []map[string]string
			for _, n := range f.files {
				out = append(out, map[string]string{"filename": n})
			}
			return out
		}())
		w.Write(b)
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/commits/abc/check-runs":
		f.looks++
		status := "in_progress"
		if f.looks > 1 {
			status = "completed"
		}
		fmt.Fprintf(w, `{"check_runs":[{"name":"build","status":%q,"conclusion":"success"},{"name":"ci-ok","status":"completed","conclusion":"success"}]}`, status)
	case r.Method == "POST" && r.URL.Path == "/repos/o/r/issues/7/comments":
		var c struct{ Body string }
		json.NewDecoder(r.Body).Decode(&c)
		f.comments = append(f.comments, c.Body)
		w.WriteHeader(201)
		fmt.Fprint(w, "{}")
	case r.Method == "PUT" && r.URL.Path == "/repos/o/r/pulls/7/merge":
		var m struct{ SHA string }
		json.NewDecoder(r.Body).Decode(&m)
		f.merged = m.SHA
		fmt.Fprint(w, `{"merged":true}`)
	default:
		http.Error(w, r.Method+" "+r.URL.String(), 404)
	}
}

// remoteClone is a clone whose origin has main and data/nightly, with the
// branch at the returned head.
func remoteClone(t *testing.T) (dir, head string) {
	t.Helper()
	src, _, commit := dataRepo(t)
	if _, err := gitOut(src, "branch", "-M", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOut(src, "checkout", "-q", "-b", "data/nightly"); err != nil {
		t.Fatal(err)
	}
	head = commit(map[string]string{"data/stats.json": `{"legislation": 200, "ruling": 2, "page_state": 200, "noindex_page": 5}`})
	dir = t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", src, dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	return dir, head
}

func TestRunMergesAPassingPR(t *testing.T) {
	dir, head := remoteClone(t)
	f := &fakeGitHub{files: []string{"data/stats.json", "db/model/citations.jsonl"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	gh := &github{api: srv.URL, repo: "o/r", token: "t", http: srv.Client()}
	o := testOptions(dir)
	o.branch, o.base, o.require, o.wait, o.poll = "data/nightly", "main", []string{"build", "ci-ok"}, time.Minute, time.Millisecond
	reviewed := ""
	err := run(context.Background(), gh, o, func(base, h string) (review, error) {
		reviewed = h
		return review{summary: []string{"noindex_page: 0 to 5 rows"}}, nil
	})
	if err == nil {
		t.Fatal("the fake head abc is not the clone's branch head; run must refuse to merge")
	}
	if f.merged != "" {
		t.Fatal("merged an unverified head")
	}

	// The same, with the pull request's head equal to the branch.
	f2 := &fakeGitHub{files: []string{"data/stats.json"}}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.ReplaceAll(r.URL.Path, head, "abc")
		rec := httptest.NewRecorder()
		f2.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write([]byte(strings.ReplaceAll(rec.Body.String(), `"sha":"abc"`, `"sha":"`+head+`"`)))
	}))
	defer srv2.Close()
	gh.api = srv2.URL
	if err := run(context.Background(), gh, o, func(base, h string) (review, error) {
		reviewed = h
		return review{summary: []string{"noindex_page: 0 to 5 rows"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if reviewed != head || f2.merged != head {
		t.Fatalf("reviewed %q merged %q, want %q", reviewed, f2.merged, head)
	}
	if f2.looks != 2 || len(f2.comments) != 1 || !strings.Contains(f2.comments[0], "noindex_page: 0 to 5 rows") {
		t.Fatalf("looks %d comments %q", f2.looks, f2.comments)
	}
}

func TestRunRefusesCodeAndFailedReviews(t *testing.T) {
	f := &fakeGitHub{files: []string{"data/stats.json", "cmd/build-db/main.go"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	gh := &github{api: srv.URL, repo: "o/r", token: "t", http: srv.Client()}
	o := testOptions(t.TempDir())
	o.branch, o.base, o.wait, o.poll = "data/nightly", "main", time.Minute, time.Millisecond
	err := run(context.Background(), gh, o, func(string, string) (review, error) {
		t.Fatal("reviewed data of a PR that changes code")
		return review{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "changes cmd/build-db/main.go") {
		t.Fatalf("err %v", err)
	}
	if f.merged != "" || len(f.comments) != 1 || !strings.Contains(f.comments[0], "Not merged") {
		t.Fatalf("merged %q comments %q", f.merged, f.comments)
	}
}
