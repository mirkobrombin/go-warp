package streambus

import (
	"context"
	"errors"
	"time"
)

var (
	ErrClosed            = errors.New("streambus: closed")
	ErrSubscriptionDone  = errors.New("streambus: subscription closed")
	ErrInvalidTopic      = errors.New("streambus: topic is required")
	ErrPayloadTooLarge   = errors.New("streambus: payload exceeds configured limit")
	ErrInvalidBuffer     = errors.New("streambus: invalid subscription buffer size")
	ErrReplayUnavailable = errors.New("streambus: requested replay is no longer available")
)

// Reliability describes whether a transport may discard a frame in flight.
// The in-memory bus preserves this value for transport adapters.
type Reliability uint8

const (
	Reliable Reliability = iota
	Unreliable
)

// Priority allows transports to isolate latency-sensitive traffic from bulk
// streams. Higher values represent more urgent traffic.
type Priority uint8

const (
	PriorityBulk Priority = iota
	PriorityNormal
	PriorityInteractive
	PriorityCritical
)

// OverflowPolicy defines what happens when a subscriber cannot keep up.
type OverflowPolicy uint8

const (
	// Block applies backpressure to Publish until queue capacity is available.
	Block OverflowPolicy = iota
	// DropNewest preserves queued frames and discards the incoming frame.
	DropNewest
	// DropOldest discards the oldest queued frame to make room.
	DropOldest
	// LatestOnly collapses the queue to the most recent state.
	LatestOnly
)

// Frame is an immutable unit delivered by StreamBus. Payload must not be
// modified after Publish unless Config.CopyPayload is enabled.
type Frame struct {
	Sequence    uint64
	Timestamp   time.Time
	Topic       string
	Payload     []byte
	Reliability Reliability
	Priority    Priority
}

// SubscribeOptions configure an exact-topic or prefix subscription.
type SubscribeOptions struct {
	Topic    string
	Prefix   bool
	Buffer   int
	Overflow OverflowPolicy
	// Replay requests up to this many recent frames. Replay requires a non-zero
	// Config.ReplayCapacity and is only supported for exact-topic subscriptions.
	Replay int
	// Since resumes an exact-topic stream after the given sequence. StreamBus
	// returns ErrReplayUnavailable instead of silently skipping a known gap.
	Since uint64
}

// Config controls an in-memory StreamBus.
type Config struct {
	DefaultBuffer   int
	MaxBuffer       int
	ReplayCapacity  int
	MaxPayloadBytes int
	CopyPayload     bool
}

func (c Config) normalized() Config {
	if c.DefaultBuffer <= 0 {
		c.DefaultBuffer = 64
	}
	if c.MaxBuffer <= 0 {
		c.MaxBuffer = 4096
	}
	if c.DefaultBuffer > c.MaxBuffer {
		c.DefaultBuffer = c.MaxBuffer
	}
	if c.MaxPayloadBytes <= 0 {
		c.MaxPayloadBytes = 16 << 20
	}
	return c
}

// Bus is the transport-independent StreamBus contract.
type Bus interface {
	Publish(context.Context, Frame) (uint64, error)
	Subscribe(context.Context, SubscribeOptions) (*Subscription, error)
	Close() error
}

// SubscriptionStats is a point-in-time view of subscriber pressure.
type SubscriptionStats struct {
	Enqueued  uint64
	Delivered uint64
	Dropped   uint64
	Queued    int
}
