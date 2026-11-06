package site

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Ruling search. A ruling is found by its title and the Centre's summary
// (ruling.search_exact / search_stem), and, once its scanned judgment has been
// read by machine, by the words in the judgment's pages (ruling_page.search_stem,
// the same Arabic folding plus ar_digits). Matches in the title or summary
// always rank above matches only the judgment has: the judgment is a machine
// reading of a scan and may contain errors.

// rulingSummaryWhere matches a ruling's own title and summary ($1 is the query).
const rulingSummaryWhere = `(search_exact @@ websearch_to_tsquery('simple', ar_norm($1))
	OR search_stem @@ websearch_to_tsquery('arabic', ar_light($1)))`

// judgmentQuery is the query as the judgment pages' vectors are made.
const judgmentQuery = `websearch_to_tsquery('arabic', ar_light(ar_digits($1)))`

// judgmentCandidates caps how many matching pages are ranked. A word that
// nearly every page holds would otherwise rank hundreds of thousands of pages
// on a small server; rare words, the point of searching judgments, are never
// cut off, and a common one only reaches here once the summaries have run out.
const judgmentCandidates = 3000

// rulingHit is one ruling found by a search. Page is set when only the
// judgment's text matched: the page its excerpt comes from.
type rulingHit struct {
	ID      int
	Title   string
	Court   string
	On      *time.Time
	Snippet string
	Page    int
	key     rulingKey
}

// rulingKey is a hit's place in the ranking: tier (1 the title or summary
// matched, 0 only the judgment), phrase (the words as a phrase), rank, date,
// then id, lowest first on a tie. A page starts after the previous page's
// last key.
type rulingKey struct {
	tier, ph int
	rk       float32
	on       time.Time // cursor.NoDate when the ruling has no date
	id       int
}

func (k rulingKey) fields() []string {
	on := k.on
	return []string{keyInt(k.tier), keyInt(k.ph), keyReal(k.rk), keyDate(&on), keyInt(k.id)}
}

// searchRulings returns how many rulings match (counted up to 1,000), at
// most limit hits after the key after (nil: from the first), and whether
// more follow: those matching the title or summary first, then those
// matching only the judgment. A limit of 0 only counts.
func (s *Server) searchRulings(ctx context.Context, q string, limit int, after *rulingKey) (int, []rulingHit, bool, error) {
	var sum int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM ruling WHERE `+rulingSummaryWhere+` LIMIT 1000) t`, q).Scan(&sum); err != nil {
		return 0, nil, false, err
	}
	total, judgments := sum, false
	if sum < 1000 {
		// Rulings whose judgment matches and whose summary does not add to the count.
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (
			(SELECT id FROM ruling WHERE `+rulingSummaryWhere+` LIMIT 1000)
			UNION (SELECT ruling_id FROM ruling_page WHERE search_stem @@ `+judgmentQuery+` LIMIT 1000)) t`, q).Scan(&total); err != nil {
			return 0, nil, false, err
		}
		total = min(total, 1000)
		judgments = total > sum
	}
	if limit == 0 {
		return total, nil, false, nil
	}
	// The judgments are ranked only once the summaries run out on a page.
	// The first page knows from the count whether they do; a later one ranks
	// the summaries alone first, then both when they fall short of the page.
	// A page the summaries fill exactly has more after it when the count
	// found judgments.
	var hits []rulingHit
	var err error
	both := judgments && ((after == nil && sum < limit) || (after != nil && after.tier == 0))
	if !both {
		if hits, err = s.rulingPage(ctx, q, limit+1, after, false); err != nil {
			return 0, nil, false, err
		}
		if len(hits) == limit {
			return total, hits, judgments, nil
		}
		both = len(hits) < limit && judgments
	}
	if both {
		if hits, err = s.rulingPage(ctx, q, limit+1, after, true); err != nil {
			return 0, nil, false, err
		}
	}
	more := len(hits) > limit
	return total, hits[:min(limit, len(hits))], more, nil
}

// rulingPage ranks the rulings matching q after the key after and returns
// the first n, with their excerpts; withJudgments adds the rulings only
// their judgment's pages match.
func (s *Server) rulingPage(ctx context.Context, q string, n int, after *rulingKey, withJudgments bool) ([]rulingHit, error) {
	const opts = `'` + headlineMarks + `, MaxWords=35, MinWords=15, ShortWord=2'`
	candidates := `SELECT id, 1 AS tier, (search_exact @@ phraseto_tsquery('simple', ar_norm($1)))::int AS ph,
			ts_rank(search_stem, websearch_to_tsquery('arabic', ar_light($1))) AS rk, 0 AS pg
		FROM ruling WHERE ` + rulingSummaryWhere
	if withJudgments {
		candidates = `WITH s AS MATERIALIZED (` + candidates + `)
		SELECT * FROM s UNION ALL
		SELECT ruling_id, 0, max(ph), max(rk), (array_agg(n ORDER BY rk DESC))[1] FROM (
			SELECT ruling_id, n, (search_stem @@ phraseto_tsquery('arabic', ar_light(ar_digits($1))))::int AS ph,
				ts_rank(search_stem, ` + judgmentQuery + `) AS rk
			FROM ruling_page WHERE search_stem @@ ` + judgmentQuery + ` LIMIT ` + fmt.Sprint(judgmentCandidates) + `) p
		WHERE ruling_id NOT IN (SELECT id FROM s) GROUP BY ruling_id`
	}
	args := []any{q, n}
	keyset := ""
	if after != nil {
		args = append(args, after.tier, after.ph, after.rk, after.on, after.id)
		keyset = ` WHERE (c.tier, c.ph, c.rk, coalesce(r.decided_on, date '0001-01-01'), -r.id) < ($3::int, $4::int, $5::real, $6::date, -$7::int)`
	}
	rows, err := s.db.Query(ctx, `SELECT h.id, h.title, h.court, h.decided_on, h.pg,
		ts_headline('arabic', CASE WHEN h.pg = 0 THEN coalesce(h.text_ar, '')
			ELSE (SELECT text_ar FROM ruling_page WHERE ruling_id = h.id AND n = h.pg) END,
			websearch_to_tsquery('arabic', ar_light($1)), `+opts+`), h.tier, h.ph, h.rk, h.d
		FROM (SELECT r.id, r.title, coalesce(r.court, '') AS court, r.decided_on, r.text_ar, c.pg, c.tier, c.ph, c.rk,
				coalesce(r.decided_on, date '0001-01-01') AS d
			FROM (`+candidates+`) c JOIN ruling r ON r.id = c.id`+keyset+`
			ORDER BY c.tier DESC, c.ph DESC, c.rk DESC, coalesce(r.decided_on, date '0001-01-01') DESC, r.id
			LIMIT $2) h
		ORDER BY h.tier DESC, h.ph DESC, h.rk DESC, h.d DESC, h.id`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (rulingHit, error) {
		var h rulingHit
		var snip *string
		err := row.Scan(&h.ID, &h.Title, &h.Court, &h.On, &h.Page, &snip, &h.key.tier, &h.key.ph, &h.key.rk, &h.key.on)
		if snip != nil {
			h.Snippet = *snip
		}
		h.key.id = h.ID
		return h, err
	})
}
