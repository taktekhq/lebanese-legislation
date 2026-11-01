package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrBudget is wrapped by every budget refusal.
var ErrBudget = errors.New("embedding budget")

func budgetErr(msg string) error { return fmt.Errorf("%w: %s", ErrBudget, msg) }

// Counter counts and embeds documents: a Gemini client, or a fake in tests.
type Counter interface {
	CountTokens(ctx context.Context, texts []string) (int64, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// OverheadPerDocument is reserved on top of the counted tokens of each text.
const OverheadPerDocument = 16

type ledger struct {
	FormatVersion       int    `json:"format_version"`
	Model               string `json:"model"`
	MaxTokens           int64  `json:"max_tokens"`
	OverheadPerDocument int    `json:"overhead_per_document"`
	ReservedTokens      int64  `json:"reserved_tokens"`
	AttemptedDocuments  int64  `json:"attempted_documents"`
	AttemptedBatches    int64  `json:"attempted_batches"`
}

// reserve adds tokens to the ledger at path, under an exclusive lock, and
// fails without writing when that would pass maxTokens. Reservations are
// never given back: a failed or interrupted request stays counted.
func reserve(path string, maxTokens, tokens int64, documents int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	state := ledger{FormatVersion: 1, Model: Model, MaxTokens: maxTokens, OverheadPerDocument: OverheadPerDocument}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return budgetErr("invalid embedding budget ledger")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var got map[string]any
		if json.Unmarshal(raw, &got) != nil || json.Unmarshal(raw, &state) != nil {
			return budgetErr("invalid embedding budget ledger")
		}
		if state.FormatVersion != 1 || state.Model != Model || state.MaxTokens != maxTokens || state.OverheadPerDocument != OverheadPerDocument {
			return budgetErr("embedding budget identity mismatch")
		}
		for _, k := range []string{"reserved_tokens", "attempted_documents", "attempted_batches"} {
			if n, ok := got[k].(float64); !ok || n < 0 || n != float64(int64(n)) {
				return budgetErr("invalid embedding budget ledger")
			}
		}
	}
	if state.ReservedTokens+tokens > maxTokens {
		return budgetErr("embedding input token budget exhausted")
	}
	state.ReservedTokens += tokens
	state.AttemptedDocuments += int64(documents)
	state.AttemptedBatches++
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Budgeted counts each batch's input tokens and reserves them, plus 16 per
// document, in a durable ledger before the paid request. It is a local
// spending guard, not an invoice: keep the ledger when resuming a build.
type Budgeted struct {
	Provider  Counter
	MaxTokens int64
	Ledger    string
}

// NewBudgeted wraps provider with a total input token budget kept at ledger.
func NewBudgeted(provider Counter, maxTokens int64, ledgerPath string) (*Budgeted, error) {
	if maxTokens <= 0 {
		return nil, budgetErr("a positive input token budget is required")
	}
	return &Budgeted{Provider: provider, MaxTokens: maxTokens, Ledger: ledgerPath}, nil
}

func (b *Budgeted) CountTokens(ctx context.Context, texts []string) (int64, error) {
	return b.Provider.CountTokens(ctx, texts)
}

func (b *Budgeted) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, budgetErr("offline budget accepts document batches only")
	}
	n, err := b.Provider.CountTokens(ctx, texts)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, budgetErr("invalid input token count")
	}
	if err := reserve(b.Ledger, b.MaxTokens, n+OverheadPerDocument*int64(len(texts)), len(texts)); err != nil {
		return nil, err
	}
	return b.Provider.Embed(ctx, texts)
}

// Daily shares one token budget per calendar day (in Zone) across builds
// and restarts: a ledger per day in Dir. The cap in tokens is the daily
// dollars divided by the price per million input tokens.
type Daily struct {
	Provider  Counter
	Dir       string
	MaxTokens int64
	Zone      *time.Location
	Now       func() time.Time
}

// NewDaily checks the dollar cap, price and time zone. Prices are exact
// decimals, such as "1" and "0.15".
func NewDaily(provider Counter, dir, dailyUSD, usdPerMillion, zone string) (*Daily, error) {
	limit, ok1 := new(big.Rat).SetString(dailyUSD)
	price, ok2 := new(big.Rat).SetString(usdPerMillion)
	if !ok1 || !ok2 || limit.Sign() <= 0 || price.Sign() <= 0 {
		return nil, budgetErr("invalid daily embedding budget")
	}
	tokens := new(big.Rat).Quo(new(big.Rat).Mul(limit, big.NewRat(1000000, 1)), price)
	max := new(big.Int).Quo(tokens.Num(), tokens.Denom())
	if !max.IsInt64() || max.Int64() <= 0 {
		return nil, budgetErr("invalid daily embedding budget")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, budgetErr("invalid daily embedding budget")
	}
	return &Daily{Provider: provider, Dir: dir, MaxTokens: max.Int64(), Zone: loc, Now: time.Now}, nil
}

func (d *Daily) CountTokens(ctx context.Context, texts []string) (int64, error) {
	return d.Provider.CountTokens(ctx, texts)
}

func (d *Daily) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, budgetErr("offline budget accepts document batches only")
	}
	n, err := d.Provider.CountTokens(ctx, texts)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, budgetErr("invalid input token count")
	}
	// The day is read after the count, so a batch counted just before
	// midnight is charged to the day it is sent on.
	day := d.Now().In(d.Zone).Format("2006-01-02")
	if err := reserve(filepath.Join(d.Dir, day+".json"), d.MaxTokens, n+OverheadPerDocument*int64(len(texts)), len(texts)); err != nil {
		return nil, err
	}
	return d.Provider.Embed(ctx, texts)
}
