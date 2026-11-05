// Command merge-data-pr reviews the open nightly data pull request and merges
// it when it passes. It runs after scripts/nightly.sh has pushed the data
// branch (on our XPS, a systemd unit started when the nightly succeeds).
//
//	GH_TOKEN=... merge-data-pr -dir ~/lebleg-nightly/public
//	merge-data-pr -dry-run    # review and print; no comment, no merge
//	merge-data-pr -review BASE..HEAD    # review two commits of the clone; no GitHub
//
// The review passes when:
//   - the pull request changes only data/ and db/model/;
//   - every check on its head commit finished without failing, and the
//     required ones (-require) succeeded;
//   - no table in data/stats.json loses more than -max-drop of its rows
//     (at least -min-drop rows are always allowed);
//   - at most -max-removed laws and -max-removed rulings disappear;
//   - at most -max-dates of the pages' last-changed dates move.
//
// These catch the two ways a nightly has gone wrong before: a source outage
// or a partial store that drops laws (3 Nov: 93,487 of 103,197), and a
// rebuild that resets every page's date (3 Nov: 164,305 of 165,855). A
// pull request that fails gets a comment with the reasons, stays open, and
// the command exits 1 after running -notify with the reasons on its input.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type options struct {
	repo, branch, base, dir string
	require                 []string
	wait, poll              time.Duration
	maxDrop                 float64
	minDrop, maxRemoved     int
	maxDates                float64
	dryRun                  bool
	notify                  string
}

func main() {
	log.SetFlags(0)
	o := options{}
	repo := flag.String("repo", env("LEBLEG_REPO", "taktekhq/lebanese-legislation"), "GitHub repository")
	flag.StringVar(&o.branch, "branch", env("LEBLEG_BRANCH", "data/nightly"), "the data pull request's branch")
	flag.StringVar(&o.base, "base", env("LEBLEG_BASE", "main"), "the branch it merges into")
	flag.StringVar(&o.dir, "dir", ".", "a clone of the repository, with the remote origin")
	require := flag.String("require", "build,ci-ok", "checks that must have succeeded, comma separated")
	flag.DurationVar(&o.wait, "wait", 3*time.Hour, "how long to wait for the checks")
	flag.DurationVar(&o.poll, "poll", time.Minute, "how often to look at the checks")
	flag.Float64Var(&o.maxDrop, "max-drop", 0.01, "largest share of a table's rows that may disappear")
	flag.IntVar(&o.minDrop, "min-drop", 50, "rows a table may always lose")
	flag.IntVar(&o.maxRemoved, "max-removed", 25, "laws, and rulings, that may disappear")
	flag.Float64Var(&o.maxDates, "max-dates", 0.05, "largest share of page dates that may move")
	flag.BoolVar(&o.dryRun, "dry-run", false, "review and print the result; no comment, no merge")
	flag.StringVar(&o.notify, "notify", "", "shell command run when the review fails, with the reasons on its input")
	api := flag.String("api", "https://api.github.com", "GitHub API")
	offline := flag.String("review", "", "review BASE..HEAD of the clone and exit, without GitHub")
	flag.Parse()
	o.repo = *repo
	for _, r := range strings.Split(*require, ",") {
		if r = strings.TrimSpace(r); r != "" {
			o.require = append(o.require, r)
		}
	}
	if *offline != "" {
		base, head, ok := strings.Cut(*offline, "..")
		if !ok {
			log.Fatal("merge-data-pr: -review takes BASE..HEAD")
		}
		r, err := reviewData(o, base, head)
		if err != nil {
			log.Fatalf("merge-data-pr: %v", err)
		}
		for _, l := range r.summary {
			fmt.Println("  " + l)
		}
		if len(r.reasons) > 0 {
			log.Fatalf("would not merge:\n- %s", strings.Join(r.reasons, "\n- "))
		}
		fmt.Println("would merge")
		return
	}
	token := env("GH_TOKEN", os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		log.Fatal("merge-data-pr: set GH_TOKEN (or GITHUB_TOKEN) to a token that can merge pull requests")
	}
	gh := &github{api: strings.TrimRight(*api, "/"), repo: o.repo, token: token, http: &http.Client{Timeout: time.Minute}}
	ctx := context.Background()
	err := run(ctx, gh, o, func(base, head string) (review, error) { return reviewData(o, base, head) })
	var failed reviewFailed
	if errors.As(err, &failed) {
		if o.notify != "" && !o.dryRun {
			cmd := exec.Command("sh", "-c", o.notify)
			cmd.Stdin = strings.NewReader(failed.Error())
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if nerr := cmd.Run(); nerr != nil {
				log.Printf("merge-data-pr: -notify: %v", nerr)
			}
		}
	}
	if err != nil {
		log.Fatalf("merge-data-pr: %v", err)
	}
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// reviewFailed is a review that found reasons not to merge.
type reviewFailed struct {
	pr      int
	reasons []string
}

func (r reviewFailed) Error() string {
	return fmt.Sprintf("data PR #%d not merged:\n- %s", r.pr, strings.Join(r.reasons, "\n- "))
}

// review is what reviewData found: why not to merge, and the changes to report.
type review struct {
	reasons, summary []string
}

// run finds the open data pull request, waits for its checks, reviews it and
// merges it, or comments why not.
func run(ctx context.Context, gh *github, o options, reviewer func(base, head string) (review, error)) error {
	pr, err := gh.openPR(ctx, o.branch, o.base)
	if err != nil {
		return err
	}
	if pr == nil {
		fmt.Printf("no open pull request from %s into %s\n", o.branch, o.base)
		return nil
	}
	fmt.Printf("PR #%d %q at %.12s\n", pr.Number, pr.Title, pr.Head.SHA)
	var reasons []string
	files, err := gh.files(ctx, pr.Number)
	if err != nil {
		return err
	}
	for _, f := range files {
		if !strings.HasPrefix(f, "data/") && !strings.HasPrefix(f, "db/model/") {
			reasons = append(reasons, "changes "+f+", outside data/ and db/model/")
		}
	}
	head := pr.Head.SHA
	if len(reasons) == 0 {
		var why []string
		if head, why, err = waitChecks(ctx, gh, o, pr); err != nil {
			return err
		}
		reasons = append(reasons, why...)
	}
	var summary []string
	if len(reasons) == 0 {
		if err := git(o.dir, "fetch", "-q", "origin", "+refs/heads/"+o.base+":refs/remotes/origin/"+o.base, "+refs/heads/"+o.branch+":refs/remotes/origin/"+o.branch); err != nil {
			return err
		}
		got, err := gitOut(o.dir, "rev-parse", "refs/remotes/origin/"+o.branch)
		if err != nil {
			return err
		}
		if strings.TrimSpace(got) != head {
			return fmt.Errorf("%s is at %.12s, the pull request at %.12s: run again", o.branch, got, head)
		}
		base, err := gitOut(o.dir, "merge-base", "refs/remotes/origin/"+o.base, head)
		if err != nil {
			return err
		}
		r, err := reviewer(strings.TrimSpace(base), head)
		if err != nil {
			return err
		}
		reasons, summary = r.reasons, r.summary
	}
	for _, s := range summary {
		fmt.Println("  " + s)
	}
	if len(reasons) > 0 {
		failed := reviewFailed{pr: pr.Number, reasons: reasons}
		fmt.Println(failed.Error())
		if !o.dryRun {
			body := "**Not merged by the nightly review.**\n\n- " + strings.Join(reasons, "\n- ") +
				"\n\nThe review (`cmd/merge-data-pr`) merges only data changes that pass its limits. Fix the cause and run the nightly again, or merge by hand after checking the diff."
			if err := gh.comment(ctx, pr.Number, body); err != nil {
				log.Printf("merge-data-pr: comment: %v", err)
			}
		}
		return failed
	}
	if o.dryRun {
		fmt.Printf("dry run: PR #%d passes; not merged\n", pr.Number)
		return nil
	}
	body := "**Reviewed and merged by the nightly review** (`cmd/merge-data-pr`): only data changed, every check passed, and no limit was reached.\n\n"
	if len(summary) > 0 {
		body += "- " + strings.Join(summary, "\n- ") + "\n"
	}
	if err := gh.comment(ctx, pr.Number, body); err != nil {
		return err
	}
	if err := gh.merge(ctx, pr.Number, head, pr.Title); err != nil {
		return err
	}
	fmt.Printf("merged PR #%d\n", pr.Number)
	return nil
}

// waitChecks waits until every check on the pull request's head has finished,
// following the head if the nightly pushes again meanwhile. It returns the
// head it judged and the reasons not to merge.
func waitChecks(ctx context.Context, gh *github, o options, pr *pull) (string, []string, error) {
	deadline := time.Now().Add(o.wait)
	head := pr.Head.SHA
	for {
		cur, err := gh.pr(ctx, pr.Number)
		if err != nil {
			return "", nil, err
		}
		if cur.State != "open" {
			return "", nil, fmt.Errorf("PR #%d is %s now", pr.Number, cur.State)
		}
		head = cur.Head.SHA
		runs, err := gh.checks(ctx, head)
		if err != nil {
			return "", nil, err
		}
		pending, why := judgeChecks(runs, o.require)
		if len(why) > 0 || len(pending) == 0 {
			return head, why, nil
		}
		if time.Now().After(deadline) {
			return head, []string{fmt.Sprintf("checks still running after %s: %s", o.wait, strings.Join(pending, ", "))}, nil
		}
		fmt.Printf("waiting for %s\n", strings.Join(pending, ", "))
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(o.poll):
		}
	}
}

// judgeChecks says which checks are still pending (unfinished, or required
// and not there yet) and why the finished ones forbid a merge.
func judgeChecks(runs []checkRun, require []string) (pending, why []string) {
	byName := map[string][]checkRun{}
	for _, r := range runs {
		byName[r.Name] = append(byName[r.Name], r)
	}
	for _, r := range runs {
		if r.Status != "completed" {
			pending = append(pending, r.Name)
			continue
		}
		switch r.Conclusion {
		case "success", "skipped", "neutral":
		default:
			why = append(why, fmt.Sprintf("check %s: %s", r.Name, r.Conclusion))
		}
	}
	for _, name := range require {
		rs := byName[name]
		if len(rs) == 0 {
			pending = append(pending, name)
			continue
		}
		ok := false
		for _, r := range rs {
			ok = ok || r.Status == "completed" && r.Conclusion == "success"
		}
		if !ok && allDone(rs) {
			why = append(why, fmt.Sprintf("required check %s did not succeed", name))
		}
	}
	sort.Strings(pending)
	return uniq(pending), why
}

func allDone(rs []checkRun) bool {
	for _, r := range rs {
		if r.Status != "completed" {
			return false
		}
	}
	return true
}

func uniq(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// reviewData compares the data at base and head in the clone at o.dir.
func reviewData(o options, base, head string) (review, error) {
	var r review
	before, err := stats(o.dir, base)
	if err != nil {
		return r, err
	}
	after, err := stats(o.dir, head)
	if err != nil {
		return r, err
	}
	tables := make([]string, 0, len(before))
	for t := range before {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		b := before[t]
		h, ok := after[t]
		if !ok {
			r.reasons = append(r.reasons, fmt.Sprintf("table %s is gone (had %d rows)", t, b))
			continue
		}
		limit := max(o.minDrop, int(math.Ceil(o.maxDrop*float64(b))))
		if b-h > limit {
			r.reasons = append(r.reasons, fmt.Sprintf("table %s: %d rows to %d (%d fewer, limit %d)", t, b, h, b-h, limit))
		}
		if h != b {
			r.summary = append(r.summary, fmt.Sprintf("%s: %d to %d rows", t, b, h))
		}
	}
	for _, t := range []string{"legislation", "ruling"} {
		gone, err := removedIDs(o.dir, base, head, t)
		if err != nil {
			return r, err
		}
		if len(gone) > o.maxRemoved {
			r.reasons = append(r.reasons, fmt.Sprintf("%d rows of %s disappear (limit %d), first %s", len(gone), t, o.maxRemoved, strings.Join(gone[:min(5, len(gone))], ", ")))
		} else if len(gone) > 0 {
			r.summary = append(r.summary, fmt.Sprintf("%s removed: %s", t, strings.Join(gone, ", ")))
		}
	}
	moved, err := changedLines(o.dir, base, head, "page_state")
	if err != nil {
		return r, err
	}
	if limit := int(o.maxDates * float64(before["page_state"])); moved > limit {
		r.reasons = append(r.reasons, fmt.Sprintf("%d page dates move (limit %d of %d)", moved, limit, before["page_state"]))
	} else if moved > 0 {
		r.summary = append(r.summary, fmt.Sprintf("page dates moved: %d", moved))
	}
	return r, nil
}

// stats reads data/stats.json, the row count of every table, at rev.
func stats(dir, rev string) (map[string]int, error) {
	b, err := gitOut(dir, "show", rev+":data/stats.json")
	if err != nil {
		return nil, err
	}
	m := map[string]int{}
	if err := json.Unmarshal([]byte(b), &m); err != nil {
		return nil, fmt.Errorf("data/stats.json at %.12s: %w", rev, err)
	}
	return m, nil
}

// tableLines reads the rows of the table's files that differ between base and
// head, on each side. Unchanged files hold the same rows on both.
func tableLines(dir, base, head, table string) (before, after []string, err error) {
	names, err := gitOut(dir, "diff", "--name-only", base, head, "--", "data/tables/"+table+"/")
	if err != nil {
		return nil, nil, err
	}
	for _, f := range strings.Fields(names) {
		if !strings.HasSuffix(f, ".tsv") {
			continue
		}
		for _, side := range []struct {
			rev string
			out *[]string
		}{{base, &before}, {head, &after}} {
			b, err := gitOut(dir, "show", side.rev+":"+f)
			if err != nil {
				if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
					continue
				}
				return nil, nil, err
			}
			sc := bufio.NewScanner(strings.NewReader(b))
			sc.Buffer(make([]byte, 1<<20), 64<<20)
			for sc.Scan() {
				if line := sc.Text(); line != "" {
					*side.out = append(*side.out, line)
				}
			}
			if err := sc.Err(); err != nil {
				return nil, nil, err
			}
		}
	}
	return before, after, nil
}

// removedIDs are the ids (first column) the table has at base and not at head.
func removedIDs(dir, base, head, table string) ([]string, error) {
	before, after, err := tableLines(dir, base, head, table)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range after {
		id, _, _ := strings.Cut(l, "\t")
		have[id] = true
	}
	var gone []string
	seen := map[string]bool{}
	for _, l := range before {
		id, _, _ := strings.Cut(l, "\t")
		if !have[id] && !seen[id] {
			seen[id] = true
			gone = append(gone, id)
		}
	}
	sort.Strings(gone)
	return gone, nil
}

// changedLines counts the table's rows at base that are not, as they are, at head.
func changedLines(dir, base, head, table string) (int, error) {
	before, after, err := tableLines(dir, base, head, table)
	if err != nil {
		return 0, err
	}
	have := map[string]int{}
	for _, l := range after {
		have[l]++
	}
	n := 0
	for _, l := range before {
		if have[l] > 0 {
			have[l]--
			continue
		}
		n++
	}
	return n, nil
}

func git(dir string, args ...string) error {
	_, err := gitOut(dir, args...)
	return err
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// github is the little of GitHub's REST API the command needs.
type github struct {
	api, repo, token string
	http             *http.Client
}

type pull struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func (g *github) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.api+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lebanese-legislation-merge-data-pr (+https://github.com/taktekhq/lebanese-legislation)")
	res, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("GitHub %s %s: HTTP %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(data[:min(len(data), 500)])))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (g *github) openPR(ctx context.Context, branch, base string) (*pull, error) {
	owner, _, _ := strings.Cut(g.repo, "/")
	var prs []pull
	if err := g.do(ctx, "GET", fmt.Sprintf("/repos/%s/pulls?state=open&base=%s&head=%s:%s", g.repo, base, owner, branch), nil, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

func (g *github) pr(ctx context.Context, n int) (*pull, error) {
	var p pull
	return &p, g.do(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", g.repo, n), nil, &p)
}

func (g *github) files(ctx context.Context, n int) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		var fs []struct {
			Filename string `json:"filename"`
		}
		if err := g.do(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=100&page=%d", g.repo, n, page), nil, &fs); err != nil {
			return nil, err
		}
		for _, f := range fs {
			names = append(names, f.Filename)
		}
		if len(fs) < 100 {
			return names, nil
		}
	}
}

func (g *github) checks(ctx context.Context, sha string) ([]checkRun, error) {
	var all []checkRun
	for page := 1; ; page++ {
		var r struct {
			CheckRuns []checkRun `json:"check_runs"`
		}
		if err := g.do(ctx, "GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100&page=%d", g.repo, sha, page), nil, &r); err != nil {
			return nil, err
		}
		all = append(all, r.CheckRuns...)
		if len(r.CheckRuns) < 100 {
			return all, nil
		}
	}
}

func (g *github) comment(ctx context.Context, n int, body string) error {
	return g.do(ctx, "POST", fmt.Sprintf("/repos/%s/issues/%d/comments", g.repo, n), map[string]string{"body": body}, nil)
}

// merge merges the pull request exactly at sha, so a push after the review
// is never merged unreviewed.
func (g *github) merge(ctx context.Context, n int, sha, title string) error {
	return g.do(ctx, "PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", g.repo, n), map[string]string{
		"sha": sha, "merge_method": "merge", "commit_title": fmt.Sprintf("Merge pull request #%d: %s", n, title),
	}, nil)
}
