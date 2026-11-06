package mcpserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// An expired value is answered at once while it is read again in the
// background; a failed reload keeps the old value.
func TestFreshReloadsInTheBackground(t *testing.T) {
	saved := dataTTL
	dataTTL = 20 * time.Millisecond
	t.Cleanup(func() { dataTTL = saved })

	var f fresh[int]
	var loads atomic.Int32
	release := make(chan struct{})
	load := func(context.Context) (int, error) {
		n := loads.Add(1)
		if n == 2 {
			<-release // the background reload waits until the test lets it go
		}
		if n == 3 {
			return 0, errors.New("database away")
		}
		return int(n) * 10, nil
	}
	ctx := context.Background()
	if v, err := f.get(ctx, load); err != nil || v != 10 {
		t.Fatalf("first read: %v %v", v, err)
	}
	if v, _ := f.get(ctx, load); v != 10 || loads.Load() != 1 {
		t.Fatalf("a fresh value was read again: %v, %d loads", v, loads.Load())
	}
	time.Sleep(30 * time.Millisecond)
	start := time.Now()
	if v, _ := f.get(ctx, load); v != 10 {
		t.Fatalf("expired: got %v, want the old value", v)
	}
	if time.Since(start) > 10*time.Millisecond {
		t.Fatal("a request waited for the reload")
	}
	f.get(ctx, load) // a reload is already running: no second one
	close(release)
	waitFor(t, func() bool { v, _ := f.peek(); return v == 20 })
	if loads.Load() != 2 {
		t.Fatalf("%d loads, want 2", loads.Load())
	}
	time.Sleep(30 * time.Millisecond)
	f.get(ctx, load) // starts the reload that fails
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return !f.loading && loads.Load() == 3 })
	if v, _ := f.peek(); v != 20 {
		t.Fatalf("a failed reload replaced the value: %v", v)
	}
}

func TestFreshFirstReadError(t *testing.T) {
	var f fresh[string]
	if _, err := f.get(context.Background(), func(context.Context) (string, error) { return "", errors.New("down") }); err == nil {
		t.Fatal("no error")
	}
	if v, err := f.get(context.Background(), func(context.Context) (string, error) { return "up", nil }); err != nil || v != "up" {
		t.Fatalf("after a failed first read: %q %v", v, err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}
