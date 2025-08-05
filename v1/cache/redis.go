package cache

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisCache implements Cache using a Redis backend.
type RedisCache[T any] struct {
	client *redis.Client
}

// NewRedis returns a new RedisCache.
func NewRedis[T any](client *redis.Client) *RedisCache[T] {
	return &RedisCache[T]{client: client}
}

// Get retrieves the value for the given key.
func (c *RedisCache[T]) Get(ctx context.Context, key string) (T, bool) {
	var zero T
	res, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return zero, false
	}
	if err != nil {
		return zero, false
	}
	v, ok := any(res).(T)
	if !ok {
		return zero, false
	}
	return v, true
}

// Set stores the value for the given key for the specified TTL.
func (c *RedisCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Invalidate removes the key from Redis.
func (c *RedisCache[T]) Invalidate(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
