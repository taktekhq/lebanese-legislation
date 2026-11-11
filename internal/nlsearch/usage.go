package nlsearch

import (
	"context"
	"sync"
	"time"
)

// Usage counts the model calls made for one request and their tokens, so
// the caller can report what a question cost. Cached answers add nothing.
type Usage struct {
	mu                      sync.Mutex
	Calls, In, Out          int64
	EmbedCalls, EmbedTokens int64 // query embeddings for the vector search (tokens estimated)
	gens                    []Generation
}

// Generation is one request to a model, answered or failed: which model,
// its tokens, how long it took and how it ended. It holds no text, neither
// the question nor what the model wrote. Calls, In and Out above count only
// the calls that completed; a Generation is kept for a failed one too.
type Generation struct {
	Kind            string // what the call was for: rewrite, expand or compose
	Provider, Model string
	In, Out         int64 // tokens, 0 when the provider reported none
	Latency         time.Duration
	Status          int  // the provider's HTTP status, 0 when no response came
	Failed          bool // no response, not 200, or an answer that did not complete
}

// generation times one model call. Start it before the request, set Status
// and the tokens as they become known, call ok when the answer completed,
// and defer end.
type generation struct {
	Generation
	ctx   context.Context
	start time.Time
}

func startGeneration(ctx context.Context, kind, provider, model string) *generation {
	return &generation{Generation: Generation{Kind: kind, Provider: provider, Model: model, Failed: true}, ctx: ctx, start: time.Now()}
}

func (g *generation) ok(in, out int64) { g.In, g.Out, g.Failed = in, out, false }

func (g *generation) end() {
	if u, has := g.ctx.Value(usageKey{}).(*Usage); has {
		g.Latency = time.Since(g.start)
		u.mu.Lock()
		u.gens = append(u.gens, g.Generation)
		u.mu.Unlock()
	}
}

// AddGeneration records one model call on ctx's Usage, if it has one.
func AddGeneration(ctx context.Context, g Generation) {
	if u, has := ctx.Value(usageKey{}).(*Usage); has {
		u.mu.Lock()
		u.gens = append(u.gens, g)
		u.mu.Unlock()
	}
}

// Generations lists the model calls made, in the order they ended.
func (u *Usage) Generations() []Generation {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Generation(nil), u.gens...)
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
