package adapter_test

import (
	"context"
	"testing"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type TestUser struct {
	ID   int `gorm:"primaryKey"`
	Name string
}

func newGormEntityStore(t *testing.T) (*adapter.GormEntityStore[TestUser], *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to connect database: %v", err)
	}

	_ = db.AutoMigrate(&TestUser{})

	// Use Int key parser because TestUser ID is int
	store := adapter.NewGormEntityStore[TestUser](db, adapter.WithEntityKeyParser(adapter.ParseIntKey))
	return store, db
}

func TestGormEntityStoreGetSet(t *testing.T) {
	s, _ := newGormEntityStore(t)
	ctx := context.Background()

	// Set
	user := TestUser{ID: 1, Name: "Alice"}
	if err := s.Set(ctx, "1", user); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get
	v, ok, err := s.Get(ctx, "1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: expected found")
	}
	if v.Name != "Alice" {
		t.Fatalf("Get: expected Alice, got %v", v.Name)
	}

	// Get not found
	_, ok, err = s.Get(ctx, "999")
	if err != nil {
		t.Fatalf("Get 999: %v", err)
	}
	if ok {
		t.Fatal("Get 999: expected not found")
	}
}

func TestGormEntityStoreKeys(t *testing.T) {
	s, _ := newGormEntityStore(t)
	ctx := context.Background()

	s.Set(ctx, "1", TestUser{ID: 1, Name: "A"})
	s.Set(ctx, "2", TestUser{ID: 2, Name: "B"})

	keys, err := s.Keys(ctx)
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	// Order is not guaranteed, but usually sorted by PK
	if len(keys) != 2 {
		t.Fatalf("Keys: expected 2, got %d", len(keys))
	}
}

func TestGormEntityStoreBatch(t *testing.T) {
	s, db := newGormEntityStore(t)
	ctx := context.Background()

	b, _ := s.Batch(ctx)
	b.Set(ctx, "1", TestUser{ID: 1, Name: "Batch1"})
	b.Set(ctx, "2", TestUser{ID: 2, Name: "Batch2"})

	// Pre-exist for delete
	db.Create(&TestUser{ID: 3, Name: "DeleteMe"})

	b.Delete(ctx, "3")

	if err := b.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify
	var count int64
	db.Model(&TestUser{}).Count(&count)
	if count != 2 {
		t.Fatalf("Expected 2 users, got %d", count)
	}

	v, ok, _ := s.Get(ctx, "1")
	if !ok || v.Name != "Batch1" {
		t.Fatal("Batch1 failed")
	}
	v, ok, _ = s.Get(ctx, "3")
	if ok {
		t.Fatal("Batch delete failed")
	}
}

// Test with String PK
type StringUser struct {
	Email string `gorm:"primaryKey"`
	Name  string
}

func TestGormEntityStoreStringKey(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	db.AutoMigrate(&StringUser{})

	s := adapter.NewGormEntityStore[StringUser](db, adapter.WithEntityKeyColumn("email"))
	ctx := context.Background()

	s.Set(ctx, "foo@bar.com", StringUser{Email: "foo@bar.com", Name: "Foo"})

	v, ok, _ := s.Get(ctx, "foo@bar.com")
	if !ok || v.Name != "Foo" {
		t.Fatal("Get failed")
	}
}
