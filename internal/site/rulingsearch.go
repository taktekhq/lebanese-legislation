package site

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const rulingSummaryWhere = `(search_exact @@ websearch_to_tsquery('simple', ar_norm($1))
	OR search_stem @@ websearch_to_tsquery('arabic', ar_light($1)))`

const judgmentQuery = `websearch_to_tsquery('arabic', ar_light(ar_digits($1)))`

const judgmentCandidates = 3000

type rulingHit struct {
	ID      int
	Title   string
	Court   string
	On      *time.Time
	Snippet string
	Page    int
}

func (s *Server) searchRulings(ctx context.Context, q string, limit, offset int) (int, []rulingHit, error) {
	var sum int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM ruling WHERE `+rulingSummaryWhere+` LIMIT 1000) t`, q).Scan(&sum); err != nil {
		return 0, nil, err
	}
	total, withJudgments := sum, false
	if sum < 1000 {

		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM (
			(SELECT id FROM ruling WHERE `+rulingSummaryWhere+` LIMIT 1000)
			UNION (SELECT ruling_id FROM ruling_page WHERE search_stem @@ `+judgmentQuery+` LIMIT 1000)) t`, q).Scan(&total); err != nil {
			return 0, nil, err
		}
		total = min(total, 1000)
		withJudgments = total > sum && sum < offset+limit
	}
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
	rows, err := s.db.Query(ctx, `SELECT h.id, h.title, h.court, h.decided_on, h.pg,
		ts_headline('arabic', CASE WHEN h.pg = 0 THEN coalesce(h.text_ar, '')
			ELSE (SELECT text_ar FROM ruling_page WHERE ruling_id = h.id AND n = h.pg) END,
			websearch_to_tsquery('arabic', ar_light($1)), `+opts+`)
		FROM (SELECT r.id, r.title, coalesce(r.court, '') AS court, r.decided_on, r.text_ar, c.pg, c.tier, c.ph, c.rk
			FROM (`+candidates+`) c JOIN ruling r ON r.id = c.id
			ORDER BY c.tier DESC, c.ph DESC, c.rk DESC, r.decided_on DESC NULLS LAST, r.id
			LIMIT $2 OFFSET $3) h
		ORDER BY h.tier DESC, h.ph DESC, h.rk DESC, h.decided_on DESC NULLS LAST, h.id`, q, limit, offset)
	if err != nil {
		return 0, nil, err
	}
	hits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rulingHit, error) {
		var h rulingHit
		var snip *string
		err := row.Scan(&h.ID, &h.Title, &h.Court, &h.On, &h.Page, &snip)
		if snip != nil {
			h.Snippet = *snip
		}
		return h, err
	})
	return total, hits, err
}
