package lock

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
)

type lockEntry struct {
	token string
	timer *time.Timer
}

// NATS implements Locker using NATS JetStream Key-Value as the coordination
// backend. This provides distributed locks without requiring Redis.
type NATS struct {
	js     jetstream.JetStream
	bucket string
	mu     sync.Mutex
	locks  map[string]*lockEntry
	kv     jetstream.KeyValue
	kvOnce sync.Once
	kvErr  error
}

// NewNATS returns a NATS JetStream-backed distributed locker.
// bucket is the JetStream KV bucket name; it is created automatically if absent.
func NewNATS(js jetstream.JetStream, bucket string) *NATS {
	return &NATS{
		js:     js,
		bucket: bucket,
		locks:  make(map[string]*lockEntry),
	}
}

func (n *NATS) getKV() (jetstream.KeyValue, error) {
	n.kvOnce.Do(func() {
		kv, err := n.js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{
			Bucket:  n.bucket,
			History: 1,
		})
		if err == nil {
			n.kv = kv
			return
		}
		if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			n.kv, n.kvErr = n.js.KeyValue(context.Background(), n.bucket)
			return
		}
		n.kvErr = err
	})
	return n.kv, n.kvErr
}

// TryLock attempts to obtain the lock without waiting. It returns true on success.
// If ttl is greater than zero, a goroutine is scheduled to release the lock after
// the duration (per-key TTL, since JetStream KV TTL is bucket-level only).
func (n *NATS) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	kv, err := n.getKV()
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
	entry := &lockEntry{token: token}
	if ttl > 0 {
		// Bind the timer to this specific token so a re-acquired lock held
		// by another caller cannot be released when this timer fires.
		entry.timer = time.AfterFunc(ttl, func() {
			_ = n.releaseWithToken(context.Background(), key, token)
		})
	}
	n.mu.Lock()
	n.locks[key] = entry
	n.mu.Unlock()
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
		kv, err := n.getKV()
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

// Release frees the lock for the given key if this instance still holds it.
func (n *NATS) Release(ctx context.Context, key string) error {
	n.mu.Lock()
	entry, ok := n.locks[key]
	n.mu.Unlock()
	if !ok {
		return nil
	}
	return n.releaseWithToken(ctx, key, entry.token)
}

// releaseWithToken deletes the lock only when the KV store still holds the
// expected token, using an optimistic last-revision check to prevent stealing
// a lock re-acquired by another caller after expiry or a network partition.
func (n *NATS) releaseWithToken(ctx context.Context, key, token string) error {
	kv, err := n.getKV()
	if err != nil {
		return err
	}
	current, err := kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			n.cleanupLocal(key, token)
			return nil
		}
		return err
	}
	if string(current.Value()) != token {
		// Another caller holds the lock; do not delete.
		return nil
	}
	// Atomic ownership-checked delete: fails if entry was modified between
	// our Get and this Delete (JSErrCodeStreamWrongLastSequence).
	if err := kv.Delete(ctx, key, jetstream.LastRevision(current.Revision())); err != nil {
		var apiErr *jetstream.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence {
			// Lost a race — entry changed concurrently; our lock is already gone.
			return nil
		}
		return err
	}
	n.cleanupLocal(key, token)
	return nil
}

func (n *NATS) cleanupLocal(key, token string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e, ok := n.locks[key]; ok && e.token == token {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(n.locks, key)
	}
}
