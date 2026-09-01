package streambus

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type prefixNode struct {
	subs     map[uint64]*Subscription
	children map[byte]*prefixNode
}

func newPrefixNode() *prefixNode {
	return &prefixNode{
		subs:     make(map[uint64]*Subscription),
		children: make(map[byte]*prefixNode),
	}
}

type replayBuffer struct {
	frames []Frame
	next   int
	len    int
}

func newReplayBuffer(capacity int) *replayBuffer {
	return &replayBuffer{frames: make([]Frame, capacity)}
}

func (r *replayBuffer) add(frame Frame) {
	if len(r.frames) == 0 {
		return
	}
	r.frames[r.next] = frame
	r.next = (r.next + 1) % len(r.frames)
	if r.len < len(r.frames) {
		r.len++
	}
}

func (r *replayBuffer) last(count int) []Frame {
	if count > r.len {
		count = r.len
	}
	out := make([]Frame, 0, count)
	start := (r.next - count + len(r.frames)) % len(r.frames)
	for i := 0; i < count; i++ {
		out = append(out, r.frames[(start+i)%len(r.frames)])
	}
	return out
}

func (r *replayBuffer) after(sequence uint64) ([]Frame, bool) {
	if r.len == 0 {
		return nil, false
	}
	start := (r.next - r.len + len(r.frames)) % len(r.frames)
	oldest := r.frames[start].Sequence
	if r.len == len(r.frames) && sequence != 0 && sequence < oldest {
		return nil, true
	}
	out := make([]Frame, 0, r.len)
	for i := 0; i < r.len; i++ {
		frame := r.frames[(start+i)%len(r.frames)]
		if frame.Sequence > sequence {
			out = append(out, frame)
		}
	}
	return out, false
}

// InMemory is a bounded, transport-independent StreamBus implementation.
type InMemory struct {
	config Config
	mu     sync.RWMutex
	exact  map[string]map[uint64]*Subscription
	prefix *prefixNode
	replay map[string]*replayBuffer
	seq    atomic.Uint64
	subSeq atomic.Uint64
	closed bool
}

// NewInMemory creates a StreamBus using process-local memory.
func NewInMemory(config Config) *InMemory {
	return &InMemory{
		config: config.normalized(),
		exact:  make(map[string]map[uint64]*Subscription),
		prefix: newPrefixNode(),
		replay: make(map[string]*replayBuffer),
	}
}

// Publish fans a frame out to all exact and prefix subscribers.
func (b *InMemory) Publish(ctx context.Context, frame Frame) (uint64, error) {
	if frame.Topic == "" {
		return 0, ErrInvalidTopic
	}
	if len(frame.Payload) > b.config.MaxPayloadBytes {
		return 0, ErrPayloadTooLarge
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	if b.config.CopyPayload {
		frame.Payload = append([]byte(nil), frame.Payload...)
	}
	// Sequence ownership stays with the bus. Transport clients cannot inject or
	// reuse sequence numbers from another session.
	frame.Sequence = b.seq.Add(1)
	if frame.Timestamp.IsZero() {
		frame.Timestamp = time.Now().UTC()
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, ErrClosed
	}
	targets := make(map[uint64]*Subscription)
	for id, sub := range b.exact[frame.Topic] {
		targets[id] = sub
	}
	node := b.prefix
	for i := 0; i < len(frame.Topic); i++ {
		next := node.children[frame.Topic[i]]
		if next == nil {
			break
		}
		node = next
		for id, sub := range node.subs {
			targets[id] = sub
		}
	}
	if b.config.ReplayCapacity > 0 {
		journal := b.replay[frame.Topic]
		if journal == nil {
			journal = newReplayBuffer(b.config.ReplayCapacity)
			b.replay[frame.Topic] = journal
		}
		journal.add(frame)
	}
	b.mu.Unlock()

	for _, sub := range targets {
		if err := sub.enqueue(ctx, frame); err != nil && err != ErrSubscriptionDone {
			return frame.Sequence, err
		}
	}
	return frame.Sequence, nil
}

// Subscribe creates a bounded exact-topic or prefix subscription.
func (b *InMemory) Subscribe(ctx context.Context, options SubscribeOptions) (*Subscription, error) {
	if options.Topic == "" {
		return nil, ErrInvalidTopic
	}
	if options.Buffer == 0 {
		options.Buffer = b.config.DefaultBuffer
	}
	if options.Buffer < 1 || options.Buffer > b.config.MaxBuffer {
		return nil, ErrInvalidBuffer
	}
	if options.Replay < 0 {
		options.Replay = 0
	}
	if options.Prefix {
		options.Replay = 0
		options.Since = 0
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	id := b.subSeq.Add(1)
	var sub *Subscription
	sub = newSubscription(id, options, func() { b.remove(sub) })

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = sub.Close()
		return nil, ErrClosed
	}
	var replay []Frame
	if journal := b.replay[options.Topic]; journal != nil {
		if options.Since > 0 {
			var gap bool
			replay, gap = journal.after(options.Since)
			if gap || len(replay) > options.Buffer {
				b.mu.Unlock()
				_ = sub.Close()
				return nil, ErrReplayUnavailable
			}
		} else if options.Replay > 0 {
			replay = journal.last(options.Replay)
		}
	} else if options.Since > 0 && b.config.ReplayCapacity == 0 {
		b.mu.Unlock()
		_ = sub.Close()
		return nil, ErrReplayUnavailable
	}
	if len(replay) > 0 {
		sub.preload(replay)
	}
	if options.Prefix {
		node := b.prefix
		for i := 0; i < len(options.Topic); i++ {
			child := node.children[options.Topic[i]]
			if child == nil {
				child = newPrefixNode()
				node.children[options.Topic[i]] = child
			}
			node = child
		}
		node.subs[id] = sub
	} else {
		if b.exact[options.Topic] == nil {
			b.exact[options.Topic] = make(map[uint64]*Subscription)
		}
		b.exact[options.Topic][id] = sub
	}
	b.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			_ = sub.Close()
		case <-sub.Done():
		}
	}()
	return sub, nil
}

func (b *InMemory) remove(sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	options := sub.options
	if options.Prefix {
		node := b.prefix
		for i := 0; i < len(options.Topic); i++ {
			node = node.children[options.Topic[i]]
			if node == nil {
				return
			}
		}
		delete(node.subs, sub.id)
		return
	}
	subs := b.exact[options.Topic]
	delete(subs, sub.id)
	if len(subs) == 0 {
		delete(b.exact, options.Topic)
	}
}

// Close stops every subscription and rejects future operations.
func (b *InMemory) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	unique := make(map[uint64]*Subscription)
	for _, subscribers := range b.exact {
		for id, sub := range subscribers {
			unique[id] = sub
		}
	}
	collectPrefixSubscriptions(b.prefix, unique)
	b.exact = make(map[string]map[uint64]*Subscription)
	b.prefix = newPrefixNode()
	b.mu.Unlock()

	for _, sub := range unique {
		_ = sub.Close()
	}
	return nil
}

func collectPrefixSubscriptions(node *prefixNode, dst map[uint64]*Subscription) {
	for id, sub := range node.subs {
		dst[id] = sub
	}
	for _, child := range node.children {
		collectPrefixSubscriptions(child, dst)
	}
}
