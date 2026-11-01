package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func gemini(t *testing.T, h http.HandlerFunc, calls int) *Gemini {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g, err := NewGemini("secret-key", Config{MaxCallsPerDay: intp(calls), Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	g.Endpoint, g.Backoff = srv.URL, time.Millisecond
	return g
}

func vector(x float64) []float64 {
	v := make([]float64, Dimensions)
	v[0], v[1] = x, x
	return v
}

func TestEmbedSendsDocumentTaskAndNormalizes(t *testing.T) {
	g := gemini(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "secret-key" || !strings.HasSuffix(r.URL.Path, "/models/gemini-embedding-001:batchEmbedContents") {
			http.Error(w, "bad", 400)
			return
		}
		var body struct {
			Requests []struct {
				TaskType string `json:"taskType"`
				Dims     int    `json:"outputDimensionality"`
			} `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var out []any
		for _, q := range body.Requests {
			if q.TaskType != "RETRIEVAL_DOCUMENT" || q.Dims != 768 {
				http.Error(w, "bad", 400)
				return
			}
			out = append(out, map[string]any{"values": vector(3)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}, 10)
	got, err := g.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0][0] < 0.7071 || got[0][0] > 0.7072 {
		t.Fatalf("%v", got[0][:2])
	}
	for _, bad := range [][]string{nil, {"  "}, make([]string, 101)} {
		if _, err := g.Embed(context.Background(), bad); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestInvalidVectorsAreRefused(t *testing.T) {
	for _, v := range [][]float64{make([]float64, Dimensions), vector(1)[:10]} {
		if _, err := Normalize(v); err == nil {
			t.Fatal("accepted")
		}
	}
}

func TestErrorsNeverCarryKeyAndRetryOnlyTransient(t *testing.T) {
	var n atomic.Int64
	g := gemini(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			http.Error(w, "slow down secret-key", 429)
			return
		}
		http.Error(w, "bad request secret-key", 400)
	}, 10)
	_, err := g.Embed(context.Background(), []string{"a"})
	if err == nil || strings.Contains(err.Error(), "secret-key") || !errors.Is(err, ErrProvider) {
		t.Fatalf("got %v", err)
	}
	if n.Load() != 2 {
		t.Fatalf("%d attempts: a 429 is retried once, a 400 never", n.Load())
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	g := gemini(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/steal", http.StatusFound)
	}, 10)
	if _, err := g.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("redirect followed")
	}
}

func TestCallLimitBlocksBeforeHTTP(t *testing.T) {
	var n atomic.Int64
	g := gemini(t, func(w http.ResponseWriter, r *http.Request) { n.Add(1) }, 0)
	if _, err := g.CountTokens(context.Background(), []string{"a"}); err == nil || n.Load() != 0 {
		t.Fatalf("got %v after %d requests", err, n.Load())
	}
	if _, err := NewGemini("k", Config{MaxCallsPerDay: intp(-1)}); err == nil {
		t.Fatal("negative limit accepted")
	}
	t.Setenv("GEMINI_CONCURRENCY", "x")
	if _, err := NewGemini("k", Config{}); err == nil {
		t.Fatal("bad GEMINI_CONCURRENCY accepted")
	}
	if _, err := NewGemini("bad\nkey", Config{}); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestCountTokensGroupsPartsAndValidatesTotal(t *testing.T) {
	total := `{"totalTokens":12}`
	g := gemini(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Contents []struct {
				Parts []map[string]string `json:"parts"`
			} `json:"contents"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 2 {
			http.Error(w, "bad", 400)
			return
		}
		_, _ = w.Write([]byte(total))
	}, 10)
	if n, err := g.CountTokens(context.Background(), []string{"a", "b"}); err != nil || n != 12 {
		t.Fatalf("%d %v", n, err)
	}
	for _, bad := range []string{`{"totalTokens":0}`, `{"totalTokens":1.5}`, `{}`} {
		total = bad
		if _, err := g.CountTokens(context.Background(), []string{"a", "b"}); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

// fake counts every text as 10 tokens.
type fake struct {
	embeds atomic.Int64
	fail   bool
}

func (f *fake) CountTokens(ctx context.Context, texts []string) (int64, error) {
	return int64(10 * len(texts)), nil
}
func (f *fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.embeds.Add(1)
	if f.fail {
		return nil, errors.New("down")
	}
	return make([][]float32, len(texts)), nil
}

func TestBudgetReservesBeforeThePaidCallAndSurvivesRestart(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "budget.json")
	p := &fake{}
	b, _ := NewBudgeted(p, 100, ledger)
	if _, err := b.Embed(context.Background(), []string{"a", "b"}); err != nil { // 20 + 32 = 52
		t.Fatal(err)
	}
	b2, _ := NewBudgeted(p, 100, ledger) // a restart
	if _, err := b2.Embed(context.Background(), []string{"a", "b"}); !errors.Is(err, ErrBudget) {
		t.Fatalf("got %v", err)
	}
	if p.embeds.Load() != 1 {
		t.Fatal("a paid call ran past the budget")
	}
	var state map[string]any
	raw, _ := os.ReadFile(ledger)
	_ = json.Unmarshal(raw, &state)
	if state["reserved_tokens"].(float64) != 52 || state["attempted_batches"].(float64) != 1 || state["overhead_per_document"].(float64) != 16 {
		t.Fatalf("ledger %s", raw)
	}
	// A changed cap or a broken ledger is refused.
	b3, _ := NewBudgeted(p, 200, ledger)
	if _, err := b3.Embed(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("got %v", err)
	}
	_ = os.WriteFile(ledger, []byte(`{"format_version":1,"model":"gemini-embedding-001","max_tokens":100,"overhead_per_document":16,"reserved_tokens":-1,"attempted_documents":0,"attempted_batches":0}`), 0o600)
	if _, err := b2.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("negative ledger accepted")
	}
}

func TestFailedAttemptStaysReserved(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "budget.json")
	b, _ := NewBudgeted(&fake{fail: true}, 100, ledger)
	_, _ = b.Embed(context.Background(), []string{"a"})
	raw, _ := os.ReadFile(ledger)
	if !strings.Contains(string(raw), `"reserved_tokens": 26`) {
		t.Fatalf("ledger %s", raw)
	}
}

func TestConcurrentReservationsCannotOverspend(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "budget.json")
	p := &fake{}
	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _ := NewBudgeted(p, 260, ledger) // ten batches of one text: 26 each
			if _, err := b.Embed(context.Background(), []string{"a"}); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 {
		t.Fatalf("%d reservations passed, want 10", ok.Load())
	}
}

func TestDailyBudgetIsSharedAndResetsInBeirut(t *testing.T) {
	dir := t.TempDir()
	p := &fake{}
	// $0.0000078 at $0.15 a million is 52 tokens: one batch of two texts.
	d, err := NewDaily(p, dir, "0.0000078", "0.15", "Asia/Beirut")
	if err != nil {
		t.Fatal(err)
	}
	if d.MaxTokens != 52 {
		t.Fatalf("cap %d", d.MaxTokens)
	}
	now := time.Date(2026, 11, 1, 21, 30, 0, 0, time.UTC) // 23:30 in Beirut
	d.Now = func() time.Time { return now }
	if _, err := d.Embed(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	other, _ := NewDaily(p, dir, "0.0000078", "0.15", "Asia/Beirut")
	other.Now = d.Now
	if _, err := other.Embed(context.Background(), []string{"a"}); !errors.Is(err, ErrBudget) {
		t.Fatalf("a second job overspent the day: %v", err)
	}
	now = now.Add(time.Hour) // past midnight in Beirut
	if _, err := other.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-11-02.json")); err != nil {
		t.Fatal("no ledger for the new day")
	}
	for _, bad := range [][2]string{{"0", "0.15"}, {"1", "-1"}, {"x", "1"}} {
		if _, err := NewDaily(p, dir, bad[0], bad[1], "Asia/Beirut"); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if _, err := NewDaily(p, dir, "1", "0.15", "Not/AZone"); err == nil {
		t.Fatal("bad zone accepted")
	}
}
