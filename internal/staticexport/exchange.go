package staticexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PathExchange exchanges crawl discoveries through R2. prefix must identify a
// fresh coordinated run shared by all shards, never a previous attempt. Each
// shard writes one immutable batch per round. Waiting for all batches gives
// global termination detection without timing guesses or rendering other owners'
// pages. timeout bounds each round's wait after publishing. R2 provides strongly
// consistent reads of these coordination objects.
func (r *R2) PathExchange(prefix string, shard, shards int, timeout time.Duration) func(context.Context, int, []string) ([]string, bool, error) {
	return func(ctx context.Context, round int, paths []string) ([]string, bool, error) {
		key := func(i int) string {
			return fmt.Sprintf("%s/round-%d/shard-%d.json", strings.TrimRight(prefix, "/"), round, i)
		}
		b, err := json.Marshal(paths)
		if err != nil {
			return nil, false, err
		}
		if err := r.Put(ctx, key(shard), b, PutOptions{ContentType: "application/json"}); err != nil {
			return nil, false, err
		}
		// Bound only the coordination wait, never the rendering context.
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		pending := make([]int, shards)
		for i := range pending {
			pending[i] = i
		}
		waitError := func(err error) error {
			return fmt.Errorf("path exchange round %d: missing shards: %v: %w", round, pending, err)
		}
		var incoming []string
		active := false
		for len(pending) > 0 {
			for n := 0; n < len(pending); {
				i := pending[n]
				b, err := r.Get(ctx, key(i))
				if errors.Is(err, ErrNotFound) {
					n++
					continue
				}
				if err != nil {
					if ctx.Err() != nil {
						return nil, false, waitError(ctx.Err())
					}
					return nil, false, err
				}
				var batch []string
				if err := json.Unmarshal(b, &batch); err != nil {
					return nil, false, fmt.Errorf("discovery %s: %w", key(i), err)
				}
				pending = append(pending[:n], pending[n+1:]...)
				active = active || len(batch) > 0
				for _, p := range batch {
					if Owner(p, shards) == shard {
						incoming = append(incoming, p)
					}
				}
			}
			if len(pending) == 0 {
				break
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, false, waitError(ctx.Err())
			case <-timer.C:
			}
		}
		return incoming, active, nil
	}
}
