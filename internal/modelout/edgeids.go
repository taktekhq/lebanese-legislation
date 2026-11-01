package modelout

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Edge ids stay the same from one build to the next. The graph is worked out
// again on every build, and its ids used to be the edges' positions, so one
// edge more or less renumbered every edge after it: the 1 Nov 2026 export
// rewrote 344,427 lines of graph_edge for a single removed edge. Now an edge
// that is still there keeps last build's id, a new edge takes the next id
// after the highest ever given, and a removed edge leaves a gap.
//
// An edge is the same edge when everything that says what it links is the
// same (edgeIdentity); how it was matched, the quote and the model may change
// under the same id.

// edgeIdentity is what makes two edges the same edge.
func edgeIdentity(e edge) string {
	return strings.Join([]string{e.fromKey, e.toKey, e.relation, e.source, e.status, e.fromType,
		strconv.Itoa(e.fromID), e.fromArticle, strconv.Itoa(e.toLaw), e.toArticle, e.toCite, e.part}, "\x1f")
}

// PriorEdgeIDs is the ids the database's graph_edge gave each edge, by
// identity and in id order, for Graph to keep. Its zero value is no ids.
type PriorEdgeIDs struct{ byIdentity map[string][]int }

// ReadPriorEdgeIDs reads them. No graph_edge (a first build) is no ids.
func ReadPriorEdgeIDs(ctx context.Context, conn *pgx.Conn) (PriorEdgeIDs, error) {
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.graph_edge') IS NOT NULL`).Scan(&ok); err != nil || !ok {
		return PriorEdgeIDs{}, err
	}
	rows, err := conn.Query(ctx, `SELECT id, from_key, coalesce(to_key, ''), relation, source, status, from_type,
		from_id, coalesce(from_article, ''), coalesce(to_law_id, 0), coalesce(to_article, ''),
		coalesce(to_cite, ''), coalesce(part, '') FROM graph_edge ORDER BY id`)
	if err != nil {
		return PriorEdgeIDs{}, err
	}
	defer rows.Close()
	prior := map[string][]int{}
	for rows.Next() {
		var id int
		var e edge
		if err := rows.Scan(&id, &e.fromKey, &e.toKey, &e.relation, &e.source, &e.status, &e.fromType,
			&e.fromID, &e.fromArticle, &e.toLaw, &e.toArticle, &e.toCite, &e.part); err != nil {
			return PriorEdgeIDs{}, err
		}
		k := edgeIdentity(e)
		prior[k] = append(prior[k], id)
	}
	return PriorEdgeIDs{prior}, rows.Err()
}

// edgeIDs gives each edge, in order, its id: last build's for an edge that was
// there (the n-th of identical edges takes the n-th id), else the next id after
// the highest last build used. With no prior ids that is 1, 2, 3...
func edgeIDs(edges []edge, prior map[string][]int) []int {
	next := 1
	for _, ids := range prior {
		for _, id := range ids {
			if id >= next {
				next = id + 1
			}
		}
	}
	used := map[string]int{}
	out := make([]int, len(edges))
	for i, e := range edges {
		k := edgeIdentity(e)
		if n := used[k]; n < len(prior[k]) {
			out[i] = prior[k][n]
			used[k] = n + 1
			continue
		}
		out[i] = next
		next++
	}
	return out
}
