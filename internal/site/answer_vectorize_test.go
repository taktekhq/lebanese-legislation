package site

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taktekhq/lebanese-legislation/internal/semantic"
	"github.com/taktekhq/lebanese-legislation/internal/vectorize"
)

type answerEmbedder struct{}

func (answerEmbedder) Embed(_ context.Context, _ []string, _ bool) ([][]float32, error) {
	v := make([]float32, 768)
	v[0] = 1
	return [][]float32{v}, nil
}

type answerVectorizeTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (tr answerVectorizeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = tr.target.Scheme, tr.target.Host
	return tr.base.RoundTrip(r)
}

func TestAnswerWithVectorizeWithoutEmbeddingColumn(t *testing.T) {
	dsn := os.Getenv("LEBLEG_TEST_DSN")
	if dsn == "" {
		t.Skip("set LEBLEG_TEST_DSN to a disposable Postgres database")
	}
	admin, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("site_answer_vectorize_%d", time.Now().UnixNano())
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
	// Neither pgvector nor an embedding column exists in this serving database.
	body := "هذه عبارة تجريبية عن المكتبة العامة."
	_, err = db.Exec(t.Context(), `CREATE TABLE ruling(id int,title text,text_ar text);
 CREATE TABLE public.semantic_chunks(model text,kind text,source_id int,part text,lang text,title text,text text,hash text,chunk_start int,chunk_end int);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(t.Context(), `INSERT INTO ruling VALUES(7,'fixture ruling',$1)`, body); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(t.Context(), `INSERT INTO semantic_chunks VALUES($1,'ruling',7,'','ar','fixture ruling',$2,$3,0,$4)`, semantic.DefaultModel, body, semantic.Hash(body), len([]rune(body))); err != nil {
		t.Fatal(err)
	}
	id := vectorize.ChunkID(semantic.DefaultModel, "ruling", 7, "", "ar", semantic.Hash(body), 0)
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"matches": []vectorize.Match{{ID: id, Score: .95}}}})
	}))
	defer remote.Close()
	target, _ := url.Parse(remote.URL)
	original := http.DefaultTransport
	http.DefaultTransport = answerVectorizeTransport{target, original}
	defer func() { http.DefaultTransport = original }()
	client, err := vectorize.New("account", "index", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	store := semantic.NewStore(db, answerEmbedder{}).WithVectorize(client)
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.db = db
	f := &evidenceFake{}
	s.SetMeaningSearch(f, "Google Gemini", false, 100)
	s.SetVectorSearch(store)
	q := "How can I use a public library?"
	// Isolate semantic retrieval from lexical SQL: the ordinary search found
	// nothing. The answer must come entirely from the remote match's fixture row.
	s.scache.put(q, Results{Q: q}, "")
	res, err := s.SearchMeaning(t.Context(), q, s.meaning)
	if err != nil {
		t.Fatalf("SearchMeaning: %v", err)
	}
	sources, err := s.answerSources(t.Context(), q, res)
	if err != nil || len(sources) != 0 {
		t.Fatalf("answerSources: %v %+v", err, sources)
	}
	sources, err = s.vectorSources(t.Context(), q)
	if err != nil || len(sources) != 1 {
		t.Fatalf("vectorSources -> Store.Search: %v %+v", err, sources)
	}
	var logs bytes.Buffer
	output := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(output)
	w := answerRequest(t, s, q, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-evidence-quote") || !strings.Contains(w.Body.String(), "/ruling/7/") {
		t.Fatalf("answer: status=%d log=%s body=%s", w.Code, logs.String(), w.Body.String())
	}
	if calls != 1 || len(f.received) != 1 || f.received[0].Text != body {
		t.Fatalf("remote evidence not used: calls=%d passages=%+v", calls, f.received)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful answer logged unavailable: %s", logs.String())
	}
}

func TestAnswerUnavailableLogsStepWithoutQuestion(t *testing.T) {
	for _, endpoint := range []string{"/en/search/answer/", "/en/search/ai/", "/en/search/ai/deep/"} {
		for _, step := range []string{"meaning search", "SearchMeaning", "limiter"} {
			if step == "limiter" && endpoint != "/en/search/answer/" {
				continue
			}
			t.Run(endpoint+step, func(t *testing.T) {
				s, err := New(nil)
				if err != nil {
					t.Fatal(err)
				}
				q := "How can I use a private fixture question?"
				if step != "meaning search" {
					s.SetMeaningSearch(&fakeRewrite{err: errors.New("provider offline")}, "Google Gemini", false, 100)
					s.scache.put(q, Results{Q: q}, "")
					if step == "limiter" {
						s.meaning.clients = newIPLimiter(1, 1)
						s.meaning.clients.Allow("192.0.2.1")
					}
				}
				var logs bytes.Buffer
				output := log.Writer()
				log.SetOutput(&logs)
				defer log.SetOutput(output)
				r := httptest.NewRequest("POST", endpoint, strings.NewReader(url.Values{"q": {q}}.Encode()))
				r.RemoteAddr = "192.0.2.1:1234"
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if !strings.Contains(logs.String(), "answer unavailable: "+step+": ") || strings.Contains(logs.String(), q) {
					t.Fatalf("unavailable step missing or question leaked: %s", logs.String())
				}
				if w.Code != 200 {
					t.Fatalf("response changed: %d", w.Code)
				}
			})
		}
	}
}
