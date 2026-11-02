package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

type vectorizeTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (tr vectorizeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = tr.target.Scheme, tr.target.Host
	return tr.base.RoundTrip(r)
}

// A separate database prevents schema changes from touching another test's index.
func TestVectorizeWithoutEmbeddingColumn(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("answer_vectorize_%d", time.Now().UnixNano())
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE "+name) }()
	cfg := admin.Config().Copy()
	cfg.ConnConfig.Database = name
	db, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(t.Context(), `CREATE TABLE public.semantic_chunks (
 model text,kind text,source_id int,part text,lang text,title text,text text,hash text,chunk_start int,chunk_end int,
 PRIMARY KEY(model,kind,source_id,part,lang,hash,chunk_start));
 INSERT INTO public.semantic_chunks VALUES ('gemini-embedding-001','ruling',7,'','ar','title','fixture passage','fixture-hash',0,15);`)
	if err != nil {
		t.Fatal(err)
	}
	id := vectorize.ChunkID(DefaultModel, "ruling", 7, "", "ar", "fixture-hash", 0)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client/v4/accounts/account/vectorize/v2/indexes/index/query" {
			t.Errorf("unexpected Vectorize path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"matches": []vectorize.Match{{ID: id, Score: .9}}}})
	}))
	defer remote.Close()
	target, _ := url.Parse(remote.URL)
	original := http.DefaultTransport
	http.DefaultTransport = vectorizeTransport{target, original}
	defer func() { http.DefaultTransport = original }()
	client, err := vectorize.New("account", "index", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, &testEmbedder{}).WithVectorize(client)
	for _, step := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"EnsureStorage", store.EnsureStorage}, {"BuildIndex", store.BuildIndex}, {"EnsureSchema", store.EnsureSchema},
	} {
		t.Run(step.name, func(t *testing.T) {
			if err := step.run(t.Context()); err != nil {
				t.Fatalf("answer startup %s: %v", step.name, err)
			}
		})
	}
	t.Run("Search", func(t *testing.T) {
		hits, err := store.Search(t.Context(), "fixture question", 4)
		if err != nil {
			t.Fatalf("answer vectorSources -> Store.Search: %v", err)
		}
		if len(hits) != 1 || hits[0].ID != 7 || hits[0].Text != "fixture passage" {
			t.Fatalf("matches not hydrated: %+v", hits)
		}
	})
	var count int
	if err = db.QueryRow(t.Context(), `SELECT count(*) FROM pg_attribute WHERE attrelid='public.semantic_chunks'::regclass AND attname='embedding' AND NOT attisdropped`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("embedding column restored: count=%d err=%v", count, err)
	}
}
