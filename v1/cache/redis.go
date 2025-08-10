package cache

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"time"

	redis "github.com/redis/go-redis/v9"

	warperrors "github.com/mirkobrombin/go-warp/v1/errors"
)

const redisCacheTimeout = 5 * time.Second

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
func (c *RedisCache[T]) Get(ctx context.Context, key string) (T, bool, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return zero, false, warperrors.ErrTimeout
		}
		return zero, false, err
	}
	cctx, cancel := context.WithTimeout(ctx, redisCacheTimeout)
	defer cancel()
	data, err := c.client.Get(cctx, key).Bytes()
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
	if err := c.codec.Unmarshal(data, &v); err != nil {
		return zero, false, err
	}
	return v, true, nil
}

// Set stores the value for the given key for the specified TTL.
func (c *RedisCache[T]) Set(ctx context.Context, key string, value T, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	data, err := c.codec.Marshal(value)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, redisCacheTimeout)
	defer cancel()
	if err := c.client.Set(cctx, key, data, ttl).Err(); err != nil {
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

// Invalidate removes the key from Redis.
func (c *RedisCache[T]) Invalidate(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		if stdErrors.Is(err, context.DeadlineExceeded) {
			return warperrors.ErrTimeout
		}
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, redisCacheTimeout)
	defer cancel()
	if err := c.client.Del(cctx, key).Err(); err != nil {
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
