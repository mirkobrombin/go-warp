package lock

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

var delScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`)

// Redis implements Locker using a Redis backend.
type Redis struct {
	client *redis.Client
	bus    syncbus.Bus

	mu     sync.Mutex
	tokens map[string]string
}

// NewRedis returns a new Redis locker using the provided client.
func NewRedis(client *redis.Client, bus syncbus.Bus) *Redis {
	if bus == nil {
		bus = syncbus.NewInMemoryBus()
	}
	return &Redis{client: client, bus: bus, tokens: make(map[string]string)}
}

// TryLock attempts to obtain the lock without waiting.
func (r *Redis) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	token := uuid.NewString()
	ok, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, err
	}
	if ok {
		r.mu.Lock()
		r.tokens[key] = token
		r.mu.Unlock()
	}
	return ok, nil
}

// Acquire blocks until the lock is obtained or the context is cancelled.
func (r *Redis) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	for {
		ok, err := r.TryLock(ctx, key, ttl)
		if err != nil {
			return err
		}
		if ok {
			_ = r.bus.Publish(ctx, "lock:"+key)
			return nil
		}
		ch, err := r.bus.Subscribe(ctx, "unlock:"+key)
		if err != nil {
			return err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			_ = r.bus.Unsubscribe(context.Background(), "unlock:"+key, ch)
			return ctx.Err()
		}
		_ = r.bus.Unsubscribe(context.Background(), "unlock:"+key, ch)
	}
}

// Release frees the lock for the given key.
func (r *Redis) Release(ctx context.Context, key string) error {
	r.mu.Lock()
	token, ok := r.tokens[key]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	_, err := delScript.Run(ctx, r.client, []string{key}, token).Result()
	if err == redis.Nil {
		err = nil
	}
	if err == nil {
		r.mu.Lock()
		delete(r.tokens, key)
		r.mu.Unlock()
		_ = r.bus.Publish(ctx, "unlock:"+key)
	}
	return err
}
