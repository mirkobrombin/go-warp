package lock

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

// NATS implements Locker using NATS JetStream Key-Value as the coordination
// backend. This provides distributed locks without requiring Redis.
type NATS struct {
	js     jetstream.JetStream
	bucket string
	mu     sync.Mutex
	tokens map[string]string
}

// NewNATS returns a NATS JetStream-backed distributed locker.
// bucket is the JetStream KV bucket name; it is created automatically if absent.
func NewNATS(js jetstream.JetStream, bucket string) *NATS {
	return &NATS{
		js:     js,
		bucket: bucket,
		tokens: make(map[string]string),
	}
}

func (n *NATS) getKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := n.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  n.bucket,
		History: 1,
	})
	if err == nil {
		return kv, nil
	}
	if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return n.js.KeyValue(ctx, n.bucket)
	}
	return nil, err
}

// TryLock attempts to obtain the lock without waiting. It returns true on success.
// If ttl is greater than zero, a goroutine is scheduled to release the lock after
// the duration (per-key TTL, since JetStream KV TTL is bucket-level only).
func (n *NATS) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	kv, err := n.getKV(ctx)
	if err != nil {
		return false, err
	}
	token := uuid.NewString()
	_, err = kv.Create(ctx, key, []byte(token))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return false, nil
		}
		return false, err
	}
	n.mu.Lock()
	n.tokens[key] = token
	n.mu.Unlock()
	if ttl > 0 {
		time.AfterFunc(ttl, func() {
			_ = n.Release(context.Background(), key)
		})
	}
	return true, nil
}

// Acquire blocks until the lock for key is obtained or the context is cancelled.
func (n *NATS) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	for {
		ok, err := n.TryLock(ctx, key, ttl)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		kv, err := n.getKV(ctx)
		if err != nil {
			return err
		}
		watcher, err := kv.Watch(ctx, key, jetstream.UpdatesOnly())
		if err != nil {
			return err
		}
		waiting := true
		for waiting {
			select {
			case entry, open := <-watcher.Updates():
				if !open {
					waiting = false
					break
				}
				if entry == nil {
					continue
				}
				if entry.Operation() == jetstream.KeyValueDelete {
					waiting = false
				}
			case <-ctx.Done():
				_ = watcher.Stop()
				return ctx.Err()
			}
		}
		_ = watcher.Stop()
	}
}

// Release frees the lock for the given key.
func (n *NATS) Release(ctx context.Context, key string) error {
	n.mu.Lock()
	_, ok := n.tokens[key]
	n.mu.Unlock()
	if !ok {
		return nil
	}
	kv, err := n.getKV(ctx)
	if err != nil {
		return err
	}
	if err := kv.Delete(ctx, key); err != nil {
		return err
	}
	n.mu.Lock()
	delete(n.tokens, key)
	n.mu.Unlock()
	return nil
}
