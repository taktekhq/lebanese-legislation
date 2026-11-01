package build

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var diffTables = []string{"legislation_article", "amendment", "article_version"}

// DiffOptions configures a comparison of two independently built revisions.
type DiffOptions struct {
	OldRef, NewRef, Archive, Scans, RulingFiles string
	Expected, Report                            string
	DSN                                         string
	Repo                                        string
}

type diffKey struct {
	table string
	key   string
}

type diffRows struct {
	added, removed, changed map[diffKey][2]string
}

// CompareRows compares sorted TSV exports by their key columns.
func CompareRows(oldData, newData map[string][]byte) (diffRows, error) {
	out := diffRows{added: map[diffKey][2]string{}, removed: map[diffKey][2]string{}, changed: map[diffKey][2]string{}}
	for _, table := range diffTables {
		oldRows, err := keyedRows(table, oldData[table])
		if err != nil {
			return diffRows{}, fmt.Errorf("old %s: %w", table, err)
		}
		newRows, err := keyedRows(table, newData[table])
		if err != nil {
			return diffRows{}, fmt.Errorf("new %s: %w", table, err)
		}
		keys := map[string]bool{}
		for k := range oldRows {
			keys[k] = true
		}
		for k := range newRows {
			keys[k] = true
		}
		ordered := make([]string, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			a, aok := oldRows[k]
			b, bok := newRows[k]
			dk := diffKey{table, k}
			switch {
			case !aok:
				out.added[dk] = [2]string{"", b}
			case !bok:
				out.removed[dk] = [2]string{a, ""}
			case a != b:
				out.changed[dk] = [2]string{a, b}
			}
		}
	}
	return out, nil
}

func keyedRows(table string, data []byte) (map[string]string, error) {
	rows := map[string]string{}
	if len(data) == 0 {
		return rows, nil
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	keyCols := 1
	switch table {
	case "legislation_article":
		keyCols = 2
	case "article_version":
		keyCols = 4
	}
	for line := 1; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if len(rec) < keyCols {
			return nil, fmt.Errorf("line %d: expected at least %d key columns", line, keyCols)
		}
		key := strings.Join(rec[:keyCols], "\x1f")
		if _, ok := rows[key]; ok {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		rows[key] = strings.Join(rec, "\t")
	}
	return rows, nil
}

// RunDiff builds old and new revisions in disposable databases, then verifies the result.
func RunDiff(ctx context.Context, o DiffOptions) error {
	if o.Expected == "" {
		return errors.New("diff-build: expected diff is required (use a TSV file or 'none')")
	}
	if o.OldRef == "" || o.NewRef == "" {
		return errors.New("diff-build: both -old and -new refs are required")
	}
	if o.DSN == "" {
		return errors.New("diff-build: -admin-dsn is required; live database is never used")
	}
	if o.Repo == "" {
		o.Repo, _ = os.Getwd()
	}
	if o.Archive == "" {
		o.Archive = filepath.Join(o.Repo, "archive")
	}
	if o.Report == "" {
		o.Report = "diff-build-report.txt"
	}
	if o.Scans == "" {
		o.Scans = filepath.Join(o.Repo, "../lebgazette-bundle")
	}
	if o.RulingFiles == "" {
		o.RulingFiles = filepath.Join(o.Repo, "../lebleg-ruling-files")
	}
	if _, err := os.Stat(o.Archive); err != nil {
		return fmt.Errorf("diff-build: archive: %w", err)
	}
	adminCfg, err := pgx.ParseConfig(o.DSN)
	if err != nil {
		return fmt.Errorf("diff-build: parse admin database DSN: %w", err)
	}
	current := adminCfg.Database
	if current == "" {
		return errors.New("diff-build: admin DSN must name its maintenance database")
	}
	if strings.HasPrefix(current, "lebleg_diff_") {
		return errors.New("diff-build: maintenance database cannot be a scratch database")
	}
	adminCfg.Database = current
	admin, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("diff-build: connect admin database: %w", err)
	}
	defer admin.Close(context.Background())
	// The scratch names are unique per process. Only databases created by this
	// invocation are removed, so an unrelated database is never dropped.
	prefix := "lebleg_diff_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	oldDB, newDB := prefix+"_old", prefix+"_new"
	created := make([]string, 0, 2)
	defer func() {
		for _, name := range created {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		}
	}()
	for _, name := range []string{oldDB, newDB} {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return err
		}
		created = append(created, name)
	}
	dsnFor := func(name string) string {
		cfg := adminCfg.Copy()
		cfg.Database = name
		return cfg.ConnString()
	}
	buildRef := func(ref, name string) error {
		tmp, err := os.MkdirTemp("", "lebleg-diff-"+name+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		cmd := exec.CommandContext(ctx, "git", "worktree", "add", "--detach", tmp, ref)
		cmd.Dir = o.Repo
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("worktree %s: %s: %w", ref, strings.TrimSpace(string(out)), err)
		}
		defer exec.Command("git", "-C", o.Repo, "worktree", "remove", "--force", tmp).Run()
		bin := filepath.Join(tmp, "lebleg-diff-build")
		cmd = exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/lebleg")
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %s: %w", ref, strings.TrimSpace(string(out)), err)
		}
		args := []string{"build", "-db", dsnFor(name), "-archive", o.Archive, "-scans", o.Scans, "-ruling-files", o.RulingFiles}
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build-db %s: %s: %w", ref, strings.TrimSpace(string(out)), err)
		}
		return nil
	}
	if err := buildRef(o.OldRef, oldDB); err != nil {
		return err
	}
	if err := buildRef(o.NewRef, newDB); err != nil {
		return err
	}
	oldRows, err := exportDiffTables(ctx, dsnFor(oldDB))
	if err != nil {
		return err
	}
	newRows, err := exportDiffTables(ctx, dsnFor(newDB))
	if err != nil {
		return err
	}
	actual, err := CompareRows(oldRows, newRows)
	if err != nil {
		return err
	}
	expected, err := readExpected(o.Expected)
	if err != nil {
		return err
	}
	return writeDiffReport(o.Report, actual, expected)
}

func exportDiffTables(ctx context.Context, dsn string) (map[string][]byte, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.Background())
	out := map[string][]byte{}
	queries := map[string]string{
		"legislation_article": `SELECT law_id, position, centre_id, section_id, number, number_int, note, text_ar, text_fr, images, in_force_from FROM legislation_article ORDER BY law_id, position`,
		"amendment":           `SELECT id, by_law_id, by_article, seq, action, law_id, article_number, part, text_ar, old_text_ar, effective_on, status, reason, method, resolved_by, judged_by, judgement, evidence FROM amendment ORDER BY id`,
		"article_version":     `SELECT law_id, article_number, major, minor, valid_from, valid_to, state, text_ar, text_source, amendment_id, note FROM article_version ORDER BY law_id, article_number, major, minor`,
	}
	for _, table := range diffTables {
		rows, err := conn.Query(ctx, queries[table])
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		w := bufio.NewWriter(&b)
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				rows.Close()
				return nil, err
			}
			for i, v := range vals {
				if i > 0 {
					w.WriteByte('\t')
				}
				if v == nil {
					w.WriteString("\\N")
					continue
				}
				text := fmt.Sprint(v)
				text = strings.ReplaceAll(text, "\\", "\\\\")
				text = strings.ReplaceAll(text, "\t", "\\t")
				text = strings.ReplaceAll(text, "\n", "\\n")
				text = strings.ReplaceAll(text, "\r", "\\r")
				w.WriteString(text)
			}
			w.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		w.Flush()
		out[table] = b.Bytes()
	}
	return out, nil
}

type expectedRow struct{ table, key, reason string }

func readExpected(path string) ([]expectedRow, error) {
	if path == "none" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []expectedRow
	s := bufio.NewScanner(bytes.NewReader(b))
	for line := 1; s.Scan(); line++ {
		f := strings.SplitN(s.Text(), "\t", 3)
		if len(f) != 3 || f[0] == "" || f[1] == "" || f[2] == "" {
			return nil, fmt.Errorf("expected diff line %d must be table, key, reason", line)
		}
		valid := false
		for _, table := range diffTables {
			if f[0] == table {
				valid = true
			}
		}
		if !valid {
			return nil, fmt.Errorf("expected diff line %d: unknown table %q", line, f[0])
		}
		out = append(out, expectedRow{f[0], f[1], f[2]})
	}
	return out, s.Err()
}

func writeDiffReport(path string, actual diffRows, expected []expectedRow) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Expected rows: %d\n", len(expected))
	for _, table := range diffTables {
		fmt.Fprintf(&b, "\n%s: added %d, removed %d, changed %d\n", table, countTable(actual.added, table), countTable(actual.removed, table), countTable(actual.changed, table))
	}
	all := map[diffKey][2]string{}
	for k, v := range actual.added {
		all[k] = v
	}
	for k, v := range actual.removed {
		all[k] = v
	}
	for k, v := range actual.changed {
		all[k] = v
	}
	keys := make([]diffKey, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].table != keys[j].table {
			return keys[i].table < keys[j].table
		}
		return keys[i].key < keys[j].key
	})
	for i, k := range keys {
		if i >= 50 {
			break
		}
		fmt.Fprintf(&b, "%s\t%s\n  old: %s\n  new: %s\n", k.table, displayKey(k.key), all[k][0], all[k][1])
	}
	want := map[diffKey]bool{}
	for _, e := range expected {
		want[diffKey{e.table, normalizeExpectedKey(e.table, e.key)}] = true
	}
	var failures []string
	for k := range all {
		if !want[k] {
			failures = append(failures, fmt.Sprintf("unexpected %s key %q", k.table, displayKey(k.key)))
		}
	}
	for k := range want {
		if _, a := actual.added[k]; !a {
			if _, r := actual.removed[k]; !r {
				if _, c := actual.changed[k]; !c {
					failures = append(failures, fmt.Sprintf("expected %s key %q did not change", k.table, displayKey(k.key)))
				}
			}
		}
	}
	if len(failures) == 0 {
		b.WriteString("\nPASS: actual diff matches expectation\n")
	} else {
		sort.Strings(failures)
		b.WriteString("\nFAIL:\n")
		for _, f := range failures {
			b.WriteString(f + "\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("diff-build: %s", strings.Join(failures, "; "))
	}
	return nil
}
func displayKey(key string) string { return strings.ReplaceAll(key, "\x1f", ",") }

func normalizeExpectedKey(table, key string) string {
	if table == "legislation_article" || table == "article_version" {
		parts := strings.Split(key, ",")
		if len(parts) > 1 {
			return strings.Join(parts, "\x1f")
		}
	}
	return key
}

func countTable(rows map[diffKey][2]string, table string) int {
	n := 0
	for k := range rows {
		if k.table == table {
			n++
		}
	}
	return n
}
