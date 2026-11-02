package semantic

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run against a disposable cluster; the test creates and removes two databases.
func TestBuildTextWithoutEmbeddings(t *testing.T) {
	dsn := os.Getenv("TEXT_BUILD_TEST_DSN")
	if dsn == "" {
		t.Skip("TEXT_BUILD_TEST_DSN needed for database integration")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	pools := make([]*pgxpool.Pool, 0, 2)
	for _, suffix := range []string{"corpus", "index"} {
		name := fmt.Sprintf("text_build_%d_%s", os.Getpid(), suffix)
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
				t.Error(err)
			}
		}()
		cfg := admin.Config().Copy()
		cfg.ConnConfig.Database = name
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		pools = append(pools, pool)
		defer pool.Close()
	}
	corpus, index := pools[0], pools[1]
	_, err = corpus.Exec(ctx, `CREATE TABLE legislation(id int,title text,status text,issued_on date,preamble text,explanatory_note text,closing text);
 CREATE TABLE legislation_article(law_id int,position int,text_ar text,text_fr text,number text);
 CREATE TABLE article_in_force(law_id int,number text,position int,state text,pending bool,text_ar text,valid_from date);
 CREATE TABLE legislation_title_en(law_id int,title_en text);
 CREATE TABLE ruling(id int,title text,text_ar text);
 CREATE TABLE ruling_page(ruling_id int,n int,text_ar text);
 CREATE TABLE opinion(id int,subject text,text_ar text);
 CREATE TABLE agreement(id int,title text,text_ar text);
 CREATE TABLE doctrine_article(id int,title text,abstract text);
 CREATE TABLE research(id int,title text,text_ar text);
 INSERT INTO legislation VALUES(1,'عنوان','in_force',NULL,NULL,NULL,NULL);
 INSERT INTO article_in_force VALUES(1,'1',1,'in_force',false,'ألف باء',NULL);
 INSERT INTO legislation_article VALUES(1,1,'ألف باء','Texte français','1');`)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := BuildText(ctx, corpus, index, DefaultModel)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Documents != 3 || stats.Chunks != 3 || stats.Embedded != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	var text, hash string
	if err = index.QueryRow(ctx, `SELECT text,hash FROM semantic_chunks WHERE kind='article' AND lang='ar'`).Scan(&text, &hash); err != nil {
		t.Fatal(err)
	}
	if text != "ألف باء" || hash != Hash(text) {
		t.Fatalf("changed source text or hash: %q %s", text, hash)
	}
	var embeddings int
	if err = index.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='semantic_chunks' AND column_name='embedding'`).Scan(&embeddings); err != nil {
		t.Fatal(err)
	}
	if embeddings != 0 {
		t.Fatal("text database has an embedding column")
	}
	// A rebuild prunes removed source text without leaving duplicate documents.
	if _, err = corpus.Exec(ctx, `UPDATE legislation_article SET text_fr=''`); err != nil {
		t.Fatal(err)
	}
	stats, err = BuildText(ctx, corpus, index, DefaultModel)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Documents != 2 || stats.Chunks != 2 {
		t.Fatalf("stale chunk retained: %+v", stats)
	}
	// A failed corpus read must roll back the initial truncate.
	if _, err = corpus.Exec(ctx, `DROP TABLE ruling`); err != nil {
		t.Fatal(err)
	}
	if _, err = BuildText(ctx, corpus, index, DefaultModel); err == nil {
		t.Fatal("broken corpus accepted")
	}
	var count int
	if err = index.QueryRow(ctx, `SELECT count(*) FROM semantic_chunks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("failed rebuild lost prior rows: %d", count)
	}
}
