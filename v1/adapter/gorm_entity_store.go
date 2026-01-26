package adapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormEntityStore implements Store by mapping keys directly to a GORM model's primary key.
// Unlike GormStore, which uses a KV table to store serialized blobs, GormEntityStore
// operates directly on the entity table defined by T.
//
// T should be a struct type that GORM recognizes as a model.
type GormEntityStore[T any] struct {
	db        *gorm.DB
	keyColumn string
	keyParser func(string) (any, error)
}

// EntityOption configures a GormEntityStore.
type EntityOption func(*entityStoreOptions)

type entityStoreOptions struct {
	keyColumn string
	keyParser func(string) (any, error)
}

// WithEntityKeyColumn sets the column name to use for lookups (default "id").
func WithEntityKeyColumn(col string) EntityOption {
	return func(o *entityStoreOptions) {
		o.keyColumn = col
	}
}

// WithEntityKeyParser sets a function to convert the string key to the DB key type.
// Default is to pass the string as-is.
// Useful for integer primary keys: use adapter.ParseIntKey
func WithEntityKeyParser(parser func(string) (any, error)) EntityOption {
	return func(o *entityStoreOptions) {
		o.keyParser = parser
	}
}

// ParseIntKey is a helper for WithEntityKeyParser to convert keys to integers.
func ParseIntKey(s string) (any, error) {
	return strconv.Atoi(s)
}

// NewGormEntityStore creates a new GormEntityStore.
// T is the model struct.
func NewGormEntityStore[T any](db *gorm.DB, opts ...EntityOption) *GormEntityStore[T] {
	o := entityStoreOptions{
		keyColumn: "id",
		keyParser: func(s string) (any, error) { return s, nil },
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &GormEntityStore[T]{
		db:        db,
		keyColumn: o.keyColumn,
		keyParser: o.keyParser,
	}
}

// Get implements Store.Get using GORM First.
func (s *GormEntityStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	pk, err := s.keyParser(key)
	if err != nil {
		return zero, false, fmt.Errorf("invalid key format: %w", err)
	}

	var val T
	q := fmt.Sprintf("%s = ?", s.keyColumn)
	err = s.db.WithContext(ctx).First(&val, q, pk).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return zero, false, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}

	return val, true, nil
}

// Set implements Store.Set using GORM Save (Upsert).
// Note: This assumes 'value' has the Primary Key populated correctly if it matches 'key'.
func (s *GormEntityStore[T]) Set(ctx context.Context, key string, value T) error {
	pk, err := s.keyParser(key)
	if err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	if err := s.db.WithContext(ctx).Save(&value).Error; err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	_ = pk // unused here, but parsed to validate format
	return nil
}

// Keys implements Store.Keys by plucking the ID column.
func (s *GormEntityStore[T]) Keys(ctx context.Context) ([]string, error) {
	var keys []string
	var vals []interface{} // We don't know the type of the key

	// gorm.Model(new(T)) allows query on table of T
	var zero T
	db := s.db.WithContext(ctx).Model(&zero)

	if err := db.Pluck(s.keyColumn, &vals).Error; err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}

	keys = make([]string, len(vals))
	for i, v := range vals {
		keys[i] = fmt.Sprint(v)
	}
	return keys, nil
}

// Batch implements Batcher.Batch.
func (s *GormEntityStore[T]) Batch(ctx context.Context) (Batch[T], error) {
	return &gormEntityBatch[T]{s: s, sets: make([]T, 0), deletes: make([]any, 0)}, nil
}

type gormEntityBatch[T any] struct {
	s       *GormEntityStore[T]
	sets    []T
	deletes []any
}

func (b *gormEntityBatch[T]) Set(ctx context.Context, key string, value T) error {
	_, err := b.s.keyParser(key)
	if err != nil {
		return err
	}
	b.sets = append(b.sets, value)
	return nil
}

func (b *gormEntityBatch[T]) Delete(ctx context.Context, key string) error {
	pk, err := b.s.keyParser(key)
	if err != nil {
		return err
	}
	b.deletes = append(b.deletes, pk)
	return nil
}

func (b *gormEntityBatch[T]) Commit(ctx context.Context) error {
	return b.s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(b.deletes) > 0 {
			var zero T
			q := fmt.Sprintf("%s IN ?", b.s.keyColumn)
			if err := tx.Delete(&zero, q, b.deletes).Error; err != nil {
				return err
			}
		}
		if len(b.sets) > 0 {
			// Save in batches
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(b.sets, 100).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
