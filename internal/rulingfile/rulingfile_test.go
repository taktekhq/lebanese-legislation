package rulingfile

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
)

// The Centre's answer for a ruling with no file (RuliID=156200, 20 Oct 2026), trimmed.
const noFile = `<!DOCTYPE html><html><head><title></title></head><body>
<form method="post" action="./RulingFile.aspx?RuliID=156200&amp;type=list" id="form1">
<div class="aspNetHidden"><input type="hidden" name="__VIEWSTATE" id="__VIEWSTATE" value="x" /></div>
<div></div></form></body></html>`

func TestClassify(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{"%PDF-1.6\r%\xe2\xe3", OK},
		{noFile, None},
		{`<form id="form1"><a href="x.pdf">1</a></form>`, Other},
		{"<html>Server Error</html>", Other},
	} {
		if got := Classify([]byte(c.body)); got != c.want {
			t.Errorf("Classify(%.20q) = %s, want %s", c.body, got, c.want)
		}
	}
}

func TestLinked(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	write("ruling.tsv", "doc_id\turl\n5388\tu\n156200\tu\n")
	write("ruling/0005/5388.html", `<a href="RulingFile.aspx?RuliID=5388&type=list" id="MainContent_tdlinka">`)
	write("ruling/0156/156200.html", `<a id="MainContent_downloadPDF" target="_blank">نص الحكم كاملا</a>`)
	ids, err := Linked(archive.Archive{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 5388 {
		t.Fatalf("Linked = %v, want [5388]", ids)
	}
}

func TestRunResumesAndRetriesErrors(t *testing.T) {
	var hits atomic.Int32
	failOnce := atomic.Bool{}
	failOnce.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Query().Get("RuliID") {
		case "1":
			w.Write([]byte("%PDF-1.4 one"))
		case "2":
			w.Write([]byte(noFile))
		case "3":
			if failOnce.CompareAndSwap(true, false) {
				http.Error(w, "busy", 503)
				return
			}
			w.Write([]byte("%PDF-1.4 three"))
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	f := func() *Fetcher {
		return &Fetcher{Base: srv.URL, Dir: dir, Workers: 2, Gap: time.Millisecond, MaxGap: 4 * time.Millisecond,
			Timeout: 5 * time.Second, UserAgent: "test", Log: t.Logf}
	}
	if err := f().Run(context.Background(), []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(Path(dir, 1)); string(b) != "%PDF-1.4 one" {
		t.Fatalf("pdf 1 = %q", b)
	}
	if _, err := os.Stat(Path(dir, 3)); err == nil {
		t.Fatal("pdf 3 saved despite a 503")
	}
	// Second run: only the failed id is fetched again.
	hits.Store(0)
	if err := f().Run(context.Background(), []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("second run made %d requests, want 1", hits.Load())
	}
	if b, _ := os.ReadFile(Path(dir, 3)); string(b) != "%PDF-1.4 three" {
		t.Fatalf("pdf 3 = %q", b)
	}
	done, _ := Done(dir)
	if !done[1] || !done[2] || !done[3] {
		t.Fatalf("Done = %v", done)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "index.tsv"))
	if strings.Count(string(idx), "\tnone\t") != 1 {
		t.Fatalf("index:\n%s", idx)
	}
}

func TestSplitPages(t *testing.T) {
	s := "preamble\n=== صفحة 1 ===\nسطر أول\n[هامش] قرار\n\n=== صفحة ٢ ===\nسطر ثان\n==== صفحة 3 ====\n"
	got := SplitPages(s)
	if len(got) != 3 || got[1] != "سطر أول\n[هامش] قرار" || got[2] != "سطر ثان" || got[3] != "" {
		t.Fatalf("SplitPages = %v", got)
	}
}
