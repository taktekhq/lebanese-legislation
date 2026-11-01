package staticexport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// ReadManifest reads a manifest into a map by path. A missing file is an
// empty manifest: the first build has no predecessor.
func ReadManifest(file string) (map[string]Entry, error) {
	f, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseManifest(f, file)
}

// ParseManifest reads manifest lines from r; name is for error messages.
func ParseManifest(r io.Reader, name string) (map[string]Entry, error) {
	m := map[string]Entry{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, n, err)
		}
		m[e.Path] = e
	}
	return m, sc.Err()
}

// WriteManifest writes m sorted by path, so a manifest diffs cleanly between
// two builds.
func WriteManifest(file string, m map[string]Entry) error {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	tmp := file + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	enc := json.NewEncoder(w)
	for _, p := range paths {
		if err := enc.Encode(m[p]); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}
