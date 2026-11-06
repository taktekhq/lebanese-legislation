package mcpserver

import (
	"context"

	"github.com/taktekhq/lebanese-legislation/internal/memo"
)

// count is a paged tool's total, kept by s.counts (internal/memo): the same
// on every page of a walk, and with cursor pages the count(*) was most of a
// deep page's cost (about 80 ms for list_legislation).
func (s *Server) count(ctx context.Context, sql string, args ...any) (int, error) {
	k := memo.Key(sql, args...)
	if v, ok := s.counts.Get(k); ok {
		return v.(int), nil
	}
	var n int
	if err := s.db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	s.counts.Put(k, n)
	return n, nil
}
