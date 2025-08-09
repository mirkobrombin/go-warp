package adapter

import (
	"context"
	"encoding/json"

	redis "github.com/redis/go-redis/v9"
)

// RedisStore implements Store using a Redis backend.
type RedisStore[T any] struct {
	client *redis.Client
}

// NewRedisStore returns a new RedisStore using the provided Redis client.
func NewRedisStore[T any](client *redis.Client) *RedisStore[T] {
	return &RedisStore[T]{client: client}
}

// Get implements Store.Get.
func (s *RedisStore[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return zero, false, nil
	}
	if err != nil {
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
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key, data, 0).Err()
}

// Keys implements Store.Keys using SCAN to iterate over keys.
func (s *RedisStore[T]) Keys(ctx context.Context) ([]string, error) {
	var cursor uint64
	var keys []string
	for {
		batch, next, err := s.client.Scan(ctx, cursor, "*", 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		if next == 0 {
			break
		}
		cursor = next
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
	pipe := b.s.client.TxPipeline()
	for k, v := range b.sets {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		pipe.Set(ctx, k, data, 0)
	}
	if len(b.deletes) > 0 {
		pipe.Del(ctx, b.deletes...)
	}
	_, err := pipe.Exec(ctx)
	return err
}
