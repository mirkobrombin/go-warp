package adapter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newGormStore[T any](t *testing.T) (*adapter.GormStore[T], *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to connect database: %v", err)
	}

	_ = db.Migrator().DropTable("warp_kv_store")

	return adapter.NewGormStore[T](db), db
}

func TestGormStoreGetSetKeys(t *testing.T) {
	s, _ := newGormStore[string](t)
	ctx := context.Background()

	if err := s.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, ok, err := s.Get(ctx, "foo"); err != nil || !ok || v != "bar" {
		t.Fatalf("Get: expected bar, got %v err %v", v, err)
	}
	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "foo" {
		t.Fatalf("Keys: expected [foo], got %v", keys)
	}
}

func TestGormStorePersistenceAndWarmup(t *testing.T) {
	s, _ := newGormStore[string](t)
	ctx := context.Background()

	// warp1 writes a value which should persist
	c1 := cache.NewInMemory[merge.Value[string]]()
	w1 := core.New[string](c1, s, nil, merge.NewEngine[string]())
	w1.Register("foo", core.ModeStrongLocal, time.Minute)
	if err := w1.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// warp2 uses a fresh cache but same store; warmup should load persisted value
	c2 := cache.NewInMemory[merge.Value[string]]()
	w2 := core.New[string](c2, s, nil, merge.NewEngine[string]())
	w2.Register("foo", core.ModeStrongLocal, time.Minute)
	w2.Warmup(ctx)

	if v, err := w2.Get(ctx, "foo"); err != nil || v != "bar" {
		t.Fatalf("Warmup/Get: expected bar, got %v err %v", v, err)
	}
}

func TestGormStoreBatch(t *testing.T) {
	s, _ := newGormStore[string](t)
	ctx := context.Background()

	b, err := s.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}

	if err := b.Set(ctx, "k1", "v1"); err != nil {
		t.Errorf("Batch Set: %v", err)
	}
	if err := b.Set(ctx, "k2", "v2"); err != nil {
		t.Errorf("Batch Set: %v", err)
	}

	// Pre-populate k3 to delete it
	s.Set(ctx, "k3", "v3")

	if err := b.Delete(ctx, "k3"); err != nil {
		t.Errorf("Batch Delete: %v", err)
	}

	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify
	v, ok, _ := s.Get(ctx, "k1")
	if !ok || v != "v1" {
		t.Errorf("k1 missing or wrong: %v", v)
	}
	v, ok, _ = s.Get(ctx, "k2")
	if !ok || v != "v2" {
		t.Errorf("k2 missing or wrong: %v", v)
	}
	_, ok, _ = s.Get(ctx, "k3")
	if ok {
		t.Errorf("k3 should be deleted")
	}
}

func TestGormStoreLargeBatch(t *testing.T) {
	s, _ := newGormStore[string](t)
	ctx := context.Background()

	b, err := s.Batch(ctx)
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}

	// 500 items to enable batching logic (chunk size 100)
	count := 500
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("k-%d", i)
		val := fmt.Sprintf("v-%d", i)
		if err := b.Set(ctx, key, val); err != nil {
			t.Errorf("Set %d: %v", i, err)
		}
	}

	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify count
	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != count {
		t.Errorf("Expected %d keys, got %d", count, len(keys))
	}

	// Verify random sample
	v, ok, _ := s.Get(ctx, "k-499")
	if !ok || v != "v-499" {
		t.Errorf("k-499 missing or wrong")
	}
}

func TestGormStoreLongKeys(t *testing.T) {
	s, _ := newGormStore[string](t)
	ctx := context.Background()

	// 1000 chars key
	longKey := strings.Repeat("a", 1000)
	val := "long-key-val"

	if err := s.Set(ctx, longKey, val); err != nil {
		t.Fatalf("Set: %v", err)
	}

	v, ok, err := s.Get(ctx, longKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatalf("Get: not found")
	}
	if v != val {
		t.Fatalf("Get: expected %v, got %v", val, v)
	}
}

func TestGormStoreWithTableName(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect database: %v", err)
	}

	s := adapter.NewGormStore[string](db, adapter.WithGormTableName("custom_kv"))
	ctx := context.Background()

	if err := s.Set(ctx, "foo", "bar"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify table exists
	if !db.Migrator().HasTable("custom_kv") {
		t.Fatal("custom_kv table does not exist")
	}
}
