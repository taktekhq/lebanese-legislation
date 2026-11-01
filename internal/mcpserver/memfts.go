package mcpserver

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// The unofficial English titles (about 100,000 short lines) and the French
// article texts (about 4,000) are small enough to search in memory, which
// reads them exactly as FTS5's unicode61 tokenizer did ("81/2018" is two
// tokens, "écrit" is "ecrit") and costs one read every few hours instead of
// building a vector for every row on every search.

// memDoc is one row of an in-memory full-text table.
type memDoc struct {
	lawID    int
	position int    // the article's, for the French texts
	issuedOn string // the law's, for the English titles' year filter
	code     bool   // the law is a code
	served   bool   // the law is one the server serves (its kind is known)
	tokens   []string
}

type memCorpus struct {
	docs   []memDoc // in the snapshot's row order
	avgLen float64
}

type memCache struct {
	mu     sync.Mutex
	at     time.Time
	corpus *memCorpus
}

const memTTL = 6 * time.Hour

func (c *memCache) get(ctx context.Context, load func(context.Context) (*memCorpus, error)) (*memCorpus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.corpus != nil && time.Since(c.at) < memTTL {
		return c.corpus, nil
	}
	m, err := load(ctx)
	if err != nil {
		return nil, err
	}
	c.corpus, c.at = m, time.Now()
	return m, nil
}

func (s *Server) loadMem(ctx context.Context, sql string) (*memCorpus, error) {
	rows, err := s.db.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := &memCorpus{}
	total := 0
	for rows.Next() {
		var d memDoc
		var text string
		var issued *string
		if err := rows.Scan(&d.lawID, &d.position, &issued, &d.code, &d.served, &text); err != nil {
			return nil, err
		}
		if issued != nil {
			d.issuedOn = *issued
		}
		d.tokens = ftsTokens(text)
		total += len(d.tokens)
		m.docs = append(m.docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(m.docs) > 0 {
		m.avgLen = float64(total) / float64(len(m.docs))
	}
	return m, nil
}

// englishTitles is the snapshot's law_search_en: every non-blank title.
func (s *Server) englishTitles(ctx context.Context) (*memCorpus, error) {
	return s.englishMem.get(ctx, func(ctx context.Context) (*memCorpus, error) {
		return s.loadMem(ctx, `SELECT te.law_id, 0, to_char(l.issued_on, 'YYYY-MM-DD'), c.law_id IS NOT NULL, k.name_ar IS NOT NULL, te.title_en
			FROM legislation_title_en te LEFT JOIN legislation l ON l.id = te.law_id LEFT JOIN legislation_kind k ON k.name_ar = l.kind
			LEFT JOIN code c ON c.law_id = te.law_id
			WHERE te.title_en ~ '\S' ORDER BY te.law_id`)
	})
}

// frenchTexts is the snapshot's article_search_fr: every non-blank French text.
func (s *Server) frenchTexts(ctx context.Context) (*memCorpus, error) {
	return s.frenchMem.get(ctx, func(ctx context.Context) (*memCorpus, error) {
		return s.loadMem(ctx, `SELECT a.law_id, a.position, NULL::text, c.law_id IS NOT NULL, k.name_ar IS NOT NULL, a.text_fr
			FROM legislation_article a JOIN legislation l ON l.id = a.law_id LEFT JOIN legislation_kind k ON k.name_ar = l.kind
			LEFT JOIN code c ON c.law_id = a.law_id
			WHERE a.text_fr ~ '\S' ORDER BY a.law_id, a.position`)
	})
}

// search ranks the served rows matching q as the Worker's queries did:
// codes first, then the best bm25, ties in row order; keep filters rows.
// Every row counts toward the statistics, as every row of the FTS5 table did.
func (m *memCorpus) search(q ftsQuery, limit int, keep func(*memDoc) bool) []*memDoc {
	phrases := q.phrases()
	df := make([]float64, len(phrases))
	type hit struct {
		d     *memDoc
		tf    []float64
		score float64
		order int
	}
	var hits []hit
	for i := range m.docs {
		d := &m.docs[i]
		var tf []float64
		for j, p := range phrases {
			if n := p.tf(d.tokens); n > 0 {
				if tf == nil {
					tf = make([]float64, len(phrases))
				}
				tf[j] = float64(n)
				df[j]++
			}
		}
		if tf != nil && d.served && q.matchesTF(tf) && (keep == nil || keep(d)) {
			hits = append(hits, hit{d: d, tf: tf, order: i})
		}
	}
	st := corpusStats{rows: float64(len(m.docs)), avgLen: m.avgLen}
	for i := range hits {
		hits[i].score = bm25(st, df, hits[i].tf, float64(len(hits[i].d.tokens)))
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.d.code != b.d.code {
			return a.d.code
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.order < b.order
	})
	var out []*memDoc
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].d)
	}
	return out
}

// matchesTF reports whether phrase frequencies, in q.phrases() order,
// satisfy every group.
func (q ftsQuery) matchesTF(tf []float64) bool {
	i := 0
	for _, g := range q.groups {
		hit := false
		for range g {
			if tf[i] > 0 {
				hit = true
			}
			i++
		}
		if !hit {
			return false
		}
	}
	return true
}

// englishTitleSearch matches the unofficial English titles, codes first,
// then by bm25.
func (s *Server) englishTitleSearch(ctx context.Context, match string, year yearVal, limit int) ([]lawRow, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return nil, nil
	}
	m, err := s.englishTitles(ctx)
	if err != nil {
		return nil, err
	}
	var keep func(*memDoc) bool
	if year.positive() {
		keep = func(d *memDoc) bool { return strings.HasPrefix(d.issuedOn, year.s+"-") }
	}
	docs := m.search(q, limit, keep)
	if len(docs) == 0 {
		return nil, nil
	}
	ids := make([]int32, len(docs))
	for i, d := range docs {
		ids[i] = int32(d.lawID)
	}
	rows, err := s.db.Query(ctx, `SELECT `+lawCols+` FROM `+lawFrom+` WHERE l.id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	laws, err := scanLaws(rows, nil)
	if err != nil {
		return nil, err
	}
	byID := map[int]lawRow{}
	for _, l := range laws {
		byID[l.ID] = l
	}
	var out []lawRow
	for _, d := range docs {
		if l, has := byID[d.lawID]; has {
			out = append(out, l)
		}
	}
	return out, nil
}

// frenchMatch matches the French texts the Centre published.
func (s *Server) frenchMatch(ctx context.Context, match string, limit int) ([]articleHit, error) {
	q, ok := parseFTS(match).usable()
	if !ok {
		return nil, nil
	}
	m, err := s.frenchTexts(ctx)
	if err != nil {
		return nil, err
	}
	docs := m.search(q, limit, nil)
	cands := make([]candidate, len(docs))
	for i, d := range docs {
		cands[i] = candidate{lawID: d.lawID, position: d.position}
	}
	return s.hitsFor(ctx, cands, "a.text_fr")
}

// Warm reads ahead what the first searches would otherwise read: the codes,
// the in-memory English titles and French texts, the articles' search
// statistics and the calculated amounts. Call it once after New, in the
// background; searches work without it, the first ones only slower.
func (s *Server) Warm(ctx context.Context) error {
	if _, err := s.codes(ctx); err != nil {
		return err
	}
	if _, err := s.englishTitles(ctx); err != nil {
		return err
	}
	if _, err := s.frenchTexts(ctx); err != nil {
		return err
	}
	if _, err := s.stats.corpus(ctx, "articles", s.articleStats); err != nil {
		return err
	}
	_, err := s.calculatedIndex(ctx)
	return err
}
