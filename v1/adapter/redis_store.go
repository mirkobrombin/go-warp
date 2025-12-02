package adapter

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"time"

	redis "github.com/redis/go-redis/v9"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
)

const defaultRedisOpTimeout = 5 * time.Second

// RedisStore implements Store using a Redis backend.
type RedisStore[T any] struct {
	client  *redis.Client
	timeout time.Duration
}

// RedisOption configures a RedisStore.
type RedisOption func(*redisStoreOptions)

type redisStoreOptions struct {
	timeout time.Duration
}

// WithTimeout sets the operation timeout for Redis calls.
func WithTimeout(d time.Duration) RedisOption {
	return func(o *redisStoreOptions) {
		o.timeout = d
	}
}

// NewRedisStore returns a new RedisStore using the provided Redis client.
func NewRedisStore[T any](client *redis.Client, opts ...RedisOption) *RedisStore[T] {
	o := redisStoreOptions{timeout: defaultRedisOpTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	return &RedisStore[T]{client: client, timeout: o.timeout}
}

// Get implements Store.Get.
func (s *RedisStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	data, err := s.client.Get(cctx, key).Bytes()
	if err == redis.Nil {
		return zero, false, nil
	}
	if err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		if stdErrors.Is(err, redis.ErrClosed) {
			return zero, false, warperrors.ErrConnectionClosed
		}
		return zero, false, err
	}
	if err := cctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, false, err
	}
	return v, true, nil
}

// Set implements Store.Set.
func (s *RedisStore[T]) Set(ctx context.Context, key string, value T) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.client.Set(cctx, key, data, 0).Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		if stdErrors.Is(err, redis.ErrClosed) {
			return warperrors.ErrConnectionClosed
		}
		return err
	}
	return nil
}

// Keys implements Store.Keys using SCAN to iterate over keys.
func (s *RedisStore[T]) Keys(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	var cursor uint64
	var keys []string
	for {
		batch, next, err := s.client.Scan(cctx, cursor, "*", 100).Result()
		if err != nil {
			if stdErrors.Is(err, context.DeadlineExceeded) {
				return nil, warperrors.ErrTimeout
			}
			if stdErrors.Is(err, redis.ErrClosed) {
				return nil, warperrors.ErrConnectionClosed
			}
			return nil, err
		}
		keys = append(keys, batch...)
		if next == 0 {
			break
		}
		cursor = next
	}
	if err := cctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return nil, warperrors.ErrTimeout
		}
		return nil, err
	}
	return keys, nil
}

// Batch implements Batcher.Batch using a Redis pipeline.
func (s *RedisStore[T]) Batch(ctx context.Context) (Batch[T], error) {
	return &redisBatch[T]{s: s, sets: make(map[string]T)}, nil
}

type redisBatch[T any] struct {
	s       *RedisStore[T]
	sets    map[string]T
	deletes []string
}

func (b *redisBatch[T]) Set(ctx context.Context, key string, value T) error {
	b.sets[key] = value
	return nil
}

func (b *redisBatch[T]) Delete(ctx context.Context, key string) error {
	b.deletes = append(b.deletes, key)
	return nil
}

func (b *redisBatch[T]) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, b.s.timeout)
	defer cancel()
	pipe := b.s.client.TxPipeline()
	for k, v := range b.sets {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		pipe.Set(cctx, k, data, 0)
	}
	if len(b.deletes) > 0 {
		pipe.Del(cctx, b.deletes...)
	}
	_, err := pipe.Exec(cctx)
	if err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		if stdErrors.Is(err, redis.ErrClosed) {
			return warperrors.ErrConnectionClosed
		}
		return err
	}
	return nil
}
