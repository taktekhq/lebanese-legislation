package staticexport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// Deps is the record of what pages used, by route: for each route pattern the
// names the site noted while rendering its pages (Options.Record), such as
// "set:law" for a page template or "key:nav.home" for a UI string. Each shard
// keeps its own beside its manifest, as deps.json. The planner reads all of
// them to turn a changed template or string into the routes that use it.
type Deps map[string][]string

// ParseDeps reads a deps.json.
func ParseDeps(b []byte, name string) (Deps, error) {
	var d Deps
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if d == nil {
		d = Deps{}
	}
	return d, nil
}

// ReadDeps reads a deps.json file; a missing file is an empty record.
func ReadDeps(file string) (Deps, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return Deps{}, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseDeps(b, file)
}

// Merge adds every name of o to d.
func (d Deps) Merge(o Deps) {
	for g, names := range o {
		d.add(g, names)
	}
}

func (d Deps) add(group string, names []string) {
	if len(names) == 0 {
		if _, ok := d[group]; !ok {
			d[group] = []string{}
		}
		return
	}
	seen := map[string]bool{}
	for _, n := range d[group] {
		seen[n] = true
	}
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			d[group] = append(d[group], n)
		}
	}
	sort.Strings(d[group])
}

// Uses says whether a route's pages used any of names.
func (d Deps) Uses(group string, names map[string]bool) bool {
	for _, n := range d[group] {
		if names[n] {
			return true
		}
	}
	return false
}
