package mcpserver

import (
	"context"
	"time"

	"golang.org/x/sync/semaphore"
)

// Tool calls share the database pool with the website's pages and API. A
// cap on the units of DB work in flight keeps a burst of calls from taking
// the whole pool: like the site's own /mcp and /api/v1, a call waits up to
// callWait for room and is otherwise answered 503.
const (
	defaultCallUnits = 6               // one search over every kind and two other calls; 2 of the 8 pool connections stay for pages
	callWait         = 2 * time.Second // how long a call queues for room
	// searchFanOut is the weight of a search over every kind: four ranked
	// queries, one per kind. They run one after another since 9 Nov 2026,
	// and the weight stays 4 so that two such searches do not run at once
	// on the one CPU.
	searchFanOut  = 4
	rpcServerBusy = -32000 // JSON-RPC server error: overloaded
)

// newCallSem is the cap for units (defaultCallUnits when 0, none when
// negative) and its size.
func newCallSem(units int) (*semaphore.Weighted, int) {
	if units < 0 {
		return nil, 0
	}
	if units == 0 {
		units = defaultCallUnits
	}
	return semaphore.NewWeighted(int64(units)), units
}

// callWeight is how many queries a tools/call may run at once:
// search_legislation runs one per kind searched, every other tool one.
func callWeight(p map[string]any, capacity int) int64 {
	n := 1
	if jsString(p["name"]) == "search_legislation" {
		a, _ := p["arguments"].(map[string]any)
		if k, isStr := a["kind"].(string); !isStr || k == "" {
			n = searchFanOut
		}
	}
	return int64(min(n, capacity))
}

// admitCall reserves room for one tools/call. ok is false when none opened
// up within callWait; release must be called once otherwise.
func (s *Server) admitCall(ctx context.Context, p map[string]any) (release func(), ok bool) {
	if s.calls == nil {
		return func() {}, true
	}
	w := callWeight(p, s.callUnits)
	ctx, cancel := context.WithTimeout(ctx, callWait)
	defer cancel()
	if s.calls.Acquire(ctx, w) != nil {
		return nil, false
	}
	return func() { s.calls.Release(w) }, true
}
