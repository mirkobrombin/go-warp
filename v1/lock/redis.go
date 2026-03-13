package lock

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
)

// redisDelScript atomically deletes a key only when its value matches the
// caller-supplied token, preventing a lock holder from evicting another owner.
var redisDelScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)

// redisLockEntry tracks the ownership token and optional TTL timer for a held lock.
type redisLockEntry struct {
	token string
	timer *time.Timer
}

// Redis implements Locker using a Redis backend.
// Locks are acquired with SET NX EX and released via a Lua script that verifies
// ownership before deletion, so only the current holder can free a lock.
// Acquire subscribes to keyspace notifications for efficient wake-up; when the
// server has notifications disabled it falls back to exponential-backoff polling.
type Redis struct {
	client redis.UniversalClient

	mu      sync.Mutex
	entries map[string]*redisLockEntry
}

// NewRedis returns a new Redis locker backed by client.
// client may be a standalone, Sentinel, or cluster client.
func NewRedis(client redis.UniversalClient) *Redis {
	return &Redis{
		client:  client,
		entries: make(map[string]*redisLockEntry),
	}
}

// TryLock attempts to obtain the lock without waiting.
// On success it stores a UUID token locally and, when ttl > 0, arms a timer
// that releases the lock automatically to clean up local state after expiry.
func (r *Redis) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	token := uuid.NewString()
	ok, err := r.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	entry := &redisLockEntry{token: token}
	if ttl > 0 {
		// Capture token by value so the closure always releases the right lock,
		// even if the entry is replaced by a concurrent re-acquisition.
		capturedToken := token
		entry.timer = time.AfterFunc(ttl, func() {
			_ = r.releaseWithToken(context.Background(), key, capturedToken)
		})
	}
	r.mu.Lock()
	r.entries[key] = entry
	r.mu.Unlock()
	return true, nil
}

// Release frees the lock for the given key if the caller currently holds it.
func (r *Redis) Release(ctx context.Context, key string) error {
	r.mu.Lock()
	entry, ok := r.entries[key]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	return r.releaseWithToken(ctx, key, entry.token)
}

// releaseWithToken runs the ownership-checked Lua delete script and removes the
// local entry only when the stored token matches, preventing races with
// concurrent re-acquisitions from overwriting a newer holder's entry.
func (r *Redis) releaseWithToken(ctx context.Context, key, token string) error {
	_, err := redisDelScript.Run(ctx, r.client, []string{key}, token).Result()
	if err == redis.Nil {
		err = nil
	}
	if err == nil {
		r.cleanupLocal(key, token)
	}
	return err
}

// cleanupLocal removes the entry for key only when the stored token matches,
// guarding against a timer firing after a concurrent re-acquisition.
func (r *Redis) cleanupLocal(key, token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[key]; ok && e.token == token {
		delete(r.entries, key)
	}
}

// Acquire blocks until the lock for key is obtained or ctx is cancelled.
// It subscribes once to keyspace notifications (__keyevent@0__:del and :expired)
// before the retry loop so that a Release firing between a failed TryLock and
// the wait cannot be missed. When the server does not publish those events the
// select falls through on a timer and retries with exponential back-off
// (5 ms → 500 ms).
func (r *Redis) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	const baseInterval = 5 * time.Millisecond
	const maxInterval = 500 * time.Millisecond
	interval := baseInterval

	// Subscribe ONCE before the retry loop — a single subscription avoids the
	// overhead of re-subscribing on every failed TryLock and prevents missing
	// release notifications that arrive between TryLock and the wait.
	pubsub := r.client.Subscribe(ctx,
		"__keyevent@0__:del",
		"__keyevent@0__:expired",
	)
	defer pubsub.Close()
	msgCh := pubsub.Channel()

	for {
		ok, err := r.TryLock(ctx, key, ttl)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-msgCh:
			timer.Stop()
			interval = baseInterval // reset after a real wake-up
		case <-timer.C:
			if interval < maxInterval {
				interval *= 2
			}
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}
