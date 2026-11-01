package citecheck

// The loader runs against a real Postgres: LEBLEG_TEST_PG is the DSN of a
// throwaway server (it creates and drops lebleg_test_citecheck on it).
//   LEBLEG_TEST_PG='host=127.0.0.1 port=55611 user=postgres password=t sslmode=disable' go test ./internal/citecheck
// The database is built from the fixture archive of internal/provenance
// (laws 8225, 9307, 326577, 327389; 9307 and 327389 amend the other two).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/archive"
	"github.com/taktekhq/lebanese-legislation/internal/build"
)

const testDB = "lebleg_test_citecheck"

func fixtureDB(t *testing.T) (dsn string, conn *pgx.Conn) {
	t.Helper()
	admin := os.Getenv("LEBLEG_TEST_PG")
	if admin == "" {
		t.Skip("LEBLEG_TEST_PG runs the database tests (a throwaway Postgres)")
	}
	ctx := context.Background()
	a, err := pgx.Connect(ctx, admin+" dbname=postgres")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	a.Exec(ctx, "DROP DATABASE IF EXISTS "+testDB)
	if _, err := a.Exec(ctx, "CREATE DATABASE "+testDB); err != nil {
		t.Fatal(err)
	}
	dsn = admin + " dbname=" + testDB
	_, err = build.Run(ctx, build.Options{Archive: archive.Archive{Dir: "../provenance/testdata/archive"}, DSN: dsn,
		Scans: "../provenance/testdata/bundle", Citations: "../provenance/testdata/none.jsonl",
		RulingCitations: "../provenance/testdata/none.jsonl", Log: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conn, err = pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	return dsn, conn
}

func sum(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

func TestLoadAgainstDatabase(t *testing.T) {
	dsn, conn := fixtureDB(t)
	ctx := context.Background()
	// Which change the fixture holds, and the two bodies it joins.
	var byLaw, law int
	var byArt, art, part string
	var seq int
	var amending, centre string
	err := conn.QueryRow(ctx, `SELECT x.by_law_id, x.by_article, x.seq, x.law_id, x.article_number, coalesce(x.part, ''), ba.text_ar, ta.text_ar
		FROM amendment x
		JOIN legislation_article ba ON ba.law_id = x.by_law_id AND ba.number = x.by_article
		JOIN legislation_article ta ON ta.law_id = x.law_id AND ta.number = x.article_number
		WHERE x.law_id IS NOT NULL ORDER BY x.by_law_id, x.by_article, x.seq LIMIT 1`).
		Scan(&byLaw, &byArt, &seq, &law, &art, &part, &amending, &centre)
	if err != nil {
		t.Fatalf("fixture has no amendment: %v", err)
	}
	key := strings.Join([]string{itoa(byLaw), byArt, itoa(seq), art}, "/")
	good := sum(amending, centre, art, part) // set apart from judge.InputHash: same layout, computed here
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	now := time.Now().UTC().Format(time.RFC3339)
	judgements := write("j.jsonl", `{"key":"`+key+`","input":"deadbeefdeadbeef","task":"state","prompt":"state-1","state":"includes","reason":"x","at":"`+now+`"}`+"\n")
	citations := write("c.jsonl", "")
	log := write("judge.log", "read 2, failed 0\nread 5, failed 2\n")
	ids := write("new.txt", itoa(byLaw)+"\n")
	in, err := Load(ctx, Options{DSN: dsn, NewIDs: ids, Citations: citations, Judgements: judgements, Logs: []string{log}, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Recomputed[key]; got != good {
		t.Errorf("recomputed hash = %q, want %q", got, good)
	}
	if len(in.New) == 0 {
		t.Fatal("the new text's amending articles were not found")
	}
	for _, a := range in.New {
		if !strings.HasPrefix(a.Key, itoa(byLaw)+"/") {
			t.Errorf("article %q is not from the new text %d", a.Key, byLaw)
		}
	}
	r := Check(in)
	var missing, mismatch, failed bool
	for _, v := range r.Violations {
		missing = missing || strings.Contains(v, "no citation reading")
		mismatch = mismatch || strings.Contains(v, "judgement "+key)
		failed = failed || strings.Contains(v, "failed 2")
	}
	if !missing || !mismatch || !failed {
		t.Errorf("want missing reading, hash mismatch and failed 2; got %q", r.Violations)
	}
	// The same judgement with the right hash passes condition 3.
	judgements = write("j2.jsonl", `{"key":"`+key+`","input":"`+good+`","task":"state","prompt":"state-1","state":"includes","reason":"x","at":"`+now+`"}`+"\n")
	in, err = Load(ctx, Options{DSN: dsn, NewIDs: ids, Citations: citations, Judgements: judgements, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range Check(in).Violations {
		if strings.Contains(v, "judgement ") {
			t.Errorf("right hash still fails: %q", v)
		}
	}
	// -prev: a file without the amending law makes it new; one with it, not.
	prev := write("prev.txt", "1\n")
	in, err = Load(ctx, Options{DSN: dsn, PrevIDs: prev, Citations: citations, Judgements: judgements, Now: time.Now().UTC()})
	if err != nil || len(in.New) == 0 || len(in.Current) < 4 {
		t.Errorf("prev file: new=%d current=%d err=%v", len(in.New), len(in.Current), err)
	}
	in, err = Load(ctx, Options{DSN: dsn, PrevIDs: filepath.Join(dir, "none.txt"), Citations: citations, Judgements: judgements, Now: time.Now().UTC()})
	if err != nil || len(in.New) != 0 || len(in.Current) < 4 {
		t.Errorf("first night: new=%d current=%d err=%v", len(in.New), len(in.Current), err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
