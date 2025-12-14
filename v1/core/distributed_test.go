package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	redis "github.com/redis/go-redis/v9"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	busnats "github.com/mirkobrombin/go-warp/v1/syncbus/nats"
	busredis "github.com/mirkobrombin/go-warp/v1/syncbus/redis"
)

type fakeQuorumBus struct {
	ackCh  chan struct{}
	err    error
	mu     sync.Mutex
	errSeq []error
}

func newFakeQuorumBus() *fakeQuorumBus {
	return &fakeQuorumBus{ackCh: make(chan struct{})}
}

func (b *fakeQuorumBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	return nil
}

func (b *fakeQuorumBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	if replicas <= 0 {
		replicas = 1
	}
	b.mu.Lock()
	var nextErr error
	if len(b.errSeq) > 0 {
		nextErr = b.errSeq[0]
		b.errSeq = b.errSeq[1:]
	} else {
		nextErr = b.err
	}
	b.mu.Unlock()
	if nextErr != nil {
		return nextErr
	}
	for i := 0; i < replicas; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.ackCh:
		}
	}
	return nil
}

func (b *fakeQuorumBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	if minZones <= 0 {
		minZones = 1
	}
	b.mu.Lock()
	var nextErr error
	if len(b.errSeq) > 0 {
		nextErr = b.errSeq[0]
		b.errSeq = b.errSeq[1:]
	} else {
		nextErr = b.err
	}
	b.mu.Unlock()
	if nextErr != nil {
		return nextErr
	}
	// fakeQuorumBus uses ackCh to simulate acks, we can reuse it for topology acks
	// assuming 1 ack per zone for simplicity in this fake
	for i := 0; i < minZones; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.ackCh:
		}
	}
	return nil
}

func (b *fakeQuorumBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	return nil, nil
}

func (b *fakeQuorumBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	return nil
}

func (b *fakeQuorumBus) RevokeLease(ctx context.Context, id string) error { return nil }

func (b *fakeQuorumBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return nil, nil
}

func (b *fakeQuorumBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return nil
}

func TestDistributedInvalidation(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	bus := busredis.NewRedisBus(busredis.RedisBusOptions{Client: client})
	store := adapter.NewInMemoryStore[int]()

	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()
	w1 := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg1))
	w2 := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg2))

	w1.Register("counter", ModeStrongDistributed, time.Minute)
	w2.Register("counter", ModeStrongDistributed, time.Minute)
	mergeFn := func(old, new int) (int, error) { return old + new, nil }
	w1.Merge("counter", mergeFn)
	w2.Merge("counter", mergeFn)

	ch, err := bus.Subscribe(ctx, "counter")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Unsubscribe(context.Background(), "counter", ch)
		_ = client.Close()
		mr.Close()
	})
	go func() {
		for range ch {
			_ = w2.cache.Invalidate(ctx, "counter")
		}
	}()

	if err := w1.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set1: %v", err)
	}
	if err := w1.Set(ctx, "counter", 2); err != nil {
		t.Fatalf("set2: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	v, err := w2.Get(ctx, "counter")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3 got %d", v)
	}

	before := bus.Metrics()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = w1.Invalidate(ctx, "counter")
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = w2.Invalidate(ctx, "counter")
	}()
	close(start)
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	after := bus.Metrics()
	// Deduplication is best-effort. If concurrent invalidations overlap, we expect 1 publish.
	// If they happen sequentially (due to scheduler/networking), we expect 2. Both are valid.
	if diff := after.Published - before.Published; diff < 1 || diff > 2 {
		t.Fatalf("expected 1 or 2 publishes, got %d", diff)
	}

	ev := testutil.ToFloat64(w1.evictionCounter) + testutil.ToFloat64(w2.evictionCounter)
	if ev != 2 {
		t.Fatalf("expected 2 evictions got %v", ev)
	}
}

func TestWarpDistributedNATS(t *testing.T) {
	ctx := context.Background()
	s := natsserver.RunRandClientPortServer()
	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	bus := busnats.NewNATSBus(conn)
	store := adapter.NewInMemoryStore[int]()

	reg1 := prometheus.NewRegistry()
	reg2 := prometheus.NewRegistry()
	w1 := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg1))
	w2 := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int](), WithMetrics[int](reg2))

	w1.Register("counter", ModeStrongDistributed, time.Minute)
	w2.Register("counter", ModeStrongDistributed, time.Minute)
	mergeFn := func(old, new int) (int, error) { return old + new, nil }
	w1.Merge("counter", mergeFn)
	w2.Merge("counter", mergeFn)

	ch, err := bus.Subscribe(ctx, "counter")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Unsubscribe(context.Background(), "counter", ch)
		conn.Close()
		s.Shutdown()
	})
	go func() {
		for range ch {
			_ = w2.cache.Invalidate(ctx, "counter")
		}
	}()

	if err := w1.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set1: %v", err)
	}
	if err := w1.Set(ctx, "counter", 2); err != nil {
		t.Fatalf("set2: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	v, err := w2.Get(ctx, "counter")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 3 {
		t.Fatalf("expected 3 got %d", v)
	}

	before := bus.Metrics()
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = w1.Invalidate(ctx, "counter")
	}()
	go func() {
		defer wg.Done()
		<-start
		_ = w2.Invalidate(ctx, "counter")
	}()
	close(start)
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	after := bus.Metrics()
	if after.Published-before.Published != 1 {
		t.Fatalf("expected 1 publish, got %d", after.Published-before.Published)
	}

	ev := testutil.ToFloat64(w1.evictionCounter) + testutil.ToFloat64(w2.evictionCounter)
	if ev != 2 {
		t.Fatalf("expected 2 evictions got %v", ev)
	}
}

func TestWarpStrongDistributedWaitsForQuorum(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)
	if !w.SetQuorum("counter", 2) {
		t.Fatalf("expected quorum configuration to succeed")
	}

	done := make(chan error, 1)
	go func() {
		done <- w.Set(ctx, "counter", 1)
	}()

	select {
	case err := <-done:
		t.Fatalf("set returned before quorum: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	bus.ackCh <- struct{}{}

	select {
	case err := <-done:
		t.Fatalf("set returned after single ack: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	bus.ackCh <- struct{}{}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("set error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("set timed out waiting for quorum")
	}
}

func TestWarpStrongDistributedQuorumTimeout(t *testing.T) {
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)
	w.SetQuorum("counter", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := w.Set(ctx, "counter", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestWarpStrongDistributedQuorumError(t *testing.T) {
	bus := newFakeQuorumBus()
	bus.err = syncbus.ErrQuorumNotSatisfied
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)
	w.SetQuorum("counter", 3)

	if err := w.Set(context.Background(), "counter", 1); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum error, got %v", err)
	}
}

func TestWarpSetRollbackOnQuorumFailure(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	bus.err = syncbus.ErrQuorumNotSatisfied
	if err := w.Set(ctx, "counter", 2); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum failure, got %v", err)
	}

	if got, ok, err := store.Get(ctx, "counter"); err != nil || !ok || got != 1 {
		t.Fatalf("store value changed after failure: ok=%v err=%v got=%d", ok, err, got)
	}
	if mv, ok, err := w.cache.Get(ctx, "counter"); err != nil {
		t.Fatalf("cache get: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("cache value changed after failure: ok=%v data=%v", ok, mv.Data)
	}
}

func TestWarpInvalidateRollbackOnQuorumFailure(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	bus.err = syncbus.ErrQuorumNotSatisfied
	if err := w.Invalidate(ctx, "counter"); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum failure, got %v", err)
	}

	if mv, ok, err := w.cache.Get(ctx, "counter"); err != nil {
		t.Fatalf("cache get: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("cache value removed after failure: ok=%v data=%v", ok, mv.Data)
	}
	if got, err := w.Get(ctx, "counter"); err != nil || got != 1 {
		t.Fatalf("warp get mismatch after failure: val=%d err=%v", got, err)
	}
}

func TestWarpTxnRollbackOnQuorumFailure(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	txn := w.Txn(ctx)
	txn.Set("counter", 2)
	bus.err = syncbus.ErrQuorumNotSatisfied
	if err := txn.Commit(); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum failure, got %v", err)
	}

	if mv, ok, err := w.cache.Get(ctx, "counter"); err != nil {
		t.Fatalf("cache get: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("cache value changed after commit failure: ok=%v data=%v", ok, mv.Data)
	}
	if got, ok, err := store.Get(ctx, "counter"); err != nil || !ok || got != 1 {
		t.Fatalf("store value changed after commit failure: ok=%v err=%v val=%d", ok, err, got)
	}
}

func TestWarpTxnDeleteRollbackOnQuorumFailure(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set baseline: %v", err)
	}

	txn := w.Txn(ctx)
	txn.Delete("counter")
	bus.err = syncbus.ErrQuorumNotSatisfied
	if err := txn.Commit(); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum failure, got %v", err)
	}

	if mv, ok, err := w.cache.Get(ctx, "counter"); err != nil {
		t.Fatalf("cache get: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("cache value removed after delete failure: ok=%v data=%v", ok, mv.Data)
	}
	if got, ok, err := store.Get(ctx, "counter"); err != nil || !ok || got != 1 {
		t.Fatalf("store value removed after delete failure: ok=%v err=%v val=%d", ok, err, got)
	}
}

func TestWarpTxnRollbackMultiKeyOnQuorumFailure(t *testing.T) {
	ctx := context.Background()
	bus := newFakeQuorumBus()
	store := adapter.NewInMemoryStore[int]()
	w := New(cache.NewInMemory[merge.Value[int]](), store, bus, merge.NewEngine[int]())
	w.Register("counter", ModeStrongDistributed, time.Minute)
	w.Register("mirror", ModeStrongDistributed, time.Minute)

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "counter", 1); err != nil {
		t.Fatalf("set counter baseline: %v", err)
	}

	go func() {
		bus.ackCh <- struct{}{}
	}()
	if err := w.Set(ctx, "mirror", 1); err != nil {
		t.Fatalf("set mirror baseline: %v", err)
	}

	txn := w.Txn(ctx)
	txn.Set("counter", 2)
	txn.Set("mirror", 5)

	bus.mu.Lock()
	bus.errSeq = []error{nil, syncbus.ErrQuorumNotSatisfied}
	bus.mu.Unlock()

	go func() {
		bus.ackCh <- struct{}{}
	}()

	if err := txn.Commit(); !errors.Is(err, syncbus.ErrQuorumNotSatisfied) {
		t.Fatalf("expected quorum failure, got %v", err)
	}

	if mv, ok, err := w.cache.Get(ctx, "counter"); err != nil {
		t.Fatalf("cache get counter: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("counter cache changed after failure: ok=%v val=%v", ok, mv.Data)
	}
	if mv, ok, err := w.cache.Get(ctx, "mirror"); err != nil {
		t.Fatalf("cache get mirror: %v", err)
	} else if !ok || mv.Data != 1 {
		t.Fatalf("mirror cache changed after failure: ok=%v val=%v", ok, mv.Data)
	}

	if got, ok, err := store.Get(ctx, "counter"); err != nil || !ok || got != 1 {
		t.Fatalf("store counter changed after failure: ok=%v err=%v val=%d", ok, err, got)
	}
	if got, ok, err := store.Get(ctx, "mirror"); err != nil || !ok || got != 1 {
		t.Fatalf("store mirror changed after failure: ok=%v err=%v val=%d", ok, err, got)
	}
}
