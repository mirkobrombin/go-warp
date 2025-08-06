package validator

import (
	"context"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
)

func TestValidatorAutoHeal(t *testing.T) {
	ctx := context.Background()
	c := cache.NewInMemory[string]()
	s := adapter.NewInMemoryStore[string]()
	_ = s.Set(ctx, "k", "v1")
	if err := c.Set(ctx, "k", "v0", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v := New[string](c, s, ModeAutoHeal, time.Millisecond)
	go v.Run(ctx)
	time.Sleep(5 * time.Millisecond)
	if val, ok, err := c.Get(ctx, "k"); err != nil || !ok || val != "v1" {
		t.Fatalf("expected cache healed to v1, got %v err %v", val, err)
	}
	if m := v.Metrics(); m == 0 {
		t.Fatalf("expected mismatch metrics > 0")
	}
}

type mockStore struct{ getCalled bool }

func (m *mockStore) Get(ctx context.Context, key string) (string, bool, error) {
	m.getCalled = true
	return "", false, nil
}

func (m *mockStore) Set(ctx context.Context, key string, value string) error {
	return nil
}

func (m *mockStore) Keys(ctx context.Context) ([]string, error) {
	return []string{"k"}, nil
}

func TestValidatorScanCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := cache.NewInMemory[string]()
	s := &mockStore{}
	v := New[string](c, s, ModeNoop, time.Millisecond)
	v.scan(ctx)
	if s.getCalled {
		t.Fatalf("expected early exit on canceled context")
	}
}
