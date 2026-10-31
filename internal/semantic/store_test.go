package semantic

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type testEmbedder struct {
	calls     int
	requests  int
	fail      bool
	failAfter int
	model     string
}

func (e *testEmbedder) Model() string {
	if e.model != "" {
		return e.model
	}
	return DefaultModel
}

func (e *testEmbedder) Embed(_ context.Context, texts []string, _ bool) ([][]float32, error) {
	e.calls += len(texts)
	e.requests++
	if e.fail || (e.failAfter > 0 && e.requests >= e.failAfter) {
		return nil, fmt.Errorf("offline")
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, 768)
		out[i][0] = 1
	}
	return out, nil
}

func TestStoreSyncAndCanonicalReread(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_TEST_DSN needed for pgvector integration")
	}
	ctx := t.Context()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err = lock.Exec(ctx, `SELECT pg_advisory_lock(192837465)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock(192837465)`) }()
	schema := fmt.Sprintf("semantic_test_%d", os.Getpid())
	_, err = db.Exec(ctx, `CREATE SCHEMA `+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	corpus, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer corpus.Close()
	_, err = corpus.Exec(ctx, `CREATE TABLE legislation(id int,title text,status text,issued_on date,preamble text,explanatory_note text,closing text);
 CREATE TABLE legislation_article(law_id int,position int,text_ar text,text_fr text,number text,note text);
 CREATE TABLE article_version(law_id int,article_number text,major int,minor int,state text,text_ar text,valid_from date);
 CREATE TABLE article_in_force(law_id int,number text,position int,state text,pending bool,text_ar text,valid_from date);
 CREATE TABLE legislation_title_en(law_id int,title_en text);
 CREATE TABLE ruling(id int,title text,text_ar text);
 CREATE TABLE ruling_page(ruling_id int,n int,text_ar text);
 CREATE TABLE opinion(id int,subject text,text_ar text);
 CREATE TABLE agreement(id int,title text,text_ar text);
 CREATE TABLE doctrine_article(id int,title text,abstract text);
 CREATE TABLE research(id int,title text,text_ar text);
 INSERT INTO legislation(id,title,status) VALUES(2147483000,'عنوان','in_force');
 INSERT INTO article_in_force VALUES(2147483000,'1',1,'in_force',false,'ألف باء',NULL);
 INSERT INTO legislation_article VALUES(2147483000,1,'ألف باء','Texte français','1',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	e := &testEmbedder{}
	s := NewStore(db, e)
	if err = s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM public.semantic_chunks WHERE source_id=2147483000;DELETE FROM public.semantic_documents WHERE source_id=2147483000`)
	}()
	// The deterministic first document is the Arabic article, selected with a one-document bound.
	stats, err := s.Sync(ctx, corpus, SyncOptions{Limit: 1, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Embedded != 1 || stats.Documents != 1 {
		t.Fatalf("stats %+v", stats)
	}
	n := e.calls
	stats, err = s.Sync(ctx, corpus, SyncOptions{Limit: 1})
	if err != nil || e.calls != n || stats.Skipped != 1 {
		t.Fatalf("unchanged re-embedded %+v %v", stats, err)
	}
	requests := e.requests
	stats, err = s.Sync(ctx, corpus, SyncOptions{Limit: 3, BatchSize: 100})
	if err != nil || e.requests != requests+1 || stats.Embedded != 2 {
		t.Fatalf("cross-document batch: %+v requests=%d err=%v", stats, e.requests-requests, err)
	}

	hits, err := s.Search(ctx, "query", 4)
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits %v %v", hits, err)
	}
	h := hits[0]
	for _, candidate := range hits {
		if candidate.Kind == "article" && candidate.Lang == "ar" {
			h = candidate
			break
		}
	}
	d, err := Load(ctx, corpus, h.Document)
	if err != nil || Hash(d.Text) != h.Hash || d.Text != "ألف باء" {
		t.Fatalf("canonical %v %v", d, err)
	}
	_, err = corpus.Exec(ctx, `UPDATE article_in_force SET text_ar='جديد'; UPDATE legislation_article SET text_ar='جديد'`)
	if err != nil {
		t.Fatal(err)
	}
	d, err = Load(ctx, corpus, h.Document)
	if err != nil || Hash(d.Text) == h.Hash {
		t.Fatal("stale hash accepted")
	}
	e.fail = true
	if _, err = s.Sync(ctx, corpus, SyncOptions{Limit: 1}); err == nil {
		t.Fatal("expected embed failure")
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM public.semantic_chunks WHERE source_id=2147483000 AND hash=$1`, h.Hash).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed sync removed previous entry", err, count)
	}
	e.fail = false
	if _, err = s.Sync(ctx, corpus, SyncOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM public.semantic_chunks WHERE source_id=2147483000 AND hash=$1`, h.Hash).Scan(&count); err != nil || count != 0 {
		t.Fatal("old document generation retained")
	}
	_, err = corpus.Exec(ctx, `UPDATE article_in_force SET pending=true; INSERT INTO article_version VALUES(2147483000,'1',1,0,'in_force','جديد',NULL),(2147483000,'1',2,0,'in_force',NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err == nil {
		t.Fatal("pending article returned")
	}
	_, err = corpus.Exec(ctx, `UPDATE article_in_force SET pending=false,valid_from=CURRENT_DATE+1;UPDATE article_version SET text_ar='جديد',valid_from=CURRENT_DATE+1 WHERE major=2`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err == nil {
		t.Fatal("future article returned")
	}
	_, err = corpus.Exec(ctx, `UPDATE article_in_force SET valid_from=NULL;UPDATE article_version SET valid_from=NULL;UPDATE legislation SET status='repealed'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err == nil {
		t.Fatal("repealed article returned")
	}
	if _, err = corpus.Exec(ctx, `UPDATE legislation SET status='in_force';UPDATE legislation_article SET note='مادة ملغاة'`); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err == nil {
		t.Fatal("original repeal note ignored")
	}
	if _, err = corpus.Exec(ctx, `UPDATE legislation_article SET note=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err != nil {
		t.Fatal("eligible restored article excluded", err)
	}

	if _, err = corpus.Exec(ctx, `UPDATE legislation SET issued_on=CURRENT_DATE+1`); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, corpus, h.Document); err == nil {
		t.Fatal("future-issued law exposed")
	}
	futureStats, err := s.Sync(ctx, corpus, SyncOptions{Limit: 1})
	if err != nil || futureStats.Chunks != 0 {
		t.Fatal("future-issued article indexed", futureStats, err)
	}
	if _, err = corpus.Exec(ctx, `UPDATE legislation SET issued_on=NULL`); err != nil {
		t.Fatal(err)
	}

	// An interruption after the first committed batch reuses that vector on resume.
	partial := Document{Kind: "article", ID: 2147483000, Part: "partial", Lang: "ar", Text: strings.Repeat("ن", 2000)}
	e.failAfter = e.requests + 2
	partialStats, partialErr := s.syncDocuments(ctx, []Document{partial}, 1, func() error { return nil })
	if partialErr == nil || partialStats.Embedded != 1 {
		t.Fatalf("partial batch persistence %+v %v", partialStats, partialErr)
	}
	e.failAfter = 0
	partialStats, partialErr = s.syncDocuments(ctx, []Document{partial}, 1, func() error { return nil })
	if partialErr != nil || partialStats.Skipped != 1 || partialStats.Embedded != 2 {
		t.Fatalf("resume %+v %v", partialStats, partialErr)
	}

	// Only a completed unlimited traversal may remove sources absent from corpus.
	if _, err = s.Sync(ctx, corpus, SyncOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	var orphanCount int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM public.semantic_chunks WHERE source_id=2147483000 AND part='partial'`).Scan(&orphanCount); err != nil || orphanCount != 3 {
		t.Fatal("limited run pruned unvisited chunks", orphanCount, err)
	}
	if _, err = corpus.Exec(ctx, `UPDATE article_in_force SET text_ar='changed after indexing'`); err != nil {
		t.Fatal(err)
	}
	e.fail = true
	if _, err = s.Sync(ctx, corpus, SyncOptions{}); err == nil {
		t.Fatal("expected full sync failure")
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM public.semantic_chunks WHERE source_id=2147483000 AND part='partial'`).Scan(&orphanCount); err != nil || orphanCount != 3 {
		t.Fatal("failed full run pruned unvisited chunks", orphanCount, err)
	}
	e.fail = false
	if _, err = s.Sync(ctx, corpus, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT count(*) FROM public.semantic_chunks WHERE source_id=2147483000 AND part='partial'`).Scan(&orphanCount); err != nil || orphanCount != 0 {
		t.Fatal("completed full run retained absent source", orphanCount, err)
	}

	// Grouping documents separately from API batch size fills sparse batch tails.
	_, err = corpus.Exec(ctx, `INSERT INTO legislation(id,title,status) SELECT 2147480000+n,'عنوان','in_force' FROM generate_series(1,500) n;
 INSERT INTO article_in_force(law_id,number,position,state,pending,text_ar) SELECT 2147480000+n,'1',1,'in_force',false,repeat('ن',CASE WHEN n%5=0 THEN 1000 ELSE 100 END) FROM generate_series(1,500) n`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM public.semantic_chunks WHERE source_id BETWEEN 2147480001 AND 2147480500;DELETE FROM public.semantic_documents WHERE source_id BETWEEN 2147480001 AND 2147480500`)
	}()
	for _, groupCase := range []struct{ size, calls int }{{0, 10}, {500, 6}} {
		_, err = db.Exec(ctx, `DELETE FROM public.semantic_chunks WHERE source_id BETWEEN 2147480001 AND 2147480500;DELETE FROM public.semantic_documents WHERE source_id BETWEEN 2147480001 AND 2147480500`)
		if err != nil {
			t.Fatal(err)
		}
		requests = e.requests
		grouped, err := s.Sync(ctx, corpus, SyncOptions{Limit: 500, BatchSize: 100, Workers: 1, GroupSize: groupCase.size})
		if err != nil || grouped.Embedded != 600 || e.requests-requests != groupCase.calls {
			t.Fatalf("group=%d stats=%+v calls=%d want=%d err=%v", groupCase.size, grouped, e.requests-requests, groupCase.calls, err)
		}
	}
	for _, invalidGroup := range []int{-1, 1001} {
		if _, err = s.Sync(ctx, corpus, SyncOptions{Limit: 1, GroupSize: invalidGroup}); err == nil {
			t.Fatal("invalid group accepted", invalidGroup)
		}
	}

}

func TestTopicLoadAndInvalidReferences(t *testing.T) {
	// A topic without a canonical DB must fail closed, even when embedded text exists.
	if _, err := Load(t.Context(), nil, Document{Kind: "topic", Part: "nationality", Lang: "en"}); err == nil {
		t.Fatal("unvalidated topic")
	}
	if _, err := Load(t.Context(), nil, Document{Kind: "topic", Part: "missing", Lang: "en"}); err == nil {
		t.Fatal("unknown topic")
	}
	if _, err := Load(t.Context(), nil, Document{Kind: "DROP TABLE"}); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestIndexRetryRecoversTransientEmbeddingFailure(t *testing.T) {
	e := &transientEmbedder{}
	values, err := embedForIndex(t.Context(), e, []string{"source"}, func() error { return nil })
	if err != nil || len(values) != 1 || e.attempts != 2 {
		t.Fatalf("retry attempts=%d values=%d err=%v", e.attempts, len(values), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e = &transientEmbedder{}
	if _, err = embedForIndex(ctx, e, []string{"source"}, func() error { return nil }); err == nil {
		t.Fatal("cancelled retry")
	}
}

type transientEmbedder struct{ attempts int }

func (e *transientEmbedder) Embed(_ context.Context, texts []string, _ bool) ([][]float32, error) {
	e.attempts++
	if e.attempts == 1 {
		return nil, fmt.Errorf("transient")
	}
	return [][]float32{make([]float32, 768)}, nil
}

func TestBoundedArticleLookupMatchesCanonicalView(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_CORPUS_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_CORPUS_TEST_DSN needed for real-corpus equivalence")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(t.Context(), `SELECT f.law_id,f.number,f.text_ar,CASE WHEN f.text_ar=a.text_ar THEN coalesce(a.text_fr,'') ELSE '' END FROM article_in_force f JOIN legislation l ON l.id=f.law_id LEFT JOIN legislation_article a ON a.law_id=f.law_id AND a.position=f.position WHERE f.state='in_force' AND NOT f.pending AND l.status<>'repealed' AND f.text_ar IS NOT NULL AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE) ORDER BY f.amendment_id NULLS LAST,f.law_id,f.position LIMIT 40`)
	if err != nil {
		t.Fatal(err)
	}
	type source struct {
		id           int
		part, ar, fr string
	}
	var sources []source
	for rows.Next() {
		var v source
		if err = rows.Scan(&v.id, &v.part, &v.ar, &v.fr); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for _, v := range sources {
		for _, lang := range []string{"ar", "fr"} {
			want := v.ar
			if lang == "fr" {
				want = v.fr
			}
			d, err := Load(t.Context(), db, Document{Kind: "article", ID: v.id, Part: v.part, Lang: lang})
			if want == "" {
				if err == nil {
					t.Fatal("empty text returned")
				}
				continue
			}
			if err != nil || d.Text != want {
				t.Fatalf("%d %s %s changed canonical wording: %v", v.id, v.part, lang, err)
			}
		}
	}
	t.Logf("bounded canonical rereads: %d sources, %s", len(sources), time.Since(started))
}

func TestSearchReturnsRequestedMinorityModelHits(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_TEST_DSN needed for pgvector integration")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := &testEmbedder{model: "semantic-minority-test"}
	s := NewStore(db, e)
	if err = s.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(context.Background(), `DELETE FROM public.semantic_chunks WHERE model IN ('semantic-minority-test','semantic-old-test')`)
	}()
	v := make([]float32, 768)
	v[0] = 1
	_, err = db.Exec(t.Context(), `INSERT INTO public.semantic_chunks(model,kind,source_id,part,lang,title,text,hash,chunk_start,chunk_end,embedding) SELECT model,'article',n,'','ar','title','body','hash',0,4,$1::halfvec FROM (SELECT 'semantic-old-test' AS model,generate_series(1,950) AS n UNION ALL SELECT 'semantic-minority-test',generate_series(1,70)) s`, vectorText(v))
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(t.Context(), "query", 48)
	if err != nil || len(hits) != 48 {
		t.Fatalf("minority model lost available hits: count=%d err=%v", len(hits), err)
	}
}

func TestBulkStorageLeavesExistingANNIntact(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_TEST_DSN needed for pgvector integration")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewStore(db, &testEmbedder{})
	if err = s.EnsureSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var before, after uint32
	if err = db.QueryRow(t.Context(), `SELECT 'public.semantic_chunks_embedding'::regclass::oid`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureStorage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.BuildIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(t.Context(), `SELECT 'public.semantic_chunks_embedding'::regclass::oid`).Scan(&after); err != nil || before != after {
		t.Fatal("bulk setup replaced an existing serving ANN index", before, after, err)
	}
}

func TestHalfPrecisionCosineTracksFloat32(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_TEST_DSN needed for pgvector integration")
	}
	db, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewStore(db, &testEmbedder{})
	if err = s.EnsureStorage(t.Context()); err != nil {
		t.Fatal(err)
	}
	var storage string
	if err = db.QueryRow(t.Context(), `SELECT format_type(atttypid,atttypmod) FROM pg_attribute WHERE attrelid='public.semantic_chunks'::regclass AND attname='embedding'`).Scan(&storage); err != nil || storage != "halfvec(768)" {
		t.Fatalf("storage=%s err=%v", storage, err)
	}
	a := make([]float32, 768)
	for i := range a {
		a[i] = float32(math.Sin(float64(i)*.73) + .23*math.Cos(float64(i)*.17))
	}
	a, err = normalize(a)
	if err != nil {
		t.Fatal(err)
	}
	maxDelta := 0.0
	for _, shift := range []float64{.001, .2, 2.1} {
		b := make([]float32, 768)
		for i := range b {
			b[i] = float32(math.Sin(float64(i)*.73+shift) + .23*math.Cos(float64(i)*.17-shift))
		}
		b, err = normalize(b)
		if err != nil {
			t.Fatal(err)
		}
		var delta float64
		if err = db.QueryRow(t.Context(), `SELECT ($1::vector <=> $2::vector)-($1::halfvec <=> $2::halfvec)`, vectorText(a), vectorText(b)).Scan(&delta); err != nil || math.Abs(delta) > .001 {
			t.Fatalf("cosine quantization delta=%g err=%v", delta, err)
		}
		maxDelta = max(maxDelta, math.Abs(delta))
	}
	t.Logf("maximum cosine-distance quantization delta: %.8g", maxDelta)
}

func TestStorageRefusesLegacyVectorColumn(t *testing.T) {
	dsn := os.Getenv("SEMANTIC_TEST_DSN")
	if dsn == "" {
		t.Skip("SEMANTIC_TEST_DSN needed for pgvector integration")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("semantic_legacy_%d", time.Now().UnixNano())
	if _, err = admin.Exec(t.Context(), `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), `DROP DATABASE `+name) }()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	legacy, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	s := NewStore(legacy, &testEmbedder{})
	if err = s.EnsureStorage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(t.Context(), `ALTER TABLE public.semantic_chunks ALTER COLUMN embedding TYPE vector(768) USING embedding::vector(768)`); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureStorage(t.Context()); err == nil || !strings.Contains(err.Error(), "halfvec(768)") || !strings.Contains(err.Error(), "offline") {
		t.Fatal("legacy vector schema accepted without migration guidance", err)
	}
}
