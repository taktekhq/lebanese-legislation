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
// pages. R2 provides strongly consistent reads of these coordination objects.
func (r *R2) PathExchange(prefix string, shard, shards int) func(context.Context, int, []string) ([]string, bool, error) {
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
		var incoming []string
		active := false
		for i := 0; i < shards; i++ {
			var batch []string
			for {
				b, err := r.Get(ctx, key(i))
				if err == nil {
					if err := json.Unmarshal(b, &batch); err != nil {
						return nil, false, fmt.Errorf("discovery %s: %w", key(i), err)
					}
					break
				}
				if !errors.Is(err, ErrNotFound) {
					return nil, false, err
				}
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, false, ctx.Err()
				case <-timer.C:
				}
			}
			active = active || len(batch) > 0
			for _, p := range batch {
				if Owner(p, shards) == shard {
					incoming = append(incoming, p)
				}
			}
		}
		return incoming, active, nil
	}
}
