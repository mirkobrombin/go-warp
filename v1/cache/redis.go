package cache

import (
	"context"
	"encoding/json"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Codec defines methods for encoding and decoding values stored in Redis.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONCodec implements Codec using encoding/json.
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// RedisCache implements Cache using a Redis backend.
type RedisCache[T any] struct {
	client *redis.Client
	codec  Codec
}

// NewRedis returns a new RedisCache using the provided Redis client.
// If codec is nil, JSONCodec is used by default.
func NewRedis[T any](client *redis.Client, codec Codec) *RedisCache[T] {
	if codec == nil {
		codec = JSONCodec{}
	}
	return &RedisCache[T]{client: client, codec: codec}
}

// Get retrieves the value for the given key.
func (c *RedisCache[T]) Get(ctx context.Context, key string) (T, bool) {
	var zero T
	data, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return zero, false
	}
	if err != nil {
		return zero, false
	}
	var v T
	if err := c.codec.Unmarshal(data, &v); err != nil {
		return zero, false
	}
	return v, true
}

// Set stores the value for the given key for the specified TTL.
func (c *RedisCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	data, err := c.codec.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, data, ttl).Err()
}

// Invalidate removes the key from Redis.
func (c *RedisCache[T]) Invalidate(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
