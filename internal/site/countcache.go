package site

import (
	"context"

	"github.com/taktekhq/lebanese-legislation/internal/memo"
)

// countRows is SELECT count(*) ... for a list's filter, kept by s.counts
// (internal/memo): the same on every page of the list.
func (s *Server) countRows(ctx context.Context, sql string, args ...any) (int, error) {
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
