package streambus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublishExactAndPrefix(t *testing.T) {
	bus := NewInMemory(Config{})
	t.Cleanup(func() { _ = bus.Close() })

	exact := mustSubscribe(t, bus, SubscribeOptions{Topic: "ui:button", Buffer: 4})
	prefix := mustSubscribe(t, bus, SubscribeOptions{Topic: "ui:", Prefix: true, Buffer: 4})
	other := mustSubscribe(t, bus, SubscribeOptions{Topic: "data:", Prefix: true, Buffer: 4})

	sequence, err := bus.Publish(context.Background(), Frame{Topic: "ui:button", Payload: []byte("clicked")})
	if err != nil {
		t.Fatal(err)
	}
	if sequence == 0 {
		t.Fatal("expected an assigned sequence")
	}

	for name, subscription := range map[string]*Subscription{"exact": exact, "prefix": prefix} {
		frame := receiveFrame(t, subscription)
		if frame.Sequence != sequence || string(frame.Payload) != "clicked" {
			t.Fatalf("%s received %#v", name, frame)
		}
	}
	select {
	case frame := <-other.Frames():
		t.Fatalf("unrelated prefix received %#v", frame)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestReplayPreservesOrder(t *testing.T) {
	bus := NewInMemory(Config{ReplayCapacity: 4})
	t.Cleanup(func() { _ = bus.Close() })
	for _, payload := range []string{"one", "two", "three"} {
		if _, err := bus.Publish(context.Background(), Frame{Topic: "history", Payload: []byte(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "history", Buffer: 4, Replay: 2})
	if got := string(receiveFrame(t, subscription).Payload); got != "two" {
		t.Fatalf("first replay frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "three" {
		t.Fatalf("second replay frame = %q", got)
	}
}

func TestResumeFromSequence(t *testing.T) {
	bus := NewInMemory(Config{ReplayCapacity: 4})
	t.Cleanup(func() { _ = bus.Close() })
	sequences := make([]uint64, 0, 3)
	for _, payload := range []string{"one", "two", "three"} {
		sequence, err := bus.Publish(context.Background(), Frame{Topic: "resume", Payload: []byte(payload)})
		if err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, sequence)
	}
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "resume", Buffer: 4, Since: sequences[0]})
	if got := string(receiveFrame(t, subscription).Payload); got != "two" {
		t.Fatalf("first resumed frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "three" {
		t.Fatalf("second resumed frame = %q", got)
	}
}

func TestResumeReportsKnownGap(t *testing.T) {
	bus := NewInMemory(Config{ReplayCapacity: 2})
	t.Cleanup(func() { _ = bus.Close() })
	first, err := bus.Publish(context.Background(), Frame{Topic: "resume", Payload: []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	publishString(t, bus, "resume", "two")
	publishString(t, bus, "resume", "three")
	if _, err := bus.Subscribe(context.Background(), SubscribeOptions{Topic: "resume", Buffer: 2, Since: first}); !errors.Is(err, ErrReplayUnavailable) {
		t.Fatalf("resume gap = %v", err)
	}
}

func TestLatestOnlyCollapsesQueuedFrames(t *testing.T) {
	bus := NewInMemory(Config{})
	t.Cleanup(func() { _ = bus.Close() })
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "cursor", Buffer: 2, Overflow: LatestOnly})

	publishString(t, bus, "cursor", "one")
	waitFor(t, func() bool { return subscription.Stats().Queued == 0 })
	for _, value := range []string{"two", "three", "four"} {
		publishString(t, bus, "cursor", value)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "one" {
		t.Fatalf("in-flight frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "four" {
		t.Fatalf("latest frame = %q", got)
	}
	if subscription.Stats().Dropped == 0 {
		t.Fatal("expected collapsed frames to be counted")
	}
}

func TestDropNewestPreservesQueuedFrame(t *testing.T) {
	bus := NewInMemory(Config{})
	t.Cleanup(func() { _ = bus.Close() })
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "events", Buffer: 1, Overflow: DropNewest})

	publishString(t, bus, "events", "one")
	waitFor(t, func() bool { return subscription.Stats().Queued == 0 })
	publishString(t, bus, "events", "two")
	publishString(t, bus, "events", "three")

	if got := string(receiveFrame(t, subscription).Payload); got != "one" {
		t.Fatalf("first frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "two" {
		t.Fatalf("queued frame = %q", got)
	}
	if got := subscription.Stats().Dropped; got != 1 {
		t.Fatalf("dropped = %d", got)
	}
}

func TestBlockAppliesBackpressure(t *testing.T) {
	bus := NewInMemory(Config{})
	t.Cleanup(func() { _ = bus.Close() })
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "bulk", Buffer: 1, Overflow: Block})

	publishString(t, bus, "bulk", "one")
	waitFor(t, func() bool { return subscription.Stats().Queued == 0 })
	publishString(t, bus, "bulk", "two")

	completed := make(chan error, 1)
	go func() {
		_, err := bus.Publish(context.Background(), Frame{Topic: "bulk", Payload: []byte("three")})
		completed <- err
	}()
	select {
	case err := <-completed:
		t.Fatalf("publish completed before capacity was available: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_ = receiveFrame(t, subscription)
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked publish did not resume")
	}
}

func TestPriorityBypassesQueuedBulkFrames(t *testing.T) {
	bus := NewInMemory(Config{})
	t.Cleanup(func() { _ = bus.Close() })
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "mixed", Buffer: 3, Overflow: Block})

	if _, err := bus.Publish(context.Background(), Frame{Topic: "mixed", Payload: []byte("in-flight"), Priority: PriorityBulk}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return subscription.Stats().Queued == 0 })
	if _, err := bus.Publish(context.Background(), Frame{Topic: "mixed", Payload: []byte("bulk"), Priority: PriorityBulk}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Publish(context.Background(), Frame{Topic: "mixed", Payload: []byte("critical"), Priority: PriorityCritical}); err != nil {
		t.Fatal(err)
	}

	if got := string(receiveFrame(t, subscription).Payload); got != "in-flight" {
		t.Fatalf("first frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "critical" {
		t.Fatalf("prioritized frame = %q", got)
	}
	if got := string(receiveFrame(t, subscription).Payload); got != "bulk" {
		t.Fatalf("bulk frame = %q", got)
	}
}

func TestCopyPayload(t *testing.T) {
	bus := NewInMemory(Config{CopyPayload: true})
	t.Cleanup(func() { _ = bus.Close() })
	subscription := mustSubscribe(t, bus, SubscribeOptions{Topic: "safe", Buffer: 1})
	payload := []byte("before")
	if _, err := bus.Publish(context.Background(), Frame{Topic: "safe", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	copy(payload, "after!")
	if got := string(receiveFrame(t, subscription).Payload); got != "before" {
		t.Fatalf("copied payload = %q", got)
	}
}

func TestSubscriptionContextAndBusClose(t *testing.T) {
	bus := NewInMemory(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	subscription := mustSubscribeContext(t, bus, ctx, SubscribeOptions{Topic: "done", Buffer: 1})
	cancel()
	select {
	case <-subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("subscription did not follow context cancellation")
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Publish(context.Background(), Frame{Topic: "done"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close = %v", err)
	}
}

func TestValidation(t *testing.T) {
	bus := NewInMemory(Config{MaxBuffer: 2, MaxPayloadBytes: 3})
	t.Cleanup(func() { _ = bus.Close() })
	if _, err := bus.Subscribe(context.Background(), SubscribeOptions{Topic: "x", Buffer: 3}); !errors.Is(err, ErrInvalidBuffer) {
		t.Fatalf("oversized buffer = %v", err)
	}
	if _, err := bus.Publish(context.Background(), Frame{Topic: "x", Payload: []byte("four")}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized payload = %v", err)
	}
	withoutReplay := NewInMemory(Config{})
	t.Cleanup(func() { _ = withoutReplay.Close() })
	if _, err := withoutReplay.Subscribe(context.Background(), SubscribeOptions{Topic: "x", Since: 1}); !errors.Is(err, ErrReplayUnavailable) {
		t.Fatalf("resume without replay = %v", err)
	}
}

func mustSubscribe(t *testing.T, bus Bus, options SubscribeOptions) *Subscription {
	t.Helper()
	return mustSubscribeContext(t, bus, context.Background(), options)
}

func mustSubscribeContext(t *testing.T, bus Bus, ctx context.Context, options SubscribeOptions) *Subscription {
	t.Helper()
	subscription, err := bus.Subscribe(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	return subscription
}

func publishString(t *testing.T, bus Bus, topic, payload string) {
	t.Helper()
	if _, err := bus.Publish(context.Background(), Frame{Topic: topic, Payload: []byte(payload)}); err != nil {
		t.Fatal(err)
	}
}

func receiveFrame(t *testing.T, subscription *Subscription) Frame {
	t.Helper()
	select {
	case frame, ok := <-subscription.Frames():
		if !ok {
			t.Fatal("subscription closed")
		}
		return frame
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame")
		return Frame{}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
