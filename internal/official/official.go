// Package official loads texts published from official page scans (see
// db/official.sql) and lists what the Lebanese University archive already has,
// so a text is never served twice: UL first, scans only where UL lacks.
package official

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/parse"
)

// ULIndex writes id, kind, number, date, title of every UL text issued in or
// after minYear (tab-separated), reading only the git archive.
func ULIndex(a archive.Archive, minYear int, w io.Writer) error {
	es, err := a.Index("law")
	if err != nil {
		return err
	}
	sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
	fmt.Fprintln(w, "id\tkind\tnumber\tdate\ttitle")
	for _, e := range es {
		if e.ID < 300000 { // the Centre's newest ids; older texts cannot be from these years
			continue
		}
		raw, err := a.Read("law", e.ID)
		if err != nil {
			continue
		}
		l, err := parse.ParseLaw(e.ID, raw)
		if err != nil || l == nil || l.IssuedOn == nil || l.IssuedOn.Year() < minYear {
			continue
		}
		n := ""
		if l.Number != nil {
			n = *l.Number
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", e.ID, l.Kind, n, l.IssuedOn.Format("2006-01-02"), strings.NewReplacer("\t", " ", "\n", " ").Replace(l.Title))
	}
	return nil
}

type meta struct {
	ID         int     `json:"id"`
	Source     string  `json:"source"`
	SourceName string  `json:"source_name"`
	SourceURL  string  `json:"source_url"`
	Kind       string  `json:"kind"`
	Number     string  `json:"number"`
	Date       string  `json:"date"`
	Year       int     `json:"year"`
	Title      string  `json:"title"`
	Group      string  `json:"group"`
	Pages      int     `json:"pages"`
	Issue      string  `json:"gazette_issue"`
	IssueDate  string  `json:"gazette_issue_date"`
	PageFrom   int     `json:"gazette_page_from"`
	PageTo     int     `json:"gazette_page_to"`
	PCMURL     string  `json:"pcm_url"`
	Words      int     `json:"words"`
	Flagged    int     `json:"flagged_spans"`
	Agreement  float64 `json:"agreement"`
	Status     string  `json:"status"`
}

var reLead = regexp.MustCompile(`^\d+`)

func nullDate(s string) any {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// Load reads every bundle in bundleDir (written by `lebgazette sader-bundle`),
// upserts it into official_text, copies its page images and PDF to filesDir/<id>/
// (p1.png, p2.png ..., source.pdf), and marks the rows UL already has as superseded.
func Load(ctx context.Context, dsn, bundleDir, filesDir string, log func(string, ...any)) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, db.OfficialSchema); err != nil {
		return err
	}
	dirs, _ := filepath.Glob(filepath.Join(bundleDir, "*", "meta.json"))
	n := 0
	for _, mp := range dirs {
		dir := filepath.Dir(mp)
		var m meta
		b, err := os.ReadFile(mp)
		if err != nil || json.Unmarshal(b, &m) != nil || m.ID == 0 {
			log("skip %s: bad meta", dir)
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, "text.json"))
		if err != nil {
			log("skip %d: no text.json", m.ID)
			continue
		}
		var numInt any
		if x := reLead.FindString(m.Number); x != "" {
			v, _ := strconv.Atoi(x)
			numInt = v
		}
		_, err = conn.Exec(ctx, `INSERT INTO official_text
			(id, source, source_name, source_url, kind, number, number_int, issued_on, year, title, grp, pages,
			 gazette_issue, gazette_issue_date, gazette_page_from, gazette_page_to, pcm_url, words, flagged_spans, agreement, status, body)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
			ON CONFLICT (id) DO UPDATE SET source_name=EXCLUDED.source_name, source_url=EXCLUDED.source_url, kind=EXCLUDED.kind,
			 number=EXCLUDED.number, number_int=EXCLUDED.number_int, issued_on=EXCLUDED.issued_on, year=EXCLUDED.year, title=EXCLUDED.title,
			 grp=EXCLUDED.grp, pages=EXCLUDED.pages, gazette_issue=EXCLUDED.gazette_issue, gazette_issue_date=EXCLUDED.gazette_issue_date,
			 gazette_page_from=EXCLUDED.gazette_page_from, gazette_page_to=EXCLUDED.gazette_page_to, pcm_url=EXCLUDED.pcm_url,
			 words=EXCLUDED.words, flagged_spans=EXCLUDED.flagged_spans, agreement=EXCLUDED.agreement, status=EXCLUDED.status,
			 body=EXCLUDED.body, loaded_at=now()`,
			m.ID, m.Source, m.SourceName, nullStr(m.SourceURL), m.Kind, nullStr(m.Number), numInt, nullDate(m.Date), nullInt(m.Year),
			m.Title, nullStr(m.Group), m.Pages, nullStr(m.Issue), nullDate(m.IssueDate), nullInt(m.PageFrom), nullInt(m.PageTo),
			nullStr(m.PCMURL), m.Words, m.Flagged, m.Agreement, m.Status, body)
		if err != nil {
			return fmt.Errorf("text %d: %w", m.ID, err)
		}
		if err := copyAssets(dir, filepath.Join(filesDir, strconv.Itoa(m.ID))); err != nil {
			return fmt.Errorf("text %d assets: %w", m.ID, err)
		}
		n++
	}
	tag, err := conn.Exec(ctx, `UPDATE official_text o SET superseded_by_law_id = l.id
		FROM legislation l WHERE l.kind = o.kind AND l.number_int = o.number_int AND l.number_int IS NOT NULL
		AND o.year = extract(year FROM l.issued_on)::int AND o.superseded_by_law_id IS DISTINCT FROM l.id`)
	if err != nil {
		return err
	}
	log("official-load: %d texts loaded, %d newly superseded by UL", n, tag.RowsAffected())
	return nil
}

var rePage = regexp.MustCompile(`^page-(\d+)\.png$`)

func copyAssets(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		name := e.Name()
		to := ""
		if m := rePage.FindStringSubmatch(name); m != nil {
			k, _ := strconv.Atoi(m[1])
			to = fmt.Sprintf("p%d.png", k)
		} else if name == "source.pdf" {
			to = name
		}
		if to == "" {
			continue
		}
		in, err := os.Open(filepath.Join(src, name))
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(dst, to))
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
