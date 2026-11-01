// Package archive reads the git-tracked raw pages: archive/<type>.tsv lists
// them, archive/<type>/<id÷1000>/<id>.html holds each page as served. A type
// whose index outgrows GitHub's 100 MB file limit is listed instead in parts,
// archive/<type>-<id÷100000>.tsv, each with its own header.
package archive

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Entry is one row of a type's index.
type Entry struct {
	Type        string
	ID          int
	URL         string
	FirstSeen   time.Time
	LastChanged time.Time
	SHA256      string // of the raw bytes
}

// Archive is a checkout's archive/ directory.
type Archive struct{ Dir string }

// Rel is a page's path inside the repository, as recorded for provenance.
func Rel(typ string, id int) string {
	return fmt.Sprintf("archive/%s/%04d/%d.html", typ, id/1000, id)
}

// Read returns a page's raw bytes.
func (a Archive) Read(typ string, id int) ([]byte, error) {
	return os.ReadFile(filepath.Join(a.Dir, typ, fmt.Sprintf("%04d", id/1000), strconv.Itoa(id)+".html"))
}

// Index lists every page of a type, in id order.
func (a Archive) Index(typ string) ([]Entry, error) {
	paths, err := filepath.Glob(filepath.Join(a.Dir, typ+"-[0-9][0-9].tsv"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		paths = []string{filepath.Join(a.Dir, typ+".tsv")}
	}
	var out []Entry
	for _, p := range paths { // Glob sorts, and parts split on id ranges
		if out, err = readIndex(p, typ, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readIndex(path, typ string, out []Entry) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	col := map[string]int{}
	for line := 0; sc.Scan(); line++ {
		fs := strings.Split(sc.Text(), "\t")
		if line == 0 {
			for i, h := range fs {
				col[h] = i
			}
			continue
		}
		get := func(k string) string {
			if i, ok := col[k]; ok && i < len(fs) {
				return fs[i]
			}
			return ""
		}
		id, err := strconv.Atoi(get("doc_id"))
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", filepath.Base(path), line+1, err)
		}
		e := Entry{Type: typ, ID: id, URL: get("url"), SHA256: get("blob_sha")}
		e.FirstSeen, _ = time.Parse(time.RFC3339, get("first_seen"))
		e.LastChanged, _ = time.Parse(time.RFC3339, get("last_changed"))
		if e.LastChanged.IsZero() {
			e.LastChanged = e.FirstSeen
		}
		out = append(out, e)
	}
	return out, sc.Err()
}
