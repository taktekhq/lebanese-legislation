package citecheck

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/cite"
	"github.com/taktekhq/lebanese-legislation/internal/judge"
)

// Options says where the check reads from.
type Options struct {
	DSN        string
	NewIDs     string   // file of law ids added in the last 24 hours, one a line
	PrevIDs    string   // file of last night's law ids: new = in the database now, not in it
	Citations  string   // db/model/citations.jsonl
	Judgements string   // db/model/judgements.jsonl
	Logs       []string // the new part of the judge and cite logs
	Now        time.Time
}

// ReadIDs reads law ids, one per line; blank lines and # comments are skipped.
func ReadIDs(path string) (map[int]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ids := map[int]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a law id", path, s)
		}
		ids[n] = true
	}
	return ids, sc.Err()
}

// Load reads the database and the model files into an Input.
func Load(ctx context.Context, o Options) (Input, error) {
	var in Input
	var ids map[int]bool
	var err error
	if o.NewIDs != "" {
		ids, err = ReadIDs(o.NewIDs)
		if err != nil {
			return in, err
		}
	}
	if in.Citations, err = cite.Load(o.Citations); err != nil {
		return in, fmt.Errorf("%s: %w", o.Citations, err)
	}
	js, err := judge.Load(o.Judgements)
	if err != nil {
		return in, fmt.Errorf("%s: %w", o.Judgements, err)
	}
	for _, p := range o.Logs {
		data, err := os.ReadFile(p)
		if err != nil {
			return in, err
		}
		// The logs only grow, so the newest summary stands for the last run.
		if m := failedRE.FindAllString(string(data), -1); len(m) > 0 {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				if failedRE.MatchString(lines[i]) {
					in.LogLines = append(in.LogLines, lines[i])
					break
				}
			}
		}
	}
	pool, err := pgxpool.New(ctx, o.DSN)
	if err != nil {
		return in, err
	}
	defer pool.Close()
	if o.PrevIDs != "" {
		if ids, in.Current, err = newSince(ctx, pool, o.PrevIDs); err != nil {
			return in, err
		}
	}
	items, err := cite.Items(ctx, pool, "all")
	if err != nil {
		return in, err
	}
	ix, err := cite.LawIndex(ctx, pool)
	if err != nil {
		return in, err
	}
	in.Gaps = map[string][]string{}
	for _, it := range items {
		if !ids[it.LawID] {
			continue
		}
		in.New = append(in.New, Article{Key: it.Key, Input: it.Input})
		if c, ok := in.Citations[it.Key]; ok {
			for _, lk := range cite.Resolve(ix, it.LawID, c) {
				if lk.LawID == 0 && cite.Changing(lk.Action) && lk.Action != "general_repeal" {
					in.Gaps[it.Key] = append(in.Gaps[it.Key], "unresolved "+lk.Action+": "+lk.Quote)
				}
			}
		}
	}
	in.Judged = map[string]judge.Judgement{}
	in.Recomputed = map[string]string{}
	since := o.Now.Add(-24 * time.Hour)
	for k, j := range js {
		parts := strings.Split(k, "/")
		if len(parts) != 4 {
			continue
		}
		byLaw, _ := strconv.Atoi(parts[0])
		seq, _ := strconv.Atoi(parts[2])
		at, _ := time.Parse(time.RFC3339, j.At)
		if !ids[byLaw] && at.Before(since) {
			continue
		}
		var amending, centre, part string
		err := pool.QueryRow(ctx, `SELECT ba.text_ar, ta.text_ar, coalesce(x.part, '')
			FROM amendment x
			JOIN legislation_article ba ON ba.law_id = x.by_law_id AND ba.number = x.by_article
			JOIN legislation_article ta ON ta.law_id = x.law_id AND ta.number = x.article_number
			WHERE x.by_law_id = $1 AND x.by_article = $2 AND x.seq = $3 AND x.article_number = $4`,
			byLaw, parts[1], seq, parts[3]).Scan(&amending, &centre, &part)
		in.Judged[k] = j
		if err == nil {
			in.Recomputed[k] = judge.InputHash(amending, centre, parts[3], part)
		}
	}
	return in, nil
}

// newSince reads every law id now and returns those not in the previous
// night's file. A missing file is the first night: nothing is new, and the
// caller records the ids.
func newSince(ctx context.Context, pool *pgxpool.Pool, prevPath string) (map[int]bool, []int, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM legislation ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var cur []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		cur = append(cur, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	prev, err := ReadIDs(prevPath)
	if os.IsNotExist(err) {
		return map[int]bool{}, cur, nil
	}
	if err != nil {
		return nil, nil, err
	}
	fresh := map[int]bool{}
	for _, id := range cur {
		if !prev[id] {
			fresh[id] = true
		}
	}
	return fresh, cur, nil
}

// SaveIDs writes ids, one a line, for tomorrow's -prev.
func SaveIDs(path string, ids []int) error {
	// The state directory is absent on a fresh checkout (nightly 2026-10-29).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintln(&b, id)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
