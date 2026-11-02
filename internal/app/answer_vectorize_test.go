package app

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taktekhq/lebanese-legislation/internal/site"
)

func TestVectorizeDSNStillEnablesAutomaticGemini(t *testing.T) {
	t.Setenv("LEBLEG_SEARCH_PROVIDER", "")
	t.Setenv("LEBLEG_VECTOR_DSN", "postgres://test:test@127.0.0.1:1/test")
	t.Setenv("LEBLEG_VECTORIZE_INDEX", "index")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account")
	t.Setenv("LEBLEG_VECTORIZE_TOKEN", "test-token")
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_SEARCH_MODEL", "")
	t.Setenv("GEMINI_MAX_CALLS_PER_DAY", "100")
	t.Setenv("GEMINI_CONCURRENCY", "2")
	t.Setenv("GEMINI_QUERY_CACHE_SIZE", "256")
	t.Setenv("GEMINI_QUERY_CACHE_TTL", "3600")
	s, err := site.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	closeSearch, err := enableSearch(t.Context(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSearch()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/en/search/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Google Gemini") {
		t.Fatal("answer startup: Vectorize retrieval was enabled but Gemini meaning search is off")
	}
}
