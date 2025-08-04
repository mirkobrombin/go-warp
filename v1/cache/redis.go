package cache

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisCache implements Cache using a Redis backend.
type RedisCache struct {
	client *redis.Client
}

// NewRedis returns a new RedisCache.
func NewRedis(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

// Get retrieves the value for the given key.
func (c *RedisCache) Get(ctx context.Context, key string) (any, bool) {
	res, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	return res, true
}

// Set stores the value for the given key for the specified TTL.
func (c *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) {
	c.client.Set(ctx, key, value, ttl)
}

// Invalidate removes the key from Redis.
func (c *RedisCache) Invalidate(ctx context.Context, key string) {
	c.client.Del(ctx, key)
}
