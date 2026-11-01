package recall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Source loads the articles and the amending articles that have a row.
type Source interface {
	Load(ctx context.Context) ([]Article, map[Key]bool, error)
}

// Run is the tool: it returns the exit code (0 pass, 1 the ratio fell or a
// large new miss group, 2 the database or baseline cannot be read). With
// init, a missing baseline is written from tonight's count instead of failing.
func Run(ctx context.Context, src Source, baselinePath string, init bool, out, errw io.Writer) int {
	var base Report
	haveBase := false
	if b, err := os.ReadFile(baselinePath); err == nil {
		if err := json.Unmarshal(b, &base); err != nil || base.Candidates == 0 {
			fmt.Fprintf(errw, "recall-report: baseline %s is not readable\n", baselinePath)
			return 2
		}
		haveBase = true
	} else if !init {
		fmt.Fprintf(errw, "recall-report: cannot read baseline: %v (run with -init once to write it)\n", err)
		return 2
	}
	arts, rows, err := src.Load(ctx)
	if err != nil {
		fmt.Fprintf(errw, "recall-report: cannot read the database: %v\n", err)
		return 2
	}
	now := Count(arts, rows)
	if now.Candidates == 0 {
		fmt.Fprintln(errw, "recall-report: no article has an amending verb; the database looks empty")
		return 2
	}
	fmt.Fprintf(out, "recall %.2f%% (%d of %d articles with an amending verb have a row)\n", now.Ratio*100, now.Produced, now.Candidates)
	for i, g := range now.Groups {
		if i == 5 {
			break
		}
		fmt.Fprintf(out, "  miss group %q: %d\n", g.Head, g.Count)
	}
	if !haveBase {
		if err := writeBaseline(baselinePath, now); err != nil {
			fmt.Fprintf(errw, "recall-report: %v\n", err)
			return 2
		}
		fmt.Fprintln(out, "baseline written")
		return 0
	}
	if why := Compare(now, base); why != "" {
		fmt.Fprintln(errw, "recall-report: FAIL "+why)
		return 1
	}
	if now.Ratio > base.Ratio {
		if err := writeBaseline(baselinePath, now); err != nil {
			fmt.Fprintf(errw, "recall-report: %v\n", err)
			return 2
		}
		fmt.Fprintf(out, "baseline raised from %.2f%%\n", base.Ratio*100)
	}
	return 0
}

func writeBaseline(path string, r Report) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
