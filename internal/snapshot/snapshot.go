// Package snapshot exports the local Postgres database to a set of plain
// SQL files that Cloudflare D1 (SQLite) can import: a schema.sql (DDL) and
// chunked data_NNNN.sql files, each a handful of INSERT statements kept
// under D1's per-statement size so the REST import API accepts them
// (deploy/snapshot.sh loads them with "wrangler d1 execute --file", which
// drives that same init/upload/ingest/poll API under the hood).
//
// Reads only: this package never writes to Postgres, and a small machine's RAM
// rules out loading a table into memory, so every table is streamed
// row by row from a cursor straight to disk.
//
// The D1 schema is a flattened, MCP-shaped copy of db/schema.sql and
// db/graph.sql: joins the Go side (internal/site) would do at request time
// (legislation_kind, court, code, source_page) are done once here instead,
// so the Worker never needs a JOIN. Arabic search text is normalized here
// too (arNorm, the Go twin of Postgres's ar_norm) and stored in FTS5
// tables, because SQLite has no such function of its own.
package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
	"github.com/taktekhq/lebanese-legislation/internal/provenance"
)

// Options configures one export run.
type Options struct {
	DSN    string // Postgres connection string, read-only use
	OutDir string // written: schema.sql, data_0001.sql..., manifest.json
	Log    func(format string, args ...any)
	// Guard is the wording guard, run before anything is written: a law
	// text without its archived source blocks the snapshot (exit 4).
	Guard provenance.Guard
}

// Manifest records what was exported, so deploy/snapshot.sh can verify the
// loaded D1 database has the same row counts before switching the Worker to it.
type Manifest struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Tables      map[string]int `json:"tables"`
	DataFiles   []string       `json:"data_files"` // in the order they must be applied
	Oversize    int            `json:"oversize"`   // rows written to oversize.jsonl instead (see internal/snapshot/oversize.go)
}

const (
	maxStmtBytes = 60_000    // keep each INSERT well under D1's statement size limit
	maxFileBytes = 3_000_000 // ~3MB target per chunk file: a dropped upload (flaky uplink) costs one small retry, not a big one
)

func (o Options) log(f string, a ...any) {
	if o.Log != nil {
		o.Log(f, a...)
	}
}

// Run exports every table the MCP Worker's tools need, as D1-ready SQL.
func Run(ctx context.Context, opt Options) (Manifest, error) {
	m := Manifest{GeneratedAt: time.Now().UTC(), Tables: map[string]int{}}
	if err := opt.Guard.Gate(ctx, opt.DSN); err != nil {
		return m, err
	}
	if err := os.MkdirAll(opt.OutDir, 0o755); err != nil {
		return m, err
	}
	for _, old := range mustGlob(opt.OutDir, "data_*.sql") {
		os.Remove(old)
	}

	pool, err := pgxpool.New(ctx, opt.DSN)
	if err != nil {
		return m, fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return m, fmt.Errorf("ping: %w", err)
	}

	if err := os.WriteFile(filepath.Join(opt.OutDir, "schema.sql"), []byte(d1Schema), 0o644); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(opt.OutDir, "drop.sql"), []byte(DropSQL()), 0o644); err != nil {
		return m, err
	}

	ch, err := newChunker(opt.OutDir, maxFileBytes)
	if err != nil {
		return m, err
	}
	defer ch.close()

	ov, err := newOversizeWriter(opt.OutDir)
	if err != nil {
		return m, err
	}
	defer ov.close()

	type step struct {
		name string
		fn   func(context.Context, *pgxpool.Pool, *chunker, *oversizeWriter) (int, error)
	}
	steps := []step{
		{"legislation", exportLegislation},
		{"legislation_section", exportSections},
		{"legislation_article", exportArticles},
		{"centre_error", exportCentreErrors},
		{"gazette_issue", exportGazetteIssues},
		{"gazette_entry", exportGazetteEntries},
		{"ruling", exportRulings},
		{"ruling_page", exportRulingPages},
		{"graph_edge", exportGraphEdges},
		{"possibly_affected", exportPossiblyAffected},
	}
	for _, s := range steps {
		opt.log("snapshot: exporting %s...", s.name)
		n, err := s.fn(ctx, pool, ch, ov)
		if err != nil {
			return m, fmt.Errorf("export %s: %w", s.name, err)
		}
		m.Tables[s.name] = n
		opt.log("snapshot: %s: %d rows", s.name, n)
	}
	if err := ch.close(); err != nil {
		return m, err
	}
	if err := ov.close(); err != nil {
		return m, err
	}
	m.DataFiles = ch.files
	m.Oversize = ov.n

	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(opt.OutDir, "manifest.json"), mb, 0o644); err != nil {
		return m, err
	}
	return m, nil
}

// Schema is the D1 schema the snapshot writes: the provenance columns are
// not in it.
func Schema() string { return d1Schema }

var schemaTableRE = regexp.MustCompile(`(?m)^CREATE (?:VIRTUAL )?TABLE ([A-Za-z_]+)`)

// SchemaTables names every table Schema creates, in schema order.
func SchemaTables() []string {
	var names []string
	for _, m := range schemaTableRE.FindAllStringSubmatch(d1Schema, -1) {
		names = append(names, m[1])
	}
	return names
}

// DropSQL drops every table Schema creates. deploy/snapshot.sh runs it on the
// idle D1 database before schema.sql, so a load that stopped halfway (its
// tables left behind) does not make the next CREATE TABLE fail. It is read
// off the schema so a new table cannot be missed (a hand-kept list once
// lacked ruling_page).
func DropSQL() string {
	var b strings.Builder
	for _, t := range SchemaTables() {
		fmt.Fprintf(&b, "DROP TABLE IF EXISTS %s;\n", t)
	}
	return b.String()
}

func mustGlob(dir, pattern string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return matches
}

// ---------------------------------------------------------------- the D1 schema

const d1Schema = `
PRAGMA foreign_keys = OFF;

CREATE TABLE legislation (
  id               INTEGER PRIMARY KEY,
  kind             TEXT,
  kind_en          TEXT,
  number           TEXT,
  issued_on        TEXT,
  title            TEXT NOT NULL,
  title_norm       TEXT NOT NULL,
  status           TEXT NOT NULL,
  gazette_number   TEXT,
  gazette_date     TEXT,
  gazette_issue_id INTEGER,
  article_count    INTEGER NOT NULL DEFAULT 0,
  code_slug        TEXT,
  code_name_en     TEXT,
  code_name_fr     TEXT,
  source_url       TEXT,
  -- Unofficial English rendering of the title (legislation_title_en), written
  -- by a model from the Arabic title alone. The Arabic title is the official,
  -- more accurate one. NULL when none exists.
  title_en         TEXT
);
CREATE INDEX idx_legislation_status ON legislation(status);
CREATE INDEX idx_legislation_gazette_date ON legislation(gazette_date);
CREATE INDEX idx_legislation_issued_on ON legislation(issued_on);

CREATE TABLE legislation_article (
  id            INTEGER PRIMARY KEY,
  law_id        INTEGER NOT NULL,
  position      INTEGER NOT NULL,
  number        TEXT NOT NULL,
  note          TEXT,
  text_ar       TEXT NOT NULL,
  text_fr       TEXT,
  text_norm     TEXT NOT NULL,
  in_force_from TEXT,
  section_id    INTEGER,
  -- From article_in_force (db/amendments.sql): the wording in force today
  -- when an amendment changed the Centre's text (NULL when version is
  -- 1.0, i.e. text_ar is still current), the article's state, whether
  -- the latest change still lacks a full wording, and since when.
  current_text_ar TEXT,
  version       TEXT NOT NULL DEFAULT '1.0',
  state         TEXT NOT NULL DEFAULT 'in_force',
  pending       INTEGER NOT NULL DEFAULT 0,
  valid_from    TEXT,
  calculated_current TEXT
);
CREATE INDEX idx_article_law ON legislation_article(law_id, position);
-- Not unique: the Centre repeats an article number within a text a
-- handful of times (25 pairs across the whole corpus, found while
-- loading the first real snapshot); get_article picks the lowest
-- position, same as internal/site's own articleDetail does.
CREATE INDEX idx_article_law_number ON legislation_article(law_id, number);

-- A text's chapters (كتاب، باب، فصل، نبذة): the table of contents, and
-- legislation_article.section_id says which one an article sits under.
CREATE TABLE legislation_section (
  id        INTEGER PRIMARY KEY,
  law_id    INTEGER NOT NULL,
  parent_id INTEGER,
  position  INTEGER NOT NULL,
  depth     INTEGER NOT NULL,
  heading   TEXT NOT NULL,
  note      TEXT
);
CREATE INDEX idx_section_law ON legislation_section(law_id, position);

-- Errors in the Centre's own text of an article (db/centre_errors.tsv).
CREATE TABLE centre_error (
  law_id  INTEGER NOT NULL,
  number  TEXT NOT NULL,
  note_en TEXT NOT NULL,
  note_ar TEXT NOT NULL,
  PRIMARY KEY (law_id, number)
);

CREATE TABLE gazette_issue (
  id           INTEGER PRIMARY KEY,
  number       TEXT,
  published_on TEXT,
  source_url   TEXT
);

CREATE TABLE gazette_entry (
  issue_id INTEGER NOT NULL,
  law_id   INTEGER NOT NULL,
  PRIMARY KEY (issue_id, law_id)
);
CREATE INDEX idx_gazette_entry_law ON gazette_entry(law_id);
CREATE INDEX idx_gazette_issue_published ON gazette_issue(published_on);

CREATE TABLE ruling (
  id         INTEGER PRIMARY KEY,
  court      TEXT,
  court_en   TEXT,
  number     TEXT,
  year       INTEGER,
  decided_on TEXT,
  title      TEXT NOT NULL,
  president  TEXT,
  members    TEXT, -- JSON array
  text_ar    TEXT,
  text_norm  TEXT NOT NULL,
  source_url TEXT,
  pdf_url    TEXT -- the judgment's scan on the Lebanese University's site, when it has one
);
CREATE INDEX idx_ruling_decided ON ruling(decided_on);

CREATE TABLE ruling_page (
  ruling_id      INTEGER, -- the judgment this page belongs to
  n              INTEGER, -- page number in the PDF, from 1
  text_ar        TEXT,    -- machine reading of the scanned page
  low_confidence INTEGER, -- 1 when hard to read (mostly handwriting), otherwise 0
  PRIMARY KEY (ruling_id, n)
);

-- The graph: every link between two items, addressed by from_key/to_key
-- (law/<id>, law/<id>/art/<number>, ruling/<id>, opinion/<id>,
-- research/<id>, gazette/<id>), same shape as db/graph.sql's graph_edge.
CREATE TABLE graph_edge (
  id           INTEGER PRIMARY KEY,
  from_key     TEXT NOT NULL,
  to_key       TEXT,
  relation     TEXT NOT NULL,
  source       TEXT NOT NULL,
  status       TEXT NOT NULL,
  from_type    TEXT NOT NULL,
  from_id      INTEGER NOT NULL,
  from_article TEXT,
  to_law_id    INTEGER,
  to_article   TEXT,
  to_cite      TEXT,
  part         TEXT,
  quote        TEXT
);
CREATE INDEX idx_edge_from_key ON graph_edge(from_key);
CREATE INDEX idx_edge_to_key ON graph_edge(to_key);
CREATE INDEX idx_edge_to_law ON graph_edge(to_law_id, to_article);
CREATE INDEX idx_edge_from_id ON graph_edge(from_id, from_type);

-- Texts and articles possibly touched by a general repeal: a list to
-- check, never a finding (db/graph.sql's possibly_affected view).
CREATE TABLE possibly_affected (
  repealing_law_id  INTEGER NOT NULL,
  repealing_article TEXT,
  law_id            INTEGER NOT NULL,
  article_number    TEXT
);
CREATE INDEX idx_affected_law ON possibly_affected(law_id);

-- Search: FTS5 over normalized text, built fresh at export time (SQLite has
-- no ar_norm of its own). Kept as separate tables, not external-content, so
-- the original text in the base tables above is untouched either way.
CREATE VIRTUAL TABLE law_search USING fts5(law_id UNINDEXED, title_norm);
-- The unofficial English titles, for English queries (ranked below Arabic matches).
CREATE VIRTUAL TABLE law_search_en USING fts5(law_id UNINDEXED, title_en, tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE article_search USING fts5(law_id UNINDEXED, number UNINDEXED, text_norm);
-- The French texts, where the Centre published one; accents folded so
-- "penal" finds "pénal".
CREATE VIRTUAL TABLE article_search_fr USING fts5(law_id UNINDEXED, number UNINDEXED, text_fr, tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE ruling_search USING fts5(ruling_id UNINDEXED, text_norm);
`

// ---------------------------------------------------------------- exporters

func exportLegislation(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT l.id, l.kind, k.name_en, l.number, l.issued_on, l.title, l.status,
		       l.gazette_number, l.gazette_date,
		       (SELECT min(ge.issue_id) FROM gazette_entry ge WHERE ge.law_id = l.id),
		       (SELECT count(*) FROM legislation_article a WHERE a.law_id = l.id),
		       c.slug, c.name_en, c.name_fr,
		       sp.url, te.title_en
		FROM legislation l
		JOIN legislation_kind k ON k.name_ar = l.kind
		LEFT JOIN code c ON c.law_id = l.id
		LEFT JOIN source_page sp ON sp.doc_type = 'law' AND sp.centre_id = l.id
		LEFT JOIN legislation_title_en te ON te.law_id = l.id
		ORDER BY l.id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	b := newBatcher(ch, "legislation",
		"id,kind,kind_en,number,issued_on,title,title_norm,status,gazette_number,gazette_date,gazette_issue_id,article_count,code_slug,code_name_en,code_name_fr,source_url,title_en")
	n := 0
	for rows.Next() {
		var id int
		var kind, title, status string
		var kindEn, number, codeSlug, codeNameEn, codeNameFr, sourceURL, titleEn *string
		var issuedOn, gazetteDate *time.Time
		var gazetteNumber *string
		var gazetteIssueID *int
		var articleCount int
		if err := rows.Scan(&id, &kind, &kindEn, &number, &issuedOn, &title, &status,
			&gazetteNumber, &gazetteDate, &gazetteIssueID, &articleCount, &codeSlug, &codeNameEn, &codeNameFr, &sourceURL, &titleEn); err != nil {
			return n, err
		}
		tuple := "(" + strings.Join([]string{
			sqlInt(id), sqlStr(kind), sqlNullStr(kindEn), sqlNullStr(number), sqlDate(issuedOn),
			sqlStr(title), sqlStr(arNorm(title)), sqlStr(status), sqlNullStr(gazetteNumber), sqlDate(gazetteDate),
			sqlNullInt(gazetteIssueID), sqlInt(articleCount), sqlNullStr(codeSlug), sqlNullStr(codeNameEn), sqlNullStr(codeNameFr), sqlNullStr(sourceURL), sqlNullStr(titleEn),
		}, ",") + ")"
		if err := b.add(tuple); err != nil {
			return n, err
		}
		if err := b2add(ch, "law_search", "law_id,title_norm", "("+sqlInt(id)+","+sqlStr(arNorm(title))+")"); err != nil {
			return n, err
		}
		if titleEn != nil && strings.TrimSpace(*titleEn) != "" {
			if err := b2add(ch, "law_search_en", "law_id,title_en", "("+sqlInt(id)+","+sqlStr(*titleEn)+")"); err != nil {
				return n, err
			}
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

// b2add is a one-off insert (not batched across calls) kept simple since
// law_search has one short row per law; batching happens inside the
// chunker's file-size accounting instead.
func b2add(ch *chunker, table, cols, tuple string) error {
	return ch.writeStmt("INSERT INTO " + table + " (" + cols + ") VALUES " + tuple + ";\n")
}

func exportArticles(ctx context.Context, pool *pgxpool.Pool, ch *chunker, ov *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, articlesQuery)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return exportArticleRows(rows, ch, ov)
}

func exportArticleRows(rows pgx.Rows, ch *chunker, ov *oversizeWriter) (int, error) {
	artCols := []string{"id", "law_id", "position", "number", "note", "text_ar", "text_fr", "text_norm", "in_force_from",
		"section_id", "current_text_ar", "version", "state", "pending", "valid_from", "calculated_current"}
	searchCols := []string{"law_id", "number", "text_norm"}
	frCols := []string{"law_id", "number", "text_fr"}
	bArt := newBatcher(ch, "legislation_article", strings.Join(artCols, ","))
	bSearch := newBatcher(ch, "article_search", strings.Join(searchCols, ","))
	bFr := newBatcher(ch, "article_search_fr", strings.Join(frCols, ","))
	n, id := 0, 0
	for rows.Next() {
		var lawID, position int
		var sectionID *int
		var number, textAr, version, state string
		var note, textFr, currentAr *string
		var inForce, validFrom *time.Time
		var pending, annulled bool
		if err := rows.Scan(&lawID, &position, &number, &note, &textAr, &textFr, &inForce, &sectionID,
			&currentAr, &version, &state, &pending, &validFrom, &annulled); err != nil {
			return n, err
		}
		// Only carry the in-force wording when it differs from the
		// Centre's, so the 99% of articles never amended cost nothing.
		if version == "1.0" || (currentAr != nil && *currentAr == textAr) {
			currentAr = nil
		}
		pendingInt := 0
		if pending {
			pendingInt = 1
		}

		source := textAr
		if currentAr != nil {
			source = *currentAr
		}
		from := validFrom
		if inForce != nil && (from == nil || inForce.After(*from)) {
			from = inForce
		}
		var calculated *string
		calculatedText := ""
		if record := calculatedcurrent.Calculate(calculatedcurrent.Input{LawID: lawID, Number: number, Source: source, State: state, Pending: pending, Annulled: annulled, ValidFrom: from}); record != nil {
			b, err := json.Marshal(record)
			if err != nil {
				return n, err
			}
			v := string(b)
			calculated = &v
			calculatedText = record.TextAr
		}
		id++
		textAr = scanReadings(textAr)
		norm := arNorm(textAr)
		searchNorm := norm
		if calculated != nil {
			searchNorm += " " + arNorm(calculatedText)
		}
		tuple := "(" + strings.Join([]string{
			sqlInt(id), sqlInt(lawID), sqlInt(position), sqlStr(number), sqlNullStr(note),
			sqlStr(textAr), sqlNullStr(textFr), sqlStr(norm), sqlDate(inForce), sqlNullInt(sectionID),
			sqlNullStr(currentAr), sqlStr(version), sqlStr(state), sqlInt(pendingInt), sqlDate(validFrom), sqlNullStr(calculated),
		}, ",") + ")"
		searchTuple := "(" + strings.Join([]string{sqlInt(lawID), sqlStr(number), sqlStr(searchNorm)}, ",") + ")"

		// A handful of articles are long enough that their row, inlined
		// as SQL text, would cross D1's 100,000-byte statement limit; see
		// oversize.go. Those go to deploy/load_oversize.py's side channel
		// instead, as raw (unescaped) values.
		if len(tuple) > oversizeThreshold {
			if err := ov.write("legislation_article", artCols,
				[]any{id, lawID, position, number, derefAny(note), textAr, derefAny(textFr), norm, dateAny(inForce),
					intAny(sectionID), derefAny(currentAr), version, state, pendingInt, dateAny(validFrom), derefAny(calculated)}); err != nil {
				return n, err
			}
		} else if err := bArt.add(tuple); err != nil {
			return n, err
		}
		if len(searchTuple) > oversizeThreshold {
			if err := ov.write("article_search", searchCols, []any{lawID, number, searchNorm}); err != nil {
				return n, err
			}
		} else if err := bSearch.add(searchTuple); err != nil {
			return n, err
		}
		if textFr != nil && strings.TrimSpace(*textFr) != "" {
			frTuple := "(" + strings.Join([]string{sqlInt(lawID), sqlStr(number), sqlStr(*textFr)}, ",") + ")"
			if len(frTuple) > oversizeThreshold {
				if err := ov.write("article_search_fr", frCols, []any{lawID, number, *textFr}); err != nil {
					return n, err
				}
			} else if err := bFr.add(frTuple); err != nil {
				return n, err
			}
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if err := bArt.flush(); err != nil {
		return n, err
	}
	if err := bFr.flush(); err != nil {
		return n, err
	}
	return n, bSearch.flush()
}

func exportGazetteIssues(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT g.id, g.number, g.published_on, sp.url
		FROM gazette_issue g LEFT JOIN source_page sp ON sp.doc_type = 'journal' AND sp.centre_id = g.id
		ORDER BY g.id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "gazette_issue", "id,number,published_on,source_url")
	n := 0
	for rows.Next() {
		var id int
		var number, sourceURL *string
		var publishedOn *time.Time
		if err := rows.Scan(&id, &number, &publishedOn, &sourceURL); err != nil {
			return n, err
		}
		tuple := "(" + strings.Join([]string{sqlInt(id), sqlNullStr(number), sqlDate(publishedOn), sqlNullStr(sourceURL)}, ",") + ")"
		if err := b.add(tuple); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

func exportGazetteEntries(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `SELECT issue_id, law_id FROM gazette_entry ORDER BY issue_id, law_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "gazette_entry", "issue_id,law_id")
	n := 0
	for rows.Next() {
		var issueID, lawID int
		if err := rows.Scan(&issueID, &lawID); err != nil {
			return n, err
		}
		if err := b.add("(" + sqlInt(issueID) + "," + sqlInt(lawID) + ")"); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

func exportRulings(ctx context.Context, pool *pgxpool.Pool, ch *chunker, ov *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.id, r.court, c.name_en, r.number, r.year, r.decided_on, r.title, r.president, r.members, r.text_ar, sp.url,
		       rf.ruling_id IS NOT NULL
		FROM ruling r
		LEFT JOIN court c ON c.name_ar = r.court
		LEFT JOIN source_page sp ON sp.doc_type = 'ruling' AND sp.centre_id = r.id
		LEFT JOIN ruling_file rf ON rf.ruling_id = r.id
		ORDER BY r.id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	rCols := []string{"id", "court", "court_en", "number", "year", "decided_on", "title", "president", "members", "text_ar", "text_norm", "source_url", "pdf_url"}
	searchCols := []string{"ruling_id", "text_norm"}
	bR := newBatcher(ch, "ruling", strings.Join(rCols, ","))
	bSearch := newBatcher(ch, "ruling_search", strings.Join(searchCols, ","))
	n := 0
	for rows.Next() {
		var id int
		var court, courtEn, number, president, textAr, sourceURL *string
		var year *int
		var decidedOn *time.Time
		var title string
		var members []string
		var hasFile bool
		if err := rows.Scan(&id, &court, &courtEn, &number, &year, &decidedOn, &title, &president, &members, &textAr, &sourceURL, &hasFile); err != nil {
			return n, err
		}
		var pdfURL *string
		if hasFile {
			u := fmt.Sprintf("https://legallaw.ul.edu.lb/RulingFile.aspx?RuliID=%d&type=list", id)
			pdfURL = &u
		}
		norm := arNorm(title + " " + derefStr(textAr))
		tuple := "(" + strings.Join([]string{
			sqlInt(id), sqlNullStr(court), sqlNullStr(courtEn), sqlNullStr(number), sqlNullInt(year), sqlDate(decidedOn),
			sqlStr(title), sqlNullStr(president), sqlJSONStrings(members), sqlNullStr(textAr), sqlStr(norm), sqlNullStr(sourceURL),
			sqlNullStr(pdfURL),
		}, ",") + ")"
		searchTuple := "(" + sqlInt(id) + "," + sqlStr(norm) + ")"

		if len(tuple) > oversizeThreshold {
			var membersJSON any
			if len(members) > 0 {
				b, _ := json.Marshal(members)
				membersJSON = string(b)
			}
			if err := ov.write("ruling", rCols, []any{
				id, derefAny(court), derefAny(courtEn), derefAny(number), intAny(year), dateAny(decidedOn),
				title, derefAny(president), membersJSON, derefAny(textAr), norm, derefAny(sourceURL), derefAny(pdfURL),
			}); err != nil {
				return n, err
			}
		} else if err := bR.add(tuple); err != nil {
			return n, err
		}
		if len(searchTuple) > oversizeThreshold {
			if err := ov.write("ruling_search", searchCols, []any{id, norm}); err != nil {
				return n, err
			}
		} else if err := bSearch.add(searchTuple); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	if err := bR.flush(); err != nil {
		return n, err
	}
	return n, bSearch.flush()
}

// The ruling export includes every ruling; the join keeps pages within that set.
const rulingPagesQuery = `
 SELECT p.ruling_id, p.n, p.text_ar, p.low_confidence
 FROM ruling_page p JOIN ruling r ON r.id = p.ruling_id
 ORDER BY p.ruling_id, p.n`

func exportRulingPages(ctx context.Context, pool *pgxpool.Pool, ch *chunker, ov *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, rulingPagesQuery)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return exportRulingPageRows(rows, ch, ov)
}

// Consume one page at a time, including pages too large for SQL literals.
func exportRulingPageRows(rows pgx.Rows, ch *chunker, ov *oversizeWriter) (int, error) {
	cols := []string{"ruling_id", "n", "text_ar", "low_confidence"}
	b := newBatcher(ch, "ruling_page", strings.Join(cols, ","))
	count := 0
	for rows.Next() {
		var rulingID, n int
		var text string
		var low bool
		if err := rows.Scan(&rulingID, &n, &text, &low); err != nil {
			return count, err
		}
		confidence := 0
		if low {
			confidence = 1
		}
		tuple := "(" + strings.Join([]string{sqlInt(rulingID), sqlInt(n), sqlStr(text), sqlInt(confidence)}, ",") + ")"
		if len(tuple) > oversizeThreshold {
			if err := ov.write("ruling_page", cols, []any{rulingID, n, text, confidence}); err != nil {
				return count, err
			}
		} else if err := b.add(tuple); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, b.flush()
}

func exportGraphEdges(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, from_key, to_key, relation, source, status, from_type, from_id, from_article,
		       to_law_id, to_article, to_cite, part, quote
		FROM graph_edge ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "graph_edge",
		"id,from_key,to_key,relation,source,status,from_type,from_id,from_article,to_law_id,to_article,to_cite,part,quote")
	n := 0
	for rows.Next() {
		var id, fromID int
		var fromKey, relation, source, status, fromType string
		var toKey, fromArticle, toArticle, toCite, part, quote *string
		var toLawID *int
		if err := rows.Scan(&id, &fromKey, &toKey, &relation, &source, &status, &fromType, &fromID, &fromArticle,
			&toLawID, &toArticle, &toCite, &part, &quote); err != nil {
			return n, err
		}
		tuple := "(" + strings.Join([]string{
			sqlInt(id), sqlStr(fromKey), sqlNullStr(toKey), sqlStr(relation), sqlStr(source), sqlStr(status),
			sqlStr(fromType), sqlInt(fromID), sqlNullStr(fromArticle), sqlNullInt(toLawID), sqlNullStr(toArticle),
			sqlNullStr(toCite), sqlNullStr(part), sqlNullStr(quote),
		}, ",") + ")"
		if err := b.add(tuple); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

func exportPossiblyAffected(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `SELECT repealing_law_id, repealing_article, law_id, article_number FROM possibly_affected`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "possibly_affected", "repealing_law_id,repealing_article,law_id,article_number")
	n := 0
	for rows.Next() {
		var repealingLawID, lawID int
		var repealingArticle, articleNumber *string
		if err := rows.Scan(&repealingLawID, &repealingArticle, &lawID, &articleNumber); err != nil {
			return n, err
		}
		tuple := "(" + strings.Join([]string{sqlInt(repealingLawID), sqlNullStr(repealingArticle), sqlInt(lawID), sqlNullStr(articleNumber)}, ",") + ")"
		if err := b.add(tuple); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

// ---------------------------------------------------------------- SQL value encoding

func sqlStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func sqlNullStr(p *string) string {
	if p == nil {
		return "NULL"
	}
	return sqlStr(*p)
}

func sqlInt(n int) string { return strconv.Itoa(n) }

func sqlNullInt(p *int) string {
	if p == nil {
		return "NULL"
	}
	return strconv.Itoa(*p)
}

func sqlDate(t *time.Time) string {
	if t == nil {
		return "NULL"
	}
	return sqlStr(t.Format("2006-01-02"))
}

func sqlJSONStrings(a []string) string {
	if len(a) == 0 {
		return "NULL"
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "NULL"
	}
	return sqlStr(string(b))
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefAny, dateAny and intAny turn a nullable Go value into the raw value
// oversize.go's rows carry (nil for NULL, no SQL escaping: they are bound
// as query parameters, not inlined as SQL text).
func derefAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func dateAny(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

func intAny(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// scanReadings writes a span the two machine readings of a Gazette scan
// disagree on (U+E000 one U+E001 two U+E002, see internal/build/scans.go)
// as ⟦one | two⟧, which an assistant reading the MCP can see and explain.
var scanReadings = strings.NewReplacer("\uE000", "⟦", "\uE001", " | ", "\uE002", "⟧").Replace

// exportSections writes every text's chapters, so get_law can return its
// table of contents and say which chapter each article sits under.
func exportSections(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, law_id, parent_id, position, depth, heading, note
		FROM legislation_section ORDER BY law_id, position`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "legislation_section", "id,law_id,parent_id,position,depth,heading,note")
	n := 0
	for rows.Next() {
		var id, lawID, position, depth int
		var parentID *int
		var heading string
		var note *string
		if err := rows.Scan(&id, &lawID, &parentID, &position, &depth, &heading, &note); err != nil {
			return n, err
		}
		tuple := "(" + strings.Join([]string{sqlInt(id), sqlInt(lawID), sqlNullInt(parentID), sqlInt(position), sqlInt(depth),
			sqlStr(heading), sqlNullStr(note)}, ",") + ")"
		if err := b.add(tuple); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

// exportCentreErrors writes the notes on errors in the Centre's own text.
func exportCentreErrors(ctx context.Context, pool *pgxpool.Pool, ch *chunker, _ *oversizeWriter) (int, error) {
	rows, err := pool.Query(ctx, `SELECT law_id, number, note_en, note_ar FROM centre_error ORDER BY law_id, number`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	b := newBatcher(ch, "centre_error", "law_id,number,note_en,note_ar")
	n := 0
	for rows.Next() {
		var lawID int
		var number, en, ar string
		if err := rows.Scan(&lawID, &number, &en, &ar); err != nil {
			return n, err
		}
		if err := b.add("(" + strings.Join([]string{sqlInt(lawID), sqlStr(number), sqlStr(en), sqlStr(ar)}, ",") + ")"); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	return n, b.flush()
}

// Whole-law repeal follows site Law.sortChanges, including unknown/future dates.
const articlesQuery = `
		SELECT a.law_id, a.position, a.number, a.note, a.text_ar, a.text_fr, a.in_force_from, a.section_id,
		       f.text_ar, coalesce(f.version, '1.0'), coalesce(f.state, 'in_force'), coalesce(f.pending, false), f.valid_from,
 (l.status <> 'in_force'
 OR EXISTS (SELECT 1 FROM amendment x WHERE x.law_id=a.law_id
   AND x.article_number IS NULL AND x.action='repeal' AND x.part IS NULL AND x.status='applied')
 OR EXISTS (SELECT 1 FROM council_ruling c WHERE c.law_id=a.law_id AND (c.number='' OR c.number=a.number) AND c.effect IN ('annulled','not_in_force')))
		FROM legislation_article a JOIN legislation l ON l.id=a.law_id
		LEFT JOIN article_in_force f ON f.law_id = a.law_id AND f.number = a.number AND f.position = a.position
		ORDER BY a.law_id, a.position`
