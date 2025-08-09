package adapter_test

import (
	"context"
	"testing"

	"github.com/mirkobrombin/go-warp/v1/adapter"
)

func TestInMemoryStoreGetSetKeys(t *testing.T) {
	s := adapter.NewInMemoryStore[string]()
	ctx := context.Background()
	if _, ok, err := s.Get(ctx, "foo"); err != nil || ok {
		t.Fatalf("Get: expected not found, got ok=%v err=%v", ok, err)
	}
	if err := s.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok, err := s.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v ok=%v err=%v", v, ok, err)
	}
	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "foo" {
		t.Fatalf("Keys: expected [foo], got %v", keys)
	}
}

func TestInMemoryStoreBatchCommitDelete(t *testing.T) {
	s := adapter.NewInMemoryStore[string]()
	ctx := context.Background()
	if err := s.Set(ctx, "remove", "me"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	b, err := s.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if err := b.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Batch Set: %v", err)
	}
	if err := b.Delete(ctx, "remove"); err != nil {
		t.Fatalf("Batch Delete: %v", err)
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, ok, _ := s.Get(ctx, "remove"); ok {
		t.Fatalf("Delete: key still present")
	}
	if v, ok, err := s.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v ok=%v err=%v", v, ok, err)
	}
	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "foo" {
		t.Fatalf("Keys: expected [foo], got %v", keys)
	}
}
