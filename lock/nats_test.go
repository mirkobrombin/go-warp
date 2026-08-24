package lock

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsserver "github.com/nats-io/nats-server/v2/test"
	nats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func newNATSLocker(t *testing.T) (*NATS, context.Context, func()) {
	t.Helper()
	s := natsserver.RunRandClientPortServer()
	if s == nil {
		t.Skip("requires NATS server")
	}
	// Restart with JetStream enabled.
	s.Shutdown()
	opts := natsserver.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s, err := server.NewServer(&opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	locker := NewNATS(js, "warp-lock-test")
	ctx := context.Background()
	cleanup := func() {
		nc.Close()
		s.Shutdown()
	}
	return locker, ctx, cleanup
}

func TestNATSTryLockDoubleAcquire(t *testing.T) {
	l, ctx, cleanup := newNATSLocker(t)
	defer cleanup()

	ok, err := l.TryLock(ctx, "k", time.Second)
	if err != nil {
		t.Fatalf("trylock: %v", err)
	}
	if !ok {
		t.Fatal("expected lock acquired")
	}

	ok2, err := l.TryLock(ctx, "k", time.Second)
	if err != nil {
		t.Fatalf("second trylock: %v", err)
	}
	if ok2 {
		t.Fatal("expected lock to be held by first holder")
	}

	if err := l.Release(ctx, "k"); err != nil {
		t.Fatalf("release: %v", err)
	}

	ok3, err := l.TryLock(ctx, "k", time.Second)
	if err != nil {
		t.Fatalf("trylock after release: %v", err)
	}
	if !ok3 {
		t.Fatal("expected lock re-acquired after release")
	}
}

func TestNATSAcquireTimeout(t *testing.T) {
	l, ctx, cleanup := newNATSLocker(t)
	defer cleanup()

	if ok, err := l.TryLock(ctx, "k", 0); err != nil || !ok {
		t.Fatalf("initial trylock: %v ok %v", err, ok)
	}

	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := l.Acquire(cctx, "k", 0); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("acquire did not respect context timeout")
	}
}
