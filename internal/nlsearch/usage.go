package nlsearch

import (
	"context"
	"sync"
)

// Usage counts the model calls made for one request and their tokens, so
// the caller can report what a question cost. Cached answers add nothing.
type Usage struct {
	mu                      sync.Mutex
	Calls, In, Out          int64
	EmbedCalls, EmbedTokens int64 // query embeddings for the vector search (tokens estimated)
}

type usageKey struct{}

// WithUsage returns a context that collects the usage of calls made with it.
func WithUsage(ctx context.Context) (context.Context, *Usage) {
	u := &Usage{}
	return context.WithValue(ctx, usageKey{}, u), u
}

// AddUsage records one model call on ctx's Usage, if it has one.
func AddUsage(ctx context.Context, in, out int64) {
	if u, ok := ctx.Value(usageKey{}).(*Usage); ok {
		u.mu.Lock()
		u.Calls++
		u.In += in
		u.Out += out
		u.mu.Unlock()
	}
}

// AddEmbedding records one query embedding of about tokens tokens.
func AddEmbedding(ctx context.Context, tokens int64) {
	if u, ok := ctx.Value(usageKey{}).(*Usage); ok {
		u.mu.Lock()
		u.EmbedCalls++
		u.EmbedTokens += tokens
		u.mu.Unlock()
	}
}

// Embeddings reads the embedding counts.
func (u *Usage) Embeddings() (calls, tokens int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.EmbedCalls, u.EmbedTokens
}

// Totals reads the counts.
func (u *Usage) Totals() (calls, in, out int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.Calls, u.In, u.Out
}
