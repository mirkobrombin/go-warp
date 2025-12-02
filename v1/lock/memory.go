package lock

import (
	"context"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

type lockState struct {
	timer  *time.Timer
	notify chan struct{}
}

// InMemory implements Locker using local memory. Lock and unlock events are
// propagated through a syncbus Bus allowing multiple nodes to coordinate.
type InMemory struct {
	mu      sync.Mutex
	bus     syncbus.Bus
	locks   map[string]*lockState
	subs    map[string]struct{}
	pending map[string]int
}

// NewInMemory returns a new in-memory locker that uses bus to propagate events.
func NewInMemory(bus syncbus.Bus) *InMemory {
	if bus == nil {
		bus = syncbus.NewInMemoryBus()
	}
	return &InMemory{
		bus:     bus,
		locks:   make(map[string]*lockState),
		subs:    make(map[string]struct{}),
		pending: make(map[string]int),
	}
}

func (l *InMemory) ensureSubscriptions(key string) error {
	l.mu.Lock()
	if _, ok := l.subs[key]; ok {
		l.mu.Unlock()
		return nil
	}
	l.subs[key] = struct{}{}
	l.mu.Unlock()

	cleanup := func() {
		l.mu.Lock()
		delete(l.subs, key)
		l.mu.Unlock()
	}

	lockCh, err := l.bus.Subscribe(context.Background(), "lock:"+key)
	if err != nil {
		cleanup()
		return err
	}
	unlockCh, err := l.bus.Subscribe(context.Background(), "unlock:"+key)
	if err != nil {
		_ = l.bus.Unsubscribe(context.Background(), "lock:"+key, lockCh)
		cleanup()
		return err
	}

	go func() {
		for range lockCh {
			l.mu.Lock()
			if l.pending["lock:"+key] > 0 {
				l.pending["lock:"+key]--
				l.mu.Unlock()
				continue
			}
			if _, ok := l.locks[key]; !ok {
				l.locks[key] = &lockState{notify: make(chan struct{})}
			}
			l.mu.Unlock()
		}
	}()
	go func() {
		for range unlockCh {
			l.mu.Lock()
			if l.pending["unlock:"+key] > 0 {
				l.pending["unlock:"+key]--
				l.mu.Unlock()
				continue
			}
			if st, ok := l.locks[key]; ok {
				if st.timer != nil {
					st.timer.Stop()
				}
				close(st.notify)
				delete(l.locks, key)
			}
			l.mu.Unlock()
		}
	}()
	return nil
}

// TryLock attempts to obtain the lock without waiting. It returns true on success.
func (l *InMemory) TryLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if err := l.ensureSubscriptions(key); err != nil {
		return false, err
	}
	l.mu.Lock()
	if _, ok := l.locks[key]; ok {
		l.mu.Unlock()
		return false, nil
	}
	st := &lockState{notify: make(chan struct{})}
	if ttl > 0 {
		st.timer = time.AfterFunc(ttl, func() {
			_ = l.Release(context.Background(), key)
		})
	}
	l.locks[key] = st
	l.pending["lock:"+key]++
	l.mu.Unlock()
	_ = l.bus.Publish(ctx, "lock:"+key)
	return true, nil
}

// Acquire blocks until the lock is obtained or the context is cancelled.
func (l *InMemory) Acquire(ctx context.Context, key string, ttl time.Duration) error {
	if err := l.ensureSubscriptions(key); err != nil {
		return err
	}
	for {
		ok, err := l.TryLock(ctx, key, ttl)
		if err != nil {
			return err
		}
		if ok {
			_ = l.bus.Publish(ctx, "lock:"+key)
			return nil
		}
		l.mu.Lock()
		st := l.locks[key]
		ch := st.notify
		l.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Release frees the lock for the given key.
func (l *InMemory) Release(ctx context.Context, key string) error {
	l.mu.Lock()
	st, ok := l.locks[key]
	if ok {
		if st.timer != nil {
			st.timer.Stop()
		}
		close(st.notify)
		delete(l.locks, key)
		l.pending["unlock:"+key]++
	}
	l.mu.Unlock()
	if ok {
		_ = l.bus.Publish(ctx, "unlock:"+key)
	}
	return nil
}
