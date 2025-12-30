package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultGormTableName = "warp_kv_store"
	defaultGormOpTimeout = 5 * time.Second
)

// gormKV is the internal model used to store key-value pairs in the database.
type gormKV struct {
	Key   string `gorm:"primaryKey;column:key_id"`
	Value []byte `gorm:"column:value"`
}

// GormStore implements Store using a GORM backend.
type GormStore[T any] struct {
	db        *gorm.DB
	tableName string
	timeout   time.Duration
	codec     cache.Codec
}

// GormOption configures a GormStore.
type GormOption func(*gormStoreOptions)

type gormStoreOptions struct {
	tableName string
	timeout   time.Duration
	codec     cache.Codec
}

// WithGormTableName sets the table name for the GormStore.
func WithGormTableName(name string) GormOption {
	return func(o *gormStoreOptions) {
		o.tableName = name
	}
}

// WithGormTimeout sets the operation timeout for GORM calls.
func WithGormTimeout(d time.Duration) GormOption {
	return func(o *gormStoreOptions) {
		o.timeout = d
	}
}

// WithGormCodec sets the codec for serialization.
func WithGormCodec(c cache.Codec) GormOption {
	return func(o *gormStoreOptions) {
		o.codec = c
	}
}

// NewGormStore returns a new GormStore using the provided GORM DB connection.
func NewGormStore[T any](db *gorm.DB, opts ...GormOption) *GormStore[T] {
	o := gormStoreOptions{
		tableName: defaultGormTableName,
		timeout:   defaultGormOpTimeout,
		codec:     cache.GobCodec{},
	}
	for _, opt := range opts {
		opt(&o)
	}

	// Ensure the table exists
	if !db.Migrator().HasTable(o.tableName) {
		_ = db.Table(o.tableName).AutoMigrate(&gormKV{})
	}

	return &GormStore[T]{
		db:        db,
		tableName: o.tableName,
		timeout:   o.timeout,
		codec:     o.codec,
	}
}

// Get implements Store.Get.
func (s *GormStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var kv gormKV
	err := s.db.WithContext(cctx).Table(s.tableName).First(&kv, "key_id = ?", key).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return zero, false, nil
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}

	if err := cctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}

	var v T
	if err := s.codec.Unmarshal(kv.Value, &v); err != nil {
		return zero, false, err
	}

	return v, true, nil
}

// Set implements Store.Set.
func (s *GormStore[T]) Set(ctx context.Context, key string, value T) error {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}

	data, err := s.codec.Marshal(value)
	if err != nil {
		return err
	}

	kv := gormKV{
		Key:   key,
		Value: data,
	}

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := s.db.WithContext(cctx).Table(s.tableName).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&kv).Error; err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}

	return nil
}

// Keys implements Store.Keys.
func (s *GormStore[T]) Keys(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var keys []string
	err := s.db.WithContext(cctx).Table(s.tableName).Pluck("key_id", &keys).Error
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}
	return keys, nil
}

// Batch implements Batcher.Batch.
func (s *GormStore[T]) Batch(ctx context.Context) (Batch[T], error) {
	return &gormBatch[T]{s: s, sets: make(map[string]T)}, nil
}

type gormBatch[T any] struct {
	s       *GormStore[T]
	sets    map[string]T
	deletes []string
}

func (b *gormBatch[T]) Set(ctx context.Context, key string, value T) error {
	b.sets[key] = value
	return nil
}

func (b *gormBatch[T]) Delete(ctx context.Context, key string) error {
	b.deletes = append(b.deletes, key)
	return nil
}

func (b *gormBatch[T]) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, b.s.timeout)
	defer cancel()

	err := b.s.db.WithContext(cctx).Transaction(func(tx *gorm.DB) error {
		// Deletes
		if len(b.deletes) > 0 {
			if err := tx.Table(b.s.tableName).Delete(&gormKV{}, "key_id IN ?", b.deletes).Error; err != nil {
				return err
			}
		}

		// Sets
		if len(b.sets) > 0 {
			var kvs []gormKV
			kvs = make([]gormKV, 0, len(b.sets))
			for k, v := range b.sets {
				data, err := b.s.codec.Marshal(v)
				if err != nil {
					return err
				}
				kvs = append(kvs, gormKV{Key: k, Value: data})
			}

			// Perform bulk upsert in batches of 100 to avoid parameter limits
			if err := tx.Table(b.s.tableName).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).CreateInBatches(kvs, 100).Error; err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	return nil
}
