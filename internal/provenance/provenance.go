// Package provenance is the wording guard: no model-written law reaches the
// site, the D1 snapshot or the public dataset.
//
// Every law text in the database (legislation_article.text_ar and text_fr,
// article_version.text_ar) carries the source it was read from, written with
// it: source_kind and source_id, never a hash. An amendment's wordings
// (amendment.text_ar and old_text_ar) are read from the amending article,
// which the row itself names (by_law_id, by_article). Before export,
// snapshot and the Hugging Face upload, Check reads each source from the raw
// archive the fetchers stored (verified against the fetchers' own SHA-256,
// or for the Gazette scans the bundle manifest's), and a text passes only
// when, normalized, it is a contiguous span of its source that starts and
// ends on word boundaries, or its SHA-256 matches a person's approved edit or a known row for
// that law and article. Anything else blocks the run (exit 4): a writer that
// stores model text under a real source id is caught, since the guard
// compares the words, not anything the writer chose. A database with no law
// text is not a pass (ErrNothingToCheck).
//
// Not checked yet: legislation.title, preamble and closing (the text's
// heading and its opening and closing formulas). The static check
// (ScanWriters) holds their writers to the build's writer files.
package provenance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/build"
)

// Source names where a text was read: a kind (ul, saderlex, amendment,
// edit; see internal/build/sources.go) and that kind's id.
type Source struct {
	Kind string
	ID   string
}

func (s Source) String() string { return s.Kind + "/" + s.ID }

// ArticleText is one stored law text. VersionID is 0 for the article's row
// in legislation_article, else major*1000+minor of its article_version row.
// Row (not in the plan's contract) names any other row: "amendment <id>
// text_ar" for an amendment's wording, whose law and article are the
// amending article's.
type ArticleText struct {
	LawID     int64
	Article   string
	VersionID int64
	Text      string
	Source    Source
	Row       string
}

// Violation is a text the guard blocks, and why.
type Violation struct {
	LawID   int64
	Article string
	Reason  string
	// Not in the plan's contract, for the report: which row and its hash.
	VersionID int64
	SHA256    string
	Source    Source
	Row       string
}

// ErrBlocked is what export, snapshot and check-wording return
// when a text has no source: errors.Is(err, ErrBlocked), exit code 4.
var ErrBlocked error = &BlockedError{}

// BlockedError lists the violations that blocked a run.
type BlockedError struct{ Violations []Violation }

// ExitCode is 4: a block, not a crash.
func (e *BlockedError) ExitCode() int { return 4 }

func (e *BlockedError) Is(target error) bool {
	_, ok := target.(*BlockedError)
	return ok
}

// Error is "wording guard: <n> articles without source", then up to 20
// lines "law <id> article <n>: <reason>".
func (e *BlockedError) Error() string {
	type art struct {
		law int64
		a   string
	}
	seen := map[art]bool{}
	var lines []string
	unchecked := 0 // scanned texts the guard could not read: no bundle manifest
	for _, v := range e.Violations {
		k := art{v.LawID, v.Article}
		if seen[k] {
			continue
		}
		seen[k] = true
		if strings.HasPrefix(v.Reason, "no bundle manifest") {
			unchecked++
			continue
		}
		if len(lines) < 20 {
			where := ""
			if v.Row != "" {
				where = " (" + v.Row + ")"
			}
			lines = append(lines, fmt.Sprintf("law %d article %s%s: %s", v.LawID, v.Article, where, v.Reason))
		}
	}
	head := fmt.Sprintf("wording guard: %d articles without source", len(seen))
	if unchecked > 0 {
		lines = append([]string{fmt.Sprintf("%d scanned texts unchecked: the SaderLex bundle has no manifest "+
			"(<bundle>.manifest.tsv, beside it; the nightly records it after each copy: "+
			"record-scan-manifest -scans <bundle>)", unchecked)}, lines...)
	}
	if more := len(seen) - unchecked - min(20, len(seen)-unchecked); more > 0 {
		lines = append(lines, fmt.Sprintf("... and %d more (check-wording --report)", more))
	}
	return strings.Join(append([]string{head}, lines...), "\n")
}

type artKey struct {
	law     int64
	article string
}

// ApprovedEdits are wordings a person wrote or approved, by law, article
// and SHA-256 of the text: approved_edits.tsv, and the person-written
// correction files.
type ApprovedEdits struct{ byText map[artKey]map[string]string } // hash -> edit id

// Has says whether the text's hash is an approved edit for that article.
func (e ApprovedEdits) Has(law int64, article, sha string) bool {
	_, ok := e.byText[artKey{law, article}][sha]
	return ok
}

// Len is the number of approved wordings.
func (e ApprovedEdits) Len() int {
	n := 0
	for _, m := range e.byText {
		n += len(m)
	}
	return n
}

func (e *ApprovedEdits) add(law int64, article, sha, id string) {
	if e.byText == nil {
		e.byText = map[artKey]map[string]string{}
	}
	k := artKey{law, article}
	if e.byText[k] == nil {
		e.byText[k] = map[string]string{}
	}
	e.byText[k][sha] = id
}

// LoadApprovedEdits reads approved edits from these files, each by its
// name:
//   - approved_edits.tsv (any other name too): edit_id, law_id, article,
//     text_sha256, approver, date. A row with no approver is refused.
//   - article_corrections.tsv: law_id, number, text_ar, source: a person's
//     wording read from the printed edition (the build's editorial minor
//     versions); its hash is taken from the text as the build stores it
//     (build.CorrectionText), and its edit id is article_corrections/<law>/<number>.
//   - centre_errors.tsv: law_id, number, note_en, note_ar. Read and checked,
//     but it approves no wording: these rows keep the Centre's text as it is
//     and only note its errors.
func LoadApprovedEdits(paths ...string) (ApprovedEdits, error) {
	var e ApprovedEdits
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return e, err
		}
		name := filepath.Base(p)
		err = eachRow(raw, func(line int, f []string) error {
			switch name {
			case "article_corrections.tsv":
				if f[0] == "law_id" {
					return nil
				}
				law, err := strconv.ParseInt(f[0], 10, 64)
				if len(f) != 4 || err != nil || strings.TrimSpace(f[2]) == "" {
					return fmt.Errorf("%s line %d unreadable", name, line)
				}
				e.add(law, f[1], sha(build.CorrectionText(f[2])), build.CorrectionSource(int(law), f[1]))
			case "centre_errors.tsv":
				if _, err := strconv.ParseInt(f[0], 10, 64); len(f) != 4 || err != nil {
					return fmt.Errorf("%s line %d unreadable", name, line)
				}
			default:
				if f[0] == "edit_id" {
					return nil
				}
				if len(f) != 6 {
					return fmt.Errorf("%s line %d: want 6 columns (edit_id, law_id, article, text_sha256, approver, date)", name, line)
				}
				law, err := strconv.ParseInt(f[1], 10, 64)
				if err != nil || f[0] == "" || f[2] == "" || !isSHA(f[3]) {
					return fmt.Errorf("%s line %d unreadable", name, line)
				}
				if strings.TrimSpace(f[4]) == "" {
					return fmt.Errorf("%s line %d: edit %s has no approver; refused", name, line, f[0])
				}
				e.add(law, f[2], strings.ToLower(f[3]), f[0])
			}
			return nil
		})
		if err != nil {
			return e, err
		}
	}
	return e, nil
}

// Known are texts the calibration found and a person pinned while a task
// fixes them (db/provenance_known.tsv: law_id, article, text_sha256, task).
// A row matches that exact text only, so a later rewrite still blocks.
type Known struct{ byText map[artKey]map[string]string } // hash -> task

// Has says whether this exact text is a known row for that article.
func (k Known) Has(law int64, article, sha string) bool {
	_, ok := k.byText[artKey{law, article}][sha]
	return ok
}

// LoadKnown reads db/provenance_known.tsv. A row with no task is refused.
func LoadKnown(path string) (Known, error) {
	k := Known{byText: map[artKey]map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return k, err
	}
	name := filepath.Base(path)
	err = eachRow(raw, func(line int, f []string) error {
		if f[0] == "law_id" {
			return nil
		}
		if len(f) != 4 {
			return fmt.Errorf("%s line %d: want 4 columns (law_id, article, text_sha256, task)", name, line)
		}
		law, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || f[1] == "" || !isSHA(f[2]) {
			return fmt.Errorf("%s line %d unreadable", name, line)
		}
		if strings.TrimSpace(f[3]) == "" {
			return fmt.Errorf("%s line %d: no task; refused", name, line)
		}
		key := artKey{law, f[1]}
		if k.byText[key] == nil {
			k.byText[key] = map[string]string{}
		}
		k.byText[key][strings.ToLower(f[2])] = f[3]
		return nil
	})
	return k, err
}

func eachRow(raw []byte, fn func(line int, f []string) error) error {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		t := sc.Text()
		if strings.TrimSpace(t) == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if err := fn(line, strings.Split(t, "\t")); err != nil {
			return err
		}
	}
	return sc.Err()
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func isSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Check returns the texts that are not a span of their source and match no
// approved edit or known row. Each source is read and normalized once.
func Check(texts []ArticleText, src SourceStore, edits ApprovedEdits, known Known) []Violation {
	sorted := append([]ArticleText(nil), texts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Source.Kind+"\x00"+sorted[i].Source.ID < sorted[j].Source.Kind+"\x00"+sorted[j].Source.ID
	})
	c := checker{src: src, edits: edits, known: known}
	var out []Violation
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && sorted[j].Source == sorted[i].Source {
			j++
		}
		out = append(out, c.group(sorted[i].Source, sorted[i:j])...)
		i = j
	}
	sortViolations(out)
	return out
}

type checker struct {
	src   SourceStore
	edits ApprovedEdits
	known Known
}

// group checks texts that share one source.
func (c checker) group(s Source, texts []ArticleText) []Violation {
	var out []Violation
	var normSrc normSource
	var srcErr error
	read := false
	for _, t := range texts {
		h := sha(t.Text)
		if c.edits.Has(t.LawID, t.Article, h) || c.known.Has(t.LawID, t.Article, h) {
			continue
		}
		v := Violation{LawID: t.LawID, Article: t.Article, VersionID: t.VersionID, SHA256: h, Source: s, Row: t.Row}
		switch s.Kind {
		case build.SourceEdit:
			v.Reason = "not in source " + s.String()
			out = append(out, v)
			continue
		case build.SourceUL, build.SourceSaderlex, build.SourceAmendment:
		default:
			v.Reason = "unknown source kind"
			if s.Kind != "" {
				v.Reason += " " + strconv.Quote(s.Kind)
			}
			out = append(out, v)
			continue
		}
		if !read {
			read = true
			var raw string
			raw, srcErr = c.src.Text(s.Kind, s.ID)
			normSrc = normalizeSource(raw)
		}
		switch {
		case errors.Is(srcErr, ErrNoManifest):
			v.Reason = "no bundle manifest for " + s.String()
		case errors.Is(srcErr, ErrChanged):
			v.Reason = "source file changed " + s.String()
		case errors.Is(srcErr, ErrUnknownKind):
			v.Reason = "unknown source kind"
		case srcErr != nil:
			v.Reason = "source " + s.String() + " not in archive"
		case !normSrc.spans(NormalizeStored(t.Text)):
			v.Reason = "not in source " + s.String()
		default:
			continue
		}
		out = append(out, v)
	}
	return out
}

func sortViolations(vs []Violation) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.LawID != b.LawID {
			return a.LawID < b.LawID
		}
		if a.Article != b.Article {
			return a.Article < b.Article
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.Row < b.Row
	})
}

// Stats says how much a CheckDB run looked at.
type Stats struct {
	Texts      int  // texts checked
	Sources    int  // sources read
	Amendments int  // of the texts, amendments' wordings (text_ar, old_text_ar)
	Inferred   bool // the database predates the source columns: sources were inferred from ids
}

// ErrNothingToCheck: the database holds no law text, so the guard has
// nothing to vouch for (a half-built or emptied database). It is not a pass.
var ErrNothingToCheck = errors.New("no law text to check: the database has no articles")

// CheckDB checks every law text in the database at dsn.
func CheckDB(ctx context.Context, dsn string, src SourceStore, edits ApprovedEdits, known Known) ([]Violation, error) {
	vs, _, err := CheckDBStats(ctx, dsn, src, edits, known)
	return vs, err
}

// CheckDBStats is CheckDB, saying also what it read. Articles are read in
// law order, so a law's page is parsed once for all its articles and for
// the versions that share its source; the versions (far fewer) are held in
// memory meanwhile. Sources are checked on a few cores at once.
func CheckDBStats(ctx context.Context, dsn string, src SourceStore, edits ApprovedEdits, known Known) ([]Violation, Stats, error) {
	var st Stats
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, st, err
	}
	defer conn.Close(context.Background())
	var cols int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public'
		AND table_name IN ('legislation_article', 'article_version') AND column_name IN ('source_kind', 'source_id')`).Scan(&cols); err != nil {
		return nil, st, err
	}
	var hasVersions, hasAmendments *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.article_version')::text, to_regclass('public.amendment')::text`).
		Scan(&hasVersions, &hasAmendments); err != nil {
		return nil, st, err
	}
	want := 2
	if hasVersions != nil {
		want = 4
	}
	st.Inferred = cols < want

	versions := map[Source][]ArticleText{}
	if hasVersions != nil {
		vs, err := loadVersions(ctx, conn, st.Inferred)
		if err != nil {
			return nil, st, err
		}
		for _, v := range vs {
			versions[v.Source] = append(versions[v.Source], v)
		}
	}
	// Amendments' wordings: each is the amending article's (its own
	// by_law_id and by_article), whatever the database's age.
	if hasAmendments != nil {
		as, err := loadAmendments(ctx, conn)
		if err != nil {
			return nil, st, err
		}
		st.Amendments = len(as)
		for _, a := range as {
			versions[a.Source] = append(versions[a.Source], a)
		}
	}

	// Groups go to a few workers; violations come back.
	type job struct {
		s     Source
		texts []ArticleText
	}
	jobs := make(chan job, 8)
	c := checker{src: src, edits: edits, known: known}
	var mu sync.Mutex
	var out []Violation
	var wg sync.WaitGroup
	workers := min(4, max(1, runtime.NumCPU()/2))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				v := c.group(j.s, j.texts)
				mu.Lock()
				out = append(out, v...)
				mu.Unlock()
			}
		}()
	}
	send := func(s Source, texts []ArticleText) {
		texts = append(texts, versions[s]...)
		delete(versions, s)
		st.Texts += len(texts)
		st.Sources++
		jobs <- job{s, texts}
	}

	q := `SELECT law_id, number, text_ar, text_fr, coalesce(source_kind, ''), coalesce(source_id, '')
		FROM legislation_article ORDER BY law_id, position`
	if st.Inferred {
		q = `SELECT law_id, number, text_ar, text_fr, '', '' FROM legislation_article ORDER BY law_id, position`
	}
	rows, err := conn.Query(ctx, q)
	if err != nil {
		close(jobs)
		wg.Wait()
		return nil, st, err
	}
	var cur Source
	var group []ArticleText
	for rows.Next() {
		var law int64
		var number, ar string
		var fr *string
		var s Source
		if err := rows.Scan(&law, &number, &ar, &fr, &s.Kind, &s.ID); err != nil {
			rows.Close()
			close(jobs)
			wg.Wait()
			return nil, st, err
		}
		if st.Inferred {
			k, id := build.LawSource(int(law))
			s = Source{k, id}
		}
		if s != cur && len(group) > 0 {
			send(cur, group)
			group = nil
		}
		cur = s
		group = append(group, ArticleText{LawID: law, Article: number, Text: ar, Source: s})
		if fr != nil && *fr != "" {
			group = append(group, ArticleText{LawID: law, Article: number, Text: *fr, Source: s})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		close(jobs)
		wg.Wait()
		return nil, st, err
	}
	if len(group) > 0 {
		send(cur, group)
	}
	// Versions whose source no article shares: amendments, edits, and any
	// row naming a source no article has.
	rest := make([]Source, 0, len(versions))
	for s := range versions {
		rest = append(rest, s)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].String() < rest[j].String() })
	for _, s := range rest {
		send(s, nil)
	}
	close(jobs)
	wg.Wait()
	sortViolations(out)
	if st.Texts == 0 {
		return nil, st, ErrNothingToCheck
	}
	return out, st, nil
}

// loadAmendments reads every amendment's wordings: text_ar, the wording it
// sets, and old_text_ar, the one it quotes as replaced. Both are read from
// the amending article, the row's by_law_id and by_article.
func loadAmendments(ctx context.Context, conn *pgx.Conn) ([]ArticleText, error) {
	rows, err := conn.Query(ctx, `SELECT id, by_law_id, by_article, coalesce(text_ar, ''), coalesce(old_text_ar, '')
		FROM amendment WHERE coalesce(text_ar, '') <> '' OR coalesce(old_text_ar, '') <> '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArticleText
	for rows.Next() {
		var id, byLaw int64
		var byArticle, text, old string
		if err := rows.Scan(&id, &byLaw, &byArticle, &text, &old); err != nil {
			return nil, err
		}
		src := Source{build.SourceAmendment, build.AmendmentSource(int(byLaw), byArticle)}
		for _, c := range []struct{ col, text string }{{"text_ar", text}, {"old_text_ar", old}} {
			if c.text != "" {
				out = append(out, ArticleText{LawID: byLaw, Article: byArticle, Text: c.text, Source: src,
					Row: fmt.Sprintf("amendment %d %s", id, c.col)})
			}
		}
	}
	return out, rows.Err()
}

// loadVersions reads every article_version row with a wording. In a
// database built before the source columns (inferred), each version's
// source is worked out the way the build's writers now store it: the
// Centre's text from its law, an amendment's wording from the change that
// published it (or, for a wording an amendment quotes as replaced, the next
// version's change), a person's correction as its edit.
func loadVersions(ctx context.Context, conn *pgx.Conn, inferred bool) ([]ArticleText, error) {
	if !inferred {
		rows, err := conn.Query(ctx, `SELECT law_id, article_number, major, minor, text_ar,
			coalesce(source_kind, ''), coalesce(source_id, '') FROM article_version WHERE text_ar IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ArticleText, error) {
			var t ArticleText
			var major, minor int64
			err := r.Scan(&t.LawID, &t.Article, &major, &minor, &t.Text, &t.Source.Kind, &t.Source.ID)
			t.VersionID = major*1000 + minor
			return t, err
		})
	}
	type change struct {
		byLaw     int
		byArticle string
		text, old string
	}
	changes := map[int64]change{}
	rows, err := conn.Query(ctx, `SELECT id, by_law_id, by_article, coalesce(text_ar, ''), coalesce(old_text_ar, '') FROM amendment`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var c change
		if err := rows.Scan(&id, &c.byLaw, &c.byArticle, &c.text, &c.old); err != nil {
			rows.Close()
			return nil, err
		}
		changes[id] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = conn.Query(ctx, `SELECT law_id, article_number, major, minor, text_ar, text_source, amendment_id
		FROM article_version ORDER BY law_id, article_number, major, minor`)
	if err != nil {
		return nil, err
	}
	type row struct {
		t      ArticleText
		text   *string
		source string
		amend  *int64
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		var major, minor int64
		err := r.Scan(&x.t.LawID, &x.t.Article, &major, &minor, &x.text, &x.source, &x.amend)
		x.t.VersionID = major*1000 + minor
		return x, err
	})
	if err != nil {
		return nil, err
	}
	var out []ArticleText
	for i, x := range all {
		if x.text == nil {
			continue
		}
		t := x.t
		t.Text = *x.text
		switch x.source {
		case "centre":
			k, id := build.LawSource(int(t.LawID))
			t.Source = Source{k, id}
		case "editorial":
			t.Source = Source{build.SourceEdit, build.CorrectionSource(int(t.LawID), t.Article)}
		case "amendment":
			if x.amend != nil && changes[*x.amend].text == t.Text {
				c := changes[*x.amend]
				t.Source = Source{build.SourceAmendment, build.AmendmentSource(c.byLaw, c.byArticle)}
			} else if i+1 < len(all) && all[i+1].t.LawID == t.LawID && all[i+1].t.Article == t.Article && all[i+1].amend != nil {
				c := changes[*all[i+1].amend]
				t.Source = Source{build.SourceAmendment, build.AmendmentSource(c.byLaw, c.byArticle)}
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// Guard is what export, snapshot and check-wording run first.
type Guard struct {
	Sources SourceStore
	Edits   ApprovedEdits
	Known   Known
}

// Paths say where a Guard's files are; Open reads them.
type Paths struct {
	Archive  string   // archive/
	Bundle   string   // the lebgazette sader-bundle folder
	Manifest string   // its manifest; empty: ManifestPath(Bundle), beside it
	Edits    []string // approved_edits.tsv, article_corrections.tsv, centre_errors.tsv
	Known    string   // provenance_known.tsv
}

// DefaultPaths are a checkout's own, from its root (the nightly runs there).
func DefaultPaths(repo string) Paths {
	return Paths{
		Archive: filepath.Join(repo, "archive"),
		Bundle:  filepath.Join(repo, "manual", "scans"),
		Edits: []string{filepath.Join(repo, "db", "approved_edits.tsv"), filepath.Join(repo, "db", "article_corrections.tsv"),
			filepath.Join(repo, "db", "centre_errors.tsv")},
		Known: filepath.Join(repo, "db", "provenance_known.tsv"),
	}
}

// Open loads a Guard. A file that cannot be read is an error: the guard
// fails closed.
func Open(p Paths) (Guard, error) {
	edits, err := LoadApprovedEdits(p.Edits...)
	if err != nil {
		return Guard{}, fmt.Errorf("wording guard: %w", err)
	}
	known, err := LoadKnown(p.Known)
	if err != nil {
		return Guard{}, fmt.Errorf("wording guard: %w", err)
	}
	return Guard{Sources: &ArchiveSources{Root: p.Archive, Bundle: p.Bundle, Manifest: p.Manifest}, Edits: edits, Known: known}, nil
}

// Gate checks the database and returns a *BlockedError (ErrBlocked, exit
// code 4) when any text lacks its source. A guard with no sources, or a
// database with no law text (ErrNothingToCheck), fails too.
func (g Guard) Gate(ctx context.Context, dsn string) error {
	if g.Sources == nil {
		return errors.New("wording guard: no sources to check against")
	}
	vs, _, err := CheckDBStats(ctx, dsn, g.Sources, g.Edits, g.Known)
	if err != nil {
		return fmt.Errorf("wording guard: %w", err)
	}
	if len(vs) > 0 {
		return &BlockedError{Violations: vs}
	}
	return nil
}

// Marker ends the schema lines of the internal provenance columns.
const Marker = "-- internal: provenance"

// internalCols are the provenance columns, by table. legislation_scan has a
// source_id of its own (the scan's id at SaderLex), which is public.
var internalCols = map[string]bool{
	"legislation_article.source_kind": true, "legislation_article.source_id": true,
	"article_version.source_kind": true, "article_version.source_id": true,
}

// Internal says whether a table's column is a provenance column, which
// export, snapshot and hf.sh leave out.
func Internal(table, column string) bool { return internalCols[table+"."+column] }

// StripInternal drops the provenance columns (lines ending with Marker,
// and the comment lines just above them) from schema SQL, for the schema
// export publishes.
func StripInternal(schema string) string {
	var out []string
	for _, l := range strings.Split(schema, "\n") {
		if strings.HasSuffix(strings.TrimRight(l, " "), Marker) {
			for len(out) > 0 && strings.HasPrefix(strings.TrimSpace(out[len(out)-1]), "--") {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
