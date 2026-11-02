package site

// The read-only JSON API, /api/v1/. Five endpoints, each backed by the same
// Postgres queries the HTML pages use (see handlers.go and docs.go): search,
// a law's metadata and table of contents, one article's verbatim text, a
// ruling, and a gazette issue. Nothing here paraphrases or rewrites a legal
// text; every text field is copied from the database as the Centre
// published it. Every response carries the site's own canonical URL and,
// where known, the Centre's source page.
//
// Responses are cacheable (Cache-Control, no per-visitor data) so Cloudflare
// and the MCP Worker in front of it can serve repeat requests without
// hitting Postgres. Usage is counted the same anonymous way as page views
// (events.go): no IP, no user agent, no cookie.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/taktekhq/lebanese-legislation/internal/calculatedcurrent"
	"github.com/taktekhq/lebanese-legislation/internal/nlsearch"
)

const apiCacheControl = "public, max-age=60, stale-while-revalidate=300"

// notLegalAdvice is the line every API response and MCP tool result outside
// the HTML pages carries; the HTML pages already show the Arabic, French and
// English notice on every page (templates/layout.html).
const notLegalAdvice = "Not legal advice. Official texts from the Lebanese University's Centre for Research and Studies in Legal Informatics."

// apiSemaphoreWait mirrors mcpSemaphoreWait (mcp.go): how long a /api/v1
// request queues for a DB slot before answering 503.
const apiSemaphoreWait = 2 * time.Second

// apiLimited wraps a DB-backed /api/v1 handler with the same per-client
// token bucket and global DB semaphore built for /mcp (limiter.go): that
// file's own doc says they're "safe to wire onto /api/v1 too", but until
// now nothing did, leaving this public, unauthenticated JSON API free to
// open as many concurrent Postgres queries as there were simultaneous
// requests, on a box whose web role allows only 20 connections. Without
// this, apiSearch alone (4 queries fired in parallel per request with no
// "type" filter) could exhaust the pool with a handful of concurrent
// requests and starve every other visitor.
func (s *Server) apiLimited(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if wait, ok := s.ipLim.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
			http.Error(w, "too many requests, slow down", http.StatusTooManyRequests)
			return
		}
		release, ok := s.dbSem.acquire(r.Context(), apiSemaphoreWait)
		if !ok {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "the server is busy, try again shortly", http.StatusServiceUnavailable)
			return
		}
		defer release()
		h(w, r)
	}
}

func (s *Server) apiRoutes() {
	if s.isStatic {
		return
	}
	m := s.mux
	m.HandleFunc("GET /api/v1/{$}", s.apiRoot)
	m.HandleFunc("GET /api/v1/search/{$}", s.apiLimited(s.apiSearch))
	m.HandleFunc("GET /api/v1/law/{id}/{$}", s.apiLimited(s.apiLaw))
	m.HandleFunc("GET /api/v1/law/{id}/calculated-current/{$}", s.apiLimited(s.apiCalculatedCurrent))
	m.HandleFunc("GET /api/v1/law/{id}/article/{n}/{$}", s.apiLimited(s.apiArticle))
	m.HandleFunc("GET /api/v1/ruling/{id}/{$}", s.apiLimited(s.apiRuling))
	m.HandleFunc("GET /api/v1/gazette/{id}/{$}", s.apiLimited(s.apiGazette))
	m.HandleFunc("GET /api/v1/laws/{$}", s.apiLimited(s.apiLaws))
	m.HandleFunc("GET /api/v1/gazette/{$}", s.apiLimited(s.apiGazettes))
}

type apiErrorBody struct {
	Error string `json:"error"`
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if code == 200 {
		w.Header().Set("Cache-Control", apiCacheControl)
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func (s *Server) apiNotFound(w http.ResponseWriter, what string) {
	s.writeJSON(w, http.StatusNotFound, apiErrorBody{Error: what + " not found"})
}

func (s *Server) apiFail(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("api error %s: %v", r.URL.RequestURI(), err)
	s.captureError(r, "error", err)
	s.writeJSON(w, http.StatusInternalServerError, apiErrorBody{Error: "internal error"})
}

func (s *Server) apiRoot(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, 200, map[string]any{
		"name":       "Lebanese Legislation API",
		"version":    "v1",
		"docs":       Base + "/developers/",
		"mcp":        Base + "/mcp",
		"not_advice": notLegalAdvice,
		"notes":      []string{titleEnNote, "search: when q finds nothing it is read again (English and French terms to Arabic, names, a year, a typo) and searched_instead says what was searched."},
		"endpoints": []string{
			"GET /api/v1/search/?q=&type=laws|rulings|gazette|articles&year=&limit=&offset=",
			"GET /api/v1/law/{id}/",
			"GET /api/v1/law/{id}/article/{n}/",
			"GET /api/v1/ruling/{id}/",
			"GET /api/v1/gazette/{id}/",
			"GET /api/v1/laws/?since=&until=&kind=&order=newest|oldest&limit=&offset=",
			"GET /api/v1/gazette/?since=&until=&order=newest|oldest&limit=&offset=",
		},
	})
}

// ---------------------------------------------------------------- shared shapes

// apiLawSummary is a law or decree's identity, without its text: what a
// search result or a gazette issue's contents list carries.
type apiLawSummary struct {
	ID           int     `json:"id"`
	Kind         string  `json:"kind"`
	KindEn       *string `json:"kind_en,omitempty"`
	Number       *string `json:"number,omitempty"`
	IssuedOn     *string `json:"issued_on,omitempty"`
	Title        string  `json:"title"`
	TitleEn      *string `json:"title_en,omitempty"` // unofficial English title (a model's rendering of the Arabic one); the Arabic title is official and more accurate
	Status       string  `json:"status"`
	ArticleCount int     `json:"article_count"`
	Code         *string `json:"code,omitempty"` // the curated code's short name, e.g. "Code of Obligations and Contracts"
	URL          string  `json:"url"`
	SourceURL    string  `json:"source_url,omitempty"`
	// UpdatedAt is the day the text's page last changed here (page_state):
	// an amendment in force, a correction. Never the text's own date; absent
	// when not known.
	UpdatedAt *string `json:"updated_at,omitempty"`
}

func ymd(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Format("2006-01-02")
	return &v
}

func lawSummary(l LawRow, sourceURL string) apiLawSummary {
	return apiLawSummary{ID: l.ID, Kind: l.Kind, KindEn: l.KindEn, Number: l.Number, IssuedOn: ymd(l.IssuedOn),
		Title: l.Title, Status: l.Status, ArticleCount: l.Articles, Code: l.Code,
		URL: Base + lawPath(l.ID), SourceURL: sourceURL}
}

func stripMarks(s string) string { return strings.NewReplacer("\x02", "", "\x03", "").Replace(s) }

func clampInt(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

// ---------------------------------------------------------------- search

type apiArticleHit struct {
	CalculatedCurrent *calculatedcurrent.Record `json:"calculated_current,omitempty"`
	Law               apiLawSummary             `json:"law"`
	Number            string                    `json:"article_number"`
	Snippet           string                    `json:"snippet"`
	URL               string                    `json:"url"`
	// MatchedCalculated says the query matched the calculated current
	// amount, not the published wording the snippet shows.
	MatchedCalculated bool `json:"matched_calculated_current,omitempty"`
	// Cited says the query cites this article ("المادة 86 من ..."), so it comes first.
	Cited bool `json:"cited,omitempty"`
}

type apiRulingHit struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Court     string `json:"court,omitempty"`
	DecidedOn string `json:"decided_on,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	URL       string `json:"url"`
	// PDFURL is the judgment's scan on the Lebanese University's site, for
	// every ruling that has one: give it with any answer about the ruling.
	PDFURL string `json:"pdf_url,omitempty"`
	// JudgmentPage is set when only the machine reading of the scanned
	// judgment matched, not the title or summary: the page the snippet is from.
	JudgmentPage int     `json:"judgment_page,omitempty"`
	UpdatedAt    *string `json:"updated_at,omitempty"` // the day its page last changed here, when known
}

type apiGazetteHit struct {
	ID          int     `json:"id"`
	Number      string  `json:"number,omitempty"`
	PublishedOn string  `json:"published_on,omitempty"`
	URL         string  `json:"url"`
	UpdatedAt   *string `json:"updated_at,omitempty"` // the day its page last changed here, when known
}

type apiSearchResult struct {
	Q        string          `json:"q"`
	Laws     []apiLawSummary `json:"laws,omitempty"`
	LawCount int             `json:"law_count"`
	Articles []apiArticleHit `json:"articles,omitempty"`
	ArtCount int             `json:"article_count"`
	Rulings  []apiRulingHit  `json:"rulings,omitempty"`
	RulCount int             `json:"ruling_count"`
	Gazette  []apiGazetteHit `json:"gazette,omitempty"`
	GazCount int             `json:"gazette_count"`
	// SearchedInstead is set when q found nothing and the results are for a
	// reading of it: the words searched ("قانون العمل" for "labor law").
	// SearchedInsteadKind is "translated" (Latin to Arabic) or "adjusted" (a typo
	// mended, a number or year used as a filter, a common word set aside).
	SearchedInstead     string `json:"searched_instead,omitempty"`
	SearchedInsteadKind string `json:"searched_instead_kind,omitempty"`
	// Related are passages close in meaning to q (semantic=true), copied
	// from the source texts. Semantic is "ok" or "unavailable".
	Related  []apiPassage `json:"related,omitempty"`
	Semantic string       `json:"semantic,omitempty"`
	// Selection is "ok" when a model kept only the related passages that
	// answer q (select=true), or "unavailable" when it could not.
	Selection string `json:"selection,omitempty"`
}

// apiPassage is one related passage, verbatim from its source.
type apiPassage struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	Lang       string `json:"lang,omitempty"`
	Status     string `json:"status,omitempty"`
	Unofficial bool   `json:"unofficial,omitempty"`
	URL        string `json:"url"`
}

// addRelated fills out.Related from the vector index. With choose, the
// model picks which passages answer q; it returns their IDs only, so every
// passage shown is still the source's own text.
func (s *Server) addRelated(ctx context.Context, q string, choose bool, out *apiSearchResult) {
	if s.vector == nil || s.meaning == nil {
		out.Semantic = "unavailable"
		return
	}
	sources, err := s.vectorSources(ctx, q)
	if err != nil {
		out.Semantic = "unavailable"
		return
	}
	out.Semantic = "ok"
	if choose && len(sources) > 0 {
		passages := make([]nlsearch.Passage, 0, len(sources))
		for _, src := range sources {
			passages = append(passages, src.Passage)
		}
		if quotes, err := s.meaning.selectEvidence(ctx, q, passages); err == nil {
			byID := map[string]answerSource{}
			for _, src := range sources {
				byID[src.ID] = src
			}
			chosen := make([]answerSource, 0, len(quotes))
			for _, quote := range quotes {
				if src, ok := byID[quote.SourceID]; ok {
					chosen = append(chosen, src)
					delete(byID, quote.SourceID)
				}
			}
			sources, out.Selection = chosen, "ok"
		} else {
			out.Selection = "unavailable"
		}
	}
	for _, src := range sources {
		out.Related = append(out.Related, apiPassage{ID: src.ID, Title: src.Title, Text: src.Text, Lang: src.Lang,
			Status: src.Status, Unofficial: src.Unofficial, URL: Base + src.Path})
	}
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	semanticWanted := r.URL.Query().Get("semantic") == "true"
	selectWanted := r.URL.Query().Get("select") == "true"
	ctx, cancel := s.ctx(r)
	if semanticWanted { // embedding the question and choosing passages take longer
		cancel()
		ctx, cancel = context.WithTimeout(r.Context(), 25*time.Second)
	}
	defer cancel()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	question := q
	typ := r.URL.Query().Get("type")
	year := clampInt(r.URL.Query().Get("year"), 0, 0, 2100)
	limit := clampInt(r.URL.Query().Get("limit"), 10, 1, 50)
	offset := clampInt(r.URL.Query().Get("offset"), 0, 0, 10_000)
	out := apiSearchResult{Q: q}
	if q == "" || len([]rune(q)) >= 200 {
		s.writeJSON(w, 200, out)
		return
	}
	if t, _ := tidyQuery(q); t != "" { // a pasted bullet, a glued number, a gazette reference
		q = t
	}
	var err error
	switch typ {
	case "laws":
		err = s.apiFindLaws(ctx, q, year, limit, offset, &out)
	case "articles":
		err = s.apiFindArticles(ctx, q, limit, offset, &out)
	case "rulings":
		err = s.apiFindRulings(ctx, q, limit, offset, &out)
	case "gazette":
		err = s.apiFindGazette(ctx, q, limit, offset, &out)
	default:
		err = s.apiFindAll(ctx, q, year, limit, offset, &out)
	}
	if err != nil {
		s.apiFail(w, r, err)
		return
	}
	s.apiRescue(ctx, q, typ, year, offset, limit, &out)
	if typ == "" || typ == "laws" {
		s.apiFindLawsEn(ctx, q, year, limit, offset, &out)
	}
	if semanticWanted && offset == 0 {
		s.addRelated(ctx, question, selectWanted, &out)
		if len(out.Related) > limit {
			out.Related = out.Related[:limit]
		}
	}
	s.track(r, map[string]any{"k": "search", "q": NormQuery(q), "api": true,
		"n": out.LawCount + out.ArtCount + out.RulCount + out.GazCount})
	s.writeJSON(w, 200, out)
}

// apiFindAll searches the four kinds at once, each on its own connection
// (each fills its own fields of out). One after the other, a common word
// ("قانون") took 5.4 to 6.9 s on the production server, past the MCP's 5 s budget.
func (s *Server) apiFindAll(ctx context.Context, q string, year, limit, offset int, out *apiSearchResult) error {
	errs := make(chan error, 4)
	go func() { errs <- s.apiFindLaws(ctx, q, year, limit, offset, out) }()
	go func() { errs <- s.apiFindArticles(ctx, q, limit, offset, out) }()
	go func() { errs <- s.apiFindRulings(ctx, q, limit, offset, out) }()
	go func() { errs <- s.apiFindGazette(ctx, q, limit, offset, out) }()
	var err error
	for range 4 {
		if e := <-errs; e != nil && err == nil {
			err = e
		}
	}
	return err
}

// apiFindLaws mirrors findLaws in docs.go: every word of q, spelling folded,
// anywhere in the title. year, when > 0, narrows to laws issued that year
// (the MCP search_legislation tool's year filter; the HTTP API takes it as
// ?year=).
func (s *Server) apiFindLaws(ctx context.Context, q string, year, limit, offset int, out *apiSearchResult) error {
	if err := s.apiFindLawsBase(ctx, q, year, limit, offset, out); err != nil || offset > 0 {
		return err
	}
	// A question that names one text ("cybercrime") has it first (glosspins.go).
	if pin := s.pinnedRow(ctx, q, year); pin != nil {
		first := s.lawSum(ctx, *pin, s.source(ctx, "law", pin.ID))
		laws := []apiLawSummary{first}
		added := true
		for _, l := range out.Laws {
			if l.ID == pin.ID {
				added = false
				continue
			}
			laws = append(laws, l)
		}
		if len(laws) > limit {
			laws = laws[:limit]
		}
		out.Laws = laws
		if added {
			out.LawCount++
		}
	}
	return nil
}

func (s *Server) apiFindLawsBase(ctx context.Context, q string, year, limit, offset int, out *apiSearchResult) error {
	// A citation ("law 28 of 10/2/2017", "رقم 175 تاريخ 8/5/2020") is looked up exactly, as on the site.
	if rq := readQuery(q); rq.lawCite() && year == 0 && offset == 0 {
		var res Results
		ok, err := s.findLawCite(ctx, &res, rq)
		if err != nil {
			return err
		}
		if ok {
			out.LawCount = res.LawCount
			for _, l := range res.Laws[:min(limit, len(res.Laws))] {
				out.Laws = append(out.Laws, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
			}
			return nil
		}
	}
	// A law's id, and the codes by their English or French name ("Penal
	// Code", "code penal"), as the MCP's search_legislation finds them.
	if year == 0 && offset == 0 {
		found, err := s.apiFindLawByIDOrCode(ctx, q)
		if err != nil {
			return err
		}
		for _, l := range found[:min(limit, len(found))] {
			out.Laws = append(out.Laws, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
		}
		out.LawCount = len(found)
		if len(found) > 0 && (isDigits(q) || !hasArabic(q)) {
			return nil
		}
	}
	conds, args := titleConds(strings.Fields(q), 1)
	if len(conds) == 0 {
		return nil
	}
	if year > 0 {
		args = append(args, year)
		conds = append(conds, "extract(year from l.issued_on)::int = $"+strconv.Itoa(len(args)))
	}
	cond := strings.Join(conds, " AND ")
	if err := s.db.QueryRow(ctx, "SELECT count(*) FROM legislation l WHERE "+cond, args...).Scan(&out.LawCount); err != nil {
		return err
	}
	simArg := len(args) + 1
	args = append(args, q, limit, offset)
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind
		WHERE `+cond+` ORDER BY (EXISTS (SELECT 1 FROM code c WHERE c.law_id = l.id)) DESC,
		similarity(ar_norm(l.title), ar_norm($`+strconv.Itoa(simArg)+`)) DESC, l.issued_on DESC NULLS LAST
		LIMIT $`+strconv.Itoa(simArg+1)+` OFFSET $`+strconv.Itoa(simArg+2), args...)
	if err != nil {
		return err
	}
	rowsOut, err := scanLawRows(rows)
	if err != nil {
		return err
	}
	for _, l := range rowsOut {
		if !slices.ContainsFunc(out.Laws, func(x apiLawSummary) bool { return x.ID == l.ID }) {
			out.Laws = append(out.Laws, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
		}
	}
	return nil
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// codeNameFold folds a code's name for matching: latinFold, with hyphens and
// apostrophes as spaces ("Code pénal" matches "code-penal").
func codeNameFold(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("-", " ", "'", " ").Replace(latinFold(s))), " ")
}

// apiFindLawByIDOrCode finds a law by its id, and the codes whose English or
// French name or slug holds every word of a Latin-script q.
func (s *Server) apiFindLawByIDOrCode(ctx context.Context, q string) ([]LawRow, error) {
	var found []LawRow
	if isDigits(q) && len(q) <= 9 {
		id, _ := strconv.Atoi(q)
		rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id)
		if err != nil {
			return nil, err
		}
		got, err := scanLawRows(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, got...)
	}
	if hasArabic(q) || !strings.ContainsFunc(q, unicode.IsLetter) {
		return found, nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.law_id, c.slug || ' ' || c.name_en || ' ' || coalesce(c.name_fr, '') FROM code c ORDER BY c.position`)
	if err != nil {
		return nil, err
	}
	var ids []int
	words := strings.Fields(codeNameFold(q))
	for rows.Next() {
		var id int
		var names string
		if err := rows.Scan(&id, &names); err != nil {
			rows.Close()
			return nil, err
		}
		names = codeNameFold(names)
		if slices.IndexFunc(words, func(w string) bool { return !strings.Contains(names, strings.TrimSuffix(w, "s")) }) < 0 {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		r, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM legislation l JOIN legislation_kind k ON k.name_ar = l.kind WHERE l.id = $1`, id)
		if err != nil {
			return nil, err
		}
		got, err := scanLawRows(r)
		if err != nil {
			return nil, err
		}
		found = append(found, got...)
	}
	return found, nil
}

// apiFindArticles mirrors findArticles in docs.go.
func (s *Server) apiFindArticles(ctx context.Context, q string, limit, offset int, out *apiSearchResult) error {
	const artWhere = `(a.search_exact @@ websearch_to_tsquery('simple', ar_norm($1))
		OR a.search_stem @@ websearch_to_tsquery('arabic', ar_light($1))
		OR a.search_fr @@ websearch_to_tsquery('french', $1))`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM legislation_article a WHERE `+artWhere+` LIMIT 1000) t`, q).
		Scan(&out.ArtCount); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `WITH hits AS (
		SELECT a.law_id, a.number, a.text_ar,
		       (a.search_exact @@ phraseto_tsquery('simple', ar_norm($1)))::int * 4
		     + (a.search_exact @@ websearch_to_tsquery('simple', ar_norm($1)))::int * 2
		     + ts_rank(a.search_stem, websearch_to_tsquery('arabic', ar_light($1)))
		     + (EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id))::int AS score,
		       (EXISTS (SELECT 1 FROM code c WHERE c.law_id = a.law_id))::int AS is_code
		FROM `+articleSource(q, artWhere)+` WHERE `+artWhere+`
		ORDER BY score DESC, is_code DESC LIMIT $2 OFFSET $3)
		SELECT `+lawRowCols+`, h.number, ts_headline('arabic', h.text_ar, websearch_to_tsquery('arabic', ar_light($1)),
		       '`+headlineMarks+`, MaxWords=40, MinWords=18, ShortWord=2')
		FROM hits h JOIN legislation l ON l.id = h.law_id JOIN legislation_kind k ON k.name_ar = l.kind
		ORDER BY h.score DESC, h.is_code DESC`, q, limit, offset)
	if err != nil {
		return err
	}
	type hitRow struct {
		x   LawRow
		art string
		hl  string
	}
	rs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hitRow, error) {
		var out hitRow
		err := r.Scan(&out.x.ID, &out.x.Kind, &out.x.KindEn, &out.x.Number, &out.x.IssuedOn, &out.x.Title, &out.x.Status,
			&out.x.Articles, &out.x.Code, &out.art, &out.hl)
		return out, err
	})
	if err != nil {
		return err
	}
	// An article the query cites, and articles whose calculated amount
	// matches it, come first, once each.
	var first []apiArticleHit
	if offset == 0 {
		if hit, ok := s.citedArticle(ctx, q); ok {
			first = append(first, hit)
		}
		first = append(first, s.calculatedMatches(ctx, q)...)
	}
	seen := map[string]bool{}
	var extra int
	for _, h := range first {
		key := strconv.Itoa(h.Law.ID) + "/" + h.Number
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Articles = append(out.Articles, h)
		extra++
	}
	calculated := s.apiHitCalculator(ctx)
	for _, r := range rs {
		if seen[strconv.Itoa(r.x.ID)+"/"+r.art] {
			continue
		}
		out.Articles = append(out.Articles, apiArticleHit{
			Law:               s.lawSum(ctx, r.x, s.source(ctx, "law", r.x.ID)),
			Number:            r.art,
			CalculatedCurrent: calculated(r.x.ID, r.art),
			Snippet:           stripMarks(r.hl),
			URL:               Base + artPath(r.x.ID, r.art),
		})
	}
	if len(out.Articles) > limit {
		out.Articles = out.Articles[:limit]
	}
	out.ArtCount += extra
	return nil
}

func (s *Server) apiFindRulings(ctx context.Context, q string, limit, offset int, out *apiSearchResult) error {
	n, hits, err := s.searchRulings(ctx, q, limit, offset)
	if err != nil {
		return err
	}
	out.RulCount = n
	ids := make([]int, len(hits))
	for i, r := range hits {
		ids[i] = r.ID
	}
	withFile := map[int]bool{}
	if rows, err := s.db.Query(ctx, `SELECT ruling_id FROM ruling_file WHERE ruling_id = ANY($1)`, ids); err == nil {
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				withFile[id] = true
			}
		}
		rows.Close()
	}
	for _, r := range hits {
		h := apiRulingHit{ID: r.ID, Title: r.Title, Court: r.Court, Snippet: stripMarks(r.Snippet),
			URL: Base + "/ruling/" + strconv.Itoa(r.ID) + "/", JudgmentPage: r.Page, UpdatedAt: s.listedChange(ctx, "ruling", r.ID)}
		if withFile[r.ID] {
			h.PDFURL = ULRulingPDF(r.ID)
		}
		if r.On != nil {
			h.DecidedOn = r.On.Format("2006-01-02")
		}
		out.Rulings = append(out.Rulings, h)
	}
	return nil
}

// apiFindGazette has no HTML equivalent (the search page only covers laws,
// articles and rulings): a loose match on the issue's own number, since
// gazette issues carry no body text of their own to search.
func (s *Server) apiFindGazette(ctx context.Context, q string, limit, offset int, out *apiSearchResult) error {
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM gazette_issue WHERE number ILIKE '%' || $1 || '%'`, q).Scan(&out.GazCount); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `SELECT id, number, published_on FROM gazette_issue WHERE number ILIKE '%' || $1 || '%'
		ORDER BY published_on DESC NULLS LAST LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var number *string
		var on *time.Time
		if err := rows.Scan(&id, &number, &on); err != nil {
			return err
		}
		h := apiGazetteHit{ID: id, URL: Base + "/gazette/" + strconv.Itoa(id) + "/", UpdatedAt: s.listedChange(ctx, "gazette", id)}
		if number != nil {
			h.Number = *number
		}
		if on != nil {
			h.PublishedOn = on.Format("2006-01-02")
		}
		out.Gazette = append(out.Gazette, h)
	}
	return rows.Err()
}

// ---------------------------------------------------------------- one law

type apiSection struct {
	ID      int     `json:"id"`
	Depth   int     `json:"depth"`
	Heading string  `json:"heading"`
	Note    *string `json:"note,omitempty"` // the Centre's editorial note on the chapter
}

type apiArticleRef struct {
	Position  int    `json:"position"`
	Number    string `json:"number"`
	SectionID *int   `json:"section_id,omitempty"` // the table_of_contents entry it sits under
	URL       string `json:"url"`
}

type apiLawDetail struct {
	apiLawSummary
	GazetteNumber   *string         `json:"gazette_number,omitempty"`
	GazetteDate     *string         `json:"gazette_date,omitempty"`
	GazetteIssueID  *int            `json:"gazette_issue_id,omitempty"`
	Preamble        *string         `json:"preamble,omitempty"`
	ExplanatoryNote *string         `json:"explanatory_note,omitempty"`
	Closing         *string         `json:"closing,omitempty"`
	TableOfContents []apiSection    `json:"table_of_contents,omitempty"`
	Articles        []apiArticleRef `json:"articles,omitempty"`
	// AmendedBy: the texts that changed this one; Amended: the texts this one
	// changed. One entry per relation and other text, oldest first; articles
	// are named when the change is to articles, not to the text as a whole.
	AmendedBy      []apiLawChange `json:"amended_by,omitempty"`
	Amended        []apiLawChange `json:"amended,omitempty"`
	NotLegalAdvice string         `json:"not_legal_advice"`
}

// loadLawOutline loads what lawDetail shows of a law: its header, chapters,
// article numbers (with those only an amendment carries) and the changes to
// it. Not the texts, versions, links or rulings loadLaw adds for the page:
// on the Penal Code those took 2 to 4 s on the production server.
func (s *Server) loadLawOutline(ctx context.Context, id int) (*Law, error) {
	l, err := s.loadLaw(ctx, id, false)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT id, depth, heading, note FROM legislation_section WHERE law_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	if l.Sections, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Section]); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT position, number, section_id FROM legislation_article WHERE law_id = $1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	l.List, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Article, error) {
		a := &Article{}
		return a, r.Scan(&a.Position, &a.Number, &a.SectionID)
	})
	if err != nil {
		return nil, err
	}
	all, err := s.changes(ctx, "x.law_id = $1", "x.by_law_id", id)
	if err != nil {
		return nil, err
	}
	l.sortChanges(all)
	// The articles only an amendment carries, placed as attachVersions does.
	rows, err = s.db.Query(ctx, `SELECT DISTINCT article_number FROM article_version WHERE law_id = $1 ORDER BY article_number`, id)
	if err != nil {
		return nil, err
	}
	nums, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, a := range l.List {
		have[a.Number] = true
	}
	for _, n := range nums {
		if !have[n] {
			have[n] = true
			l.insertArticle(&Article{Number: n, Added: true})
		}
	}
	return l, nil
}

// lawDetail loads a law and builds the shape /api/v1/law/{id} and the (not
// yet wired) MCP get_law tool both return; see api.go's package doc. It
// returns the loaded Law too, so a caller can track a read (trackRead) the
// way the HTTP handler does.
func (s *Server) lawDetail(ctx context.Context, id int) (*Law, apiLawDetail, error) {
	l, err := s.loadLawOutline(ctx, id)
	if err != nil {
		return nil, apiLawDetail{}, err
	}
	out, err := s.lawDetailOf(ctx, l)
	return l, out, err
}

// lawDetailOf builds lawDetail's shape from a law loaded by loadLawOutline
// (or loadLaw, which loads more).
func (s *Server) lawDetailOf(ctx context.Context, l *Law) (apiLawDetail, error) {
	id := l.ID
	out := apiLawDetail{apiLawSummary: s.lawSum(ctx, l.LawRow, s.source(ctx, "law", id)),
		GazetteNumber: l.GazetteNumber, Preamble: l.Preamble, ExplanatoryNote: l.ExplanatoryNote, Closing: l.Closing,
		GazetteIssueID: l.GazetteIssue, NotLegalAdvice: notLegalAdvice}
	out.UpdatedAt = changedISO(l.Changed) // read now, not the hourly copy lawSum used
	out.GazetteDate = ymd(l.GazetteDate)
	// From the link graph, as the MCP's get_law does; the amendment table adds
	// what the graph lacks (see mergeAmendments).
	self := l.GazetteDate
	if self == nil {
		self = l.IssuedOn
	}
	by, err := s.lawLinks(ctx, id, true, nil)
	if err != nil {
		return apiLawDetail{}, err
	}
	out.AmendedBy = mergeAmendments(by, s.lawChanges(ctx, append(append([]Change{}, l.RepealedByChange...), l.Changes...)))
	of, err := s.lawLinks(ctx, id, false, self)
	if err != nil {
		return apiLawDetail{}, err
	}
	amends, err := s.changes(ctx, "x.by_law_id = $1 AND x.status <> 'review'", "x.law_id", id)
	if err != nil {
		return apiLawDetail{}, err
	}
	out.Amended = mergeAmendments(of, s.lawChanges(ctx, amends))
	for _, sec := range l.Sections {
		out.TableOfContents = append(out.TableOfContents, apiSection{ID: sec.ID, Depth: sec.Depth, Heading: Heading(sec.Heading), Note: sec.Note})
	}
	for _, a := range l.List {
		out.Articles = append(out.Articles, apiArticleRef{Position: a.Position, Number: a.Number, SectionID: a.SectionID,
			URL: Base + artPath(id, a.Number)})
	}
	return out, nil
}

func (s *Server) apiLaw(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.apiNotFound(w, "law")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	l, out, err := s.lawDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			s.apiNotFound(w, "law")
			return
		}
		s.apiFail(w, r, err)
		return
	}
	s.trackRead(r, l, "")
	s.writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- one article

type apiArticleVersion struct {
	Major      int     `json:"major"`
	Minor      int     `json:"minor"`
	ValidFrom  *string `json:"valid_from,omitempty"`
	ValidTo    *string `json:"valid_to,omitempty"`
	State      string  `json:"state"`
	TextAr     *string `json:"text_ar,omitempty"`
	TextSource string  `json:"text_source"`
	Note       *string `json:"note,omitempty"`
}

// apiChange is a text that changed the article, with the amending
// article's own words, newest first (the same list the article page shows).
type apiChange struct {
	Relation  string        `json:"relation"`
	Part      *string       `json:"part,omitempty"`
	By        apiLawSummary `json:"by"`
	ByArticle string        `json:"by_article,omitempty"`
	Effective *string       `json:"effective_on,omitempty"`
	TextAr    string        `json:"text_ar,omitempty"`
	FoundBy   []string      `json:"found_by"`
	URL       string        `json:"url"`
}

// apiAffected is a general repeal ("all contrary provisions are repealed")
// that may touch the article: a lead to check, never a finding.
type apiAffected struct {
	By        apiLawSummary `json:"by"`
	ByArticle string        `json:"by_article"`
	URL       string        `json:"url"`
}

// apiRulingRef is a ruling that applied the article, newest first.
type apiRulingRef struct {
	ID        int     `json:"id"`
	Court     string  `json:"court"`
	Number    string  `json:"number,omitempty"`
	Year      *int16  `json:"year,omitempty"`
	DecidedOn *string `json:"decided_on,omitempty"`
	URL       string  `json:"url"`
}

type apiArticleDetail struct {
	CalculatedCurrent *calculatedcurrent.Record `json:"calculated_current,omitempty"`
	Law               apiLawSummary             `json:"law"`
	Number            string                    `json:"number"`
	Note              *string                   `json:"note,omitempty"`
	CentreError       *CentreError              `json:"centre_error,omitempty"`
	TextAr            string                    `json:"text_ar"`
	TextFr            *string                   `json:"text_fr,omitempty"`
	CentreTextAr      *string                   `json:"centre_text_ar,omitempty"`
	CentreTextFr      *string                   `json:"centre_text_fr,omitempty"`
	InForceFrom       *string                   `json:"in_force_from,omitempty"`
	Versions          []apiArticleVersion       `json:"versions,omitempty"`
	Repealed          bool                      `json:"repealed,omitempty"`
	Changes           []apiChange               `json:"changes,omitempty"`
	Affected          []apiAffected             `json:"possibly_affected_by,omitempty"`
	RulingCount       int                       `json:"rulings_count,omitempty"`
	Rulings           []apiRulingRef            `json:"rulings,omitempty"`
	URL               string                    `json:"url"`
	SourceURL         string                    `json:"source_url,omitempty"`
	// UpdatedAt is the day the page of the article's text last changed here:
	// an article belongs to its text, and they change together.
	UpdatedAt      *string `json:"updated_at,omitempty"`
	NotLegalAdvice string  `json:"not_legal_advice"`
}

// articleDetail loads a law and finds one article in it, by its printed
// number or the slug form (the HTTP route matches on the slug; the MCP
// get_article tool, not yet wired, takes either). art is nil, with no
// error, when the law exists but has no such article.
func (s *Server) articleDetail(ctx context.Context, lawID int, number string) (l *Law, art *Article, out apiArticleDetail, err error) {
	l, err = s.loadLaw(ctx, lawID, true)
	if err != nil {
		return nil, nil, apiArticleDetail{}, err
	}
	want := Slug(strings.TrimSpace(number))
	for _, a := range l.List {
		if Slug(a.Number) != want {
			continue
		}
		sourceURL := s.source(ctx, "law", lawID)
		out = apiArticleDetail{Law: s.lawSum(ctx, l.LawRow, sourceURL), Number: a.Number, Note: a.Note, CentreError: a.CentreError,
			TextAr: a.TextAr, TextFr: a.TextFr, InForceFrom: ymd(a.From),
			URL:       Base + artPath(lawID, a.Number),
			SourceURL: sourceURL, UpdatedAt: changedISO(l.Changed), NotLegalAdvice: notLegalAdvice}
		out.Law.UpdatedAt = out.UpdatedAt
		for _, v := range a.Versions {
			out.Versions = append(out.Versions, apiArticleVersion{Major: v.Major, Minor: v.Minor,
				ValidFrom: ymd(v.From), ValidTo: ymd(v.To), State: v.State, TextAr: v.TextAr,
				TextSource: v.Source, Note: v.Note})
		}
		out.Repealed = a.Repealed()
		out.CalculatedCurrent = l.calculatedCurrent(a)
		// text_ar is the wording the page prints (in force today). When an
		// amendment replaced the Centre's text, the Centre's wording, and its
		// French, which follows it, move to centre_text_*, so a client quoting
		// text_ar never quotes a superseded article. Same shape as the MCP.
		if shown := a.Shown(); shown != a.TextAr {
			centre := a.TextAr
			out.TextAr, out.CentreTextAr = shown, &centre
			out.TextFr, out.CentreTextFr = nil, a.TextFr
		}
		out.TextAr = scanReadings(out.TextAr) // a scanned text's disputed spans, as the MCP shows them
		for _, k := range a.Links {
			out.Changes = append(out.Changes, apiChange{Relation: k.Relation, Part: k.Part,
				By: s.lawSum(ctx, k.By, ""), ByArticle: k.ByArticle, Effective: ymd(k.Effective), TextAr: k.Text,
				FoundBy: k.Sources, URL: Base + artPath(k.By.ID, k.ByArticle)})
		}
		out.RulingCount = a.RulingCount
		for _, x := range a.Rulings {
			out.Rulings = append(out.Rulings, apiRulingRef{ID: x.ID, Court: x.Court, Number: x.Number, Year: x.Year,
				DecidedOn: ymd(x.On), URL: Base + "/ruling/" + strconv.Itoa(x.ID) + "/"})
		}
		for _, f := range a.Affected {
			out.Affected = append(out.Affected, apiAffected{By: s.lawSum(ctx, f.By, ""), ByArticle: f.ByArticle,
				URL: Base + artPath(f.By.ID, f.ByArticle)})
		}
		return l, a, out, nil
	}
	return l, nil, apiArticleDetail{}, nil
}

func (s *Server) apiArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.apiNotFound(w, "law")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	l, a, out, err := s.articleDetail(ctx, id, r.PathValue("n"))
	if err != nil {
		if errorIsNoRows(err) {
			s.apiNotFound(w, "law")
			return
		}
		s.apiFail(w, r, err)
		return
	}
	if a == nil {
		s.apiNotFound(w, "article")
		return
	}
	s.trackRead(r, l, a.Number)
	s.writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- one ruling

type apiRulingDetail struct {
	ID        int      `json:"id"`
	Court     *string  `json:"court,omitempty"`
	CourtEn   *string  `json:"court_en,omitempty"`
	Number    *string  `json:"number,omitempty"`
	Year      *int     `json:"year,omitempty"`
	DecidedOn *string  `json:"decided_on,omitempty"`
	Title     string   `json:"title"`
	President *string  `json:"president,omitempty"`
	Members   []string `json:"members,omitempty"`
	TextAr    *string  `json:"text_ar,omitempty"`
	Note      string   `json:"note,omitempty"` // set when the Centre holds only the judgment's file
	URL       string   `json:"url"`
	SourceURL string   `json:"source_url,omitempty"`
	// The full judgment: the Centre's scan (a PDF) and its machine reading,
	// page by page. The reading may contain errors; the scan is the reference.
	PagesModel     string          `json:"pages_model,omitempty"` // which models read the pages, as in the dataset
	PDFURL         string          `json:"pdf_url,omitempty"`     // the scan on the Lebanese University's site: give it with any answer
	PageCount      int             `json:"page_count,omitempty"`  // pages in the scan
	Pages          []apiRulingPage `json:"pages,omitempty"`
	PagesNote      string          `json:"pages_note,omitempty"`
	UpdatedAt      *string         `json:"updated_at,omitempty"` // the day its page last changed here (page_state), when known
	NotLegalAdvice string          `json:"not_legal_advice"`
}

// apiRulingPage is one page of a judgment's machine reading.
type apiRulingPage struct {
	N      int    `json:"n"`
	TextAr string `json:"text_ar"`
	// LowConfidence: the page is hard to read (mostly handwriting); expect
	// errors and read the scan (pdf_url).
	LowConfidence bool `json:"low_confidence,omitempty"`
}

func (s *Server) rulingDetail(ctx context.Context, id int) (apiRulingDetail, error) {
	var court, courtEn, number, president, text *string
	var year *int
	var on *time.Time
	var members []string
	var title string
	err := s.db.QueryRow(ctx, `SELECT r.title, r.court, c.name_en, r.number, r.year, r.decided_on, r.president, r.members, r.text_ar
		FROM ruling r LEFT JOIN court c ON c.name_ar = r.court WHERE r.id = $1`, id).
		Scan(&title, &court, &courtEn, &number, &year, &on, &president, &members, &text)
	if err != nil {
		return apiRulingDetail{}, err
	}
	out := apiRulingDetail{ID: id, Court: court, CourtEn: courtEn, Number: number, Year: year, DecidedOn: ymd(on),
		Title: title, President: president, Members: members, TextAr: text,
		URL: Base + "/ruling/" + strconv.Itoa(id) + "/", SourceURL: s.source(ctx, "ruling", id),
		UpdatedAt: changedISO(s.changedOn(ctx, "ruling", id)), NotLegalAdvice: notLegalAdvice}
	if text == nil {
		out.Note = "The Centre holds this judgment as a file only, not as typed text."
	}
	j, err := s.loadJudgment(ctx, id)
	if err != nil {
		return apiRulingDetail{}, err
	}
	if j.HasFile {
		out.PDFURL = ULRulingPDF(id)
		out.PageCount = j.Scanned
	}
	if len(j.Pages) > 0 {
		for _, p := range j.Pages {
			out.Pages = append(out.Pages, apiRulingPage{N: p.N, TextAr: scanReadings(p.Text), LowConfidence: p.LowConfidence}) // readers' disputes spelled out
		}
		out.PageCount = max(out.PageCount, j.Pages[len(j.Pages)-1].N)
		out.PagesModel = j.Model
		out.PagesNote = "Machine reading of the scanned judgment. It may contain errors; the scan (pdf_url) is the reference."
		for _, p := range out.Pages {
			if p.LowConfidence {
				out.PagesNote += " Pages with low_confidence: true are hard to read (mostly handwriting): expect errors."
				break
			}
		}
		if text == nil {
			out.Note = "The Centre holds this judgment as a scanned file; its machine reading is in pages."
		}
	} else if j.HasFile && text == nil {
		out.Note = "The Centre holds this judgment as a scanned file only; its text has not been read yet."
	}
	return out, nil
}

func (s *Server) apiRuling(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.apiNotFound(w, "ruling")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	out, err := s.rulingDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			s.apiNotFound(w, "ruling")
			return
		}
		s.apiFail(w, r, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- one gazette issue

type apiGazetteDetail struct {
	ID        int             `json:"id"`
	Number    *string         `json:"number,omitempty"`
	Published *string         `json:"published_on,omitempty"`
	Texts     []apiLawSummary `json:"texts"`
	URL       string          `json:"url"`
	SourceURL string          `json:"source_url,omitempty"`
	UpdatedAt *string         `json:"updated_at,omitempty"` // the day its page last changed here (page_state), when known
}

func (s *Server) gazetteDetail(ctx context.Context, id int) (apiGazetteDetail, error) {
	var number *string
	var on *time.Time
	if err := s.db.QueryRow(ctx, "SELECT number, published_on FROM gazette_issue WHERE id = $1", id).Scan(&number, &on); err != nil {
		return apiGazetteDetail{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawRowCols+` FROM gazette_entry e JOIN legislation l ON l.id = e.law_id
		JOIN legislation_kind k ON k.name_ar = l.kind WHERE e.issue_id = $1 ORDER BY l.id`, id)
	if err != nil {
		return apiGazetteDetail{}, err
	}
	laws, err := scanLawRows(rows)
	if err != nil {
		return apiGazetteDetail{}, err
	}
	out := apiGazetteDetail{ID: id, Number: number, Published: ymd(on),
		URL: Base + "/gazette/" + strconv.Itoa(id) + "/", SourceURL: s.source(ctx, "journal", id),
		UpdatedAt: changedISO(s.changedOn(ctx, "gazette", id))}
	for _, l := range laws {
		out.Texts = append(out.Texts, s.lawSum(ctx, l, s.source(ctx, "law", l.ID)))
	}
	return out, nil
}

func (s *Server) apiGazette(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.apiNotFound(w, "gazette issue")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	out, err := s.gazetteDetail(ctx, id)
	if err != nil {
		if errorIsNoRows(err) {
			s.apiNotFound(w, "gazette issue")
			return
		}
		s.apiFail(w, r, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func errorIsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// apiHitCalculator loads the reviewed law once per search response. Search
// snippets stay verbatim; the separate record uses current source and state.
func (s *Server) apiHitCalculator(ctx context.Context) func(int, string) *calculatedcurrent.Record {
	var records map[string]*calculatedcurrent.Record
	loaded := false
	return func(lawID int, number string) *calculatedcurrent.Record {
		if lawID != 244565 {
			return nil
		}
		if !loaded {
			loaded = true
			records = map[string]*calculatedcurrent.Record{}
			if law, err := s.loadLaw(ctx, lawID, true); err == nil {
				for _, a := range law.List {
					if _, exists := records[a.Number]; !exists {
						records[a.Number] = law.calculatedCurrent(a)
					}
				}
			}
		}
		return records[number]
	}
}

// apiCalculatedCurrent exposes all currently eligible calculated articles,
// including an explicit empty map when there are no derived claims.
func (s *Server) apiCalculatedCurrent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id != 244565 {
		http.NotFound(w, r)
		return
	}
	law, err := s.loadLaw(r.Context(), id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"law_id": id, "articles": map[string]any{}})
		return
	}
	if err != nil {
		http.Error(w, "cannot load current law", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"law_id": id, "articles": law.calculatedCollection()})
}
