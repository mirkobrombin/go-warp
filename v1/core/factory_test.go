package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
)

func TestGetOrSet_Hit(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	w := New[string](c, nil, nil, nil)
	w.Register("gos-key-1", ModeStrongLocal, 10*time.Minute)

	// Pre-populate
	w.Set(context.Background(), "gos-key-1", "cached-val")

	loaderCalled := false
	loader := func(ctx context.Context) (string, error) {
		loaderCalled = true
		return "loaded-val", nil
	}

	val, err := w.GetOrSet(context.Background(), "gos-key-1", loader)
	if err != nil {
		t.Fatalf("Expected success, got: %v", err)
	}
	if val != "cached-val" {
		t.Errorf("Expected 'cached-val', got '%v'", val)
	}
	if loaderCalled {
		t.Error("Loader should not be called on hit")
	}
}

func TestGetOrSet_Miss(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	w := New[string](c, nil, nil, nil)
	w.Register("gos-key-2", ModeStrongLocal, 10*time.Minute)

	loader := func(ctx context.Context) (string, error) {
		return "loaded-val", nil
	}

	val, err := w.GetOrSet(context.Background(), "gos-key-2", loader)
	if err != nil {
		t.Fatalf("Expected success, got: %v", err)
	}
	if val != "loaded-val" {
		t.Errorf("Expected 'loaded-val', got '%v'", val)
	}

	// Verify it's now in cache
	cached, err := w.Get(context.Background(), "gos-key-2")
	if err != nil || cached != "loaded-val" {
		t.Errorf("Value was not cached correctly. Got: %v, Err: %v", cached, err)
	}
}

func TestGetOrSet_Singleflight(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	w := New[string](c, nil, nil, nil)
	w.Register("gos-key-3", ModeStrongLocal, 10*time.Minute)

	var calls atomic.Int32
	ready := make(chan struct{})
	block := make(chan struct{})

	var once sync.Once
	loader := func(ctx context.Context) (string, error) {
		calls.Add(1)
		once.Do(func() {
			close(ready) // Signal we started
		})
		<-block // Wait to finish
		return "concurrent-val", nil
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Routine 1
	go func() {
		defer wg.Done()
		w.GetOrSet(context.Background(), "gos-key-3", loader)
	}()

	// Wait for Routine 1 to start loader
	<-ready

	// Routine 2 (Should join the flight)
	go func() {
		defer wg.Done()
		w.GetOrSet(context.Background(), "gos-key-3", loader)
	}()

	// Let them finish
	close(block)
	wg.Wait()

	if calls.Load() != 1 {
		t.Errorf("Expected 1 loader call, got %d", calls.Load())
	}
}

func TestGetOrSet_FailSafe(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	w := New[string](c, nil, nil, nil)

	key := "gos-key-fs"
	// Register with FailSafe
	w.Register(key, ModeStrongLocal, 10*time.Millisecond, cache.WithFailSafe(1*time.Hour))

	// Populate
	w.Set(context.Background(), key, "stale-val")

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Loader fails
	loader := func(ctx context.Context) (string, error) {
		return "", errors.New("loader boom")
	}

	// Should return stale value
	val, err := w.GetOrSet(context.Background(), key, loader)
	if err != nil {
		t.Fatalf("Expected success (FailSafe), got error: %v", err)
	}
	if val != "stale-val" {
		t.Errorf("Expected 'stale-val', got '%v'", val)
	}
}

func TestGetOrSet_LoaderError(t *testing.T) {
	c := cache.NewInMemory[merge.Value[string]](cache.WithSweepInterval[merge.Value[string]](10 * time.Millisecond))
	w := New[string](c, nil, nil, nil)
	w.Register("gos-key-err", ModeStrongLocal, 10*time.Minute)

	boom := errors.New("boom")
	loader := func(ctx context.Context) (string, error) {
		return "", boom
	}

	_, err := w.GetOrSet(context.Background(), "gos-key-err", loader)
	if !errors.Is(err, boom) {
		t.Errorf("Expected 'boom' error, got: %v", err)
	}
}
