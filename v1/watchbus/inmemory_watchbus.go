package watchbus

import (
	"context"
	"sync"
)

// prefixNode represents a node in the prefix trie used for prefix
// subscriptions and prefix lookups for key watchers. Each node keeps the
// watchers of keys that share its prefix as well as subscribers that have
// explicitly subscribed to that prefix.
type prefixNode struct {
	subs     []chan []byte
	watchers map[chan []byte]struct{}
	children map[rune]*prefixNode
}

func newPrefixNode() *prefixNode {
	return &prefixNode{
		watchers: make(map[chan []byte]struct{}),
		children: make(map[rune]*prefixNode),
	}
}

// InMemoryWatchBus is an in-memory implementation of WatchBus.
type InMemoryWatchBus struct {
	mu   sync.RWMutex
	subs map[string][]chan []byte
	root *prefixNode
}

// NewInMemory creates a new InMemoryWatchBus.
func NewInMemory() *InMemoryWatchBus {
	return &InMemoryWatchBus{
		subs: make(map[string][]chan []byte),
		root: newPrefixNode(),
	}
}

// Publish sends data to all watchers of key.
func (b *InMemoryWatchBus) Publish(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	targets := make(map[chan []byte]struct{})

	b.mu.RLock()
	for _, ch := range b.subs[key] {
		targets[ch] = struct{}{}
	}
	node := b.root
	for _, r := range key {
		next, ok := node.children[r]
		if !ok {
			break
		}
		node = next
		for _, ch := range node.subs {
			targets[ch] = struct{}{}
		}
	}
	b.mu.RUnlock()

	for ch := range targets {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		safeSend(ch, data)
	}
	return nil
}

// PublishPrefix sends data to all watchers of keys with the given prefix.
func (b *InMemoryWatchBus) PublishPrefix(ctx context.Context, prefix string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	targets := make(map[chan []byte]struct{})

	b.mu.RLock()
	node := b.root
	for _, r := range prefix {
		next, ok := node.children[r]
		if !ok {
			node = nil
			break
		}
		node = next
	}
	if node != nil {
		for ch := range node.watchers {
			targets[ch] = struct{}{}
		}
		for _, ch := range node.subs {
			targets[ch] = struct{}{}
		}
	}
	b.mu.RUnlock()

	for ch := range targets {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		safeSend(ch, data)
	}
	return nil
}

func safeSend(ch chan []byte, data []byte) {
	defer func() {
		if r := recover(); r != nil {
			// channel has been closed concurrently; drop the message
		}
	}()
	select {
	case ch <- data:
	default:
	}
}

// Watch subscribes to key and returns a channel receiving messages.
func (b *InMemoryWatchBus) Watch(ctx context.Context, key string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	node := b.root
	for _, r := range key {
		if node.children[r] == nil {
			node.children[r] = newPrefixNode()
		}
		node = node.children[r]
		node.watchers[ch] = struct{}{}
	}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), key, ch)
	}()
	return ch, nil
}

// SubscribePrefix subscribes to all keys with the given prefix.
func (b *InMemoryWatchBus) SubscribePrefix(ctx context.Context, prefix string) (chan []byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ch := make(chan []byte, 1)
	b.mu.Lock()
	node := b.root
	for _, r := range prefix {
		if node.children[r] == nil {
			node.children[r] = newPrefixNode()
		}
		node = node.children[r]
	}
	node.subs = append(node.subs, ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unwatch(context.Background(), prefix, ch)
	}()
	return ch, nil
}

// Unwatch removes the channel from key watchers.
func (b *InMemoryWatchBus) Unwatch(ctx context.Context, key string, ch chan []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	b.mu.Lock()
	subs := b.subs[key]
	removed := false
	for i, c := range subs {
		if c == ch {
			subs[i] = subs[len(subs)-1]
			subs = subs[:len(subs)-1]
			b.subs[key] = subs
			close(c)
			removed = true
			break
		}
	}
	if removed {
		if len(subs) == 0 {
			delete(b.subs, key)
		}
		node := b.root
		for _, r := range key {
			next, ok := node.children[r]
			if !ok {
				break
			}
			delete(next.watchers, ch)
			node = next
		}
		b.mu.Unlock()
		return nil
	}

	node := b.root
	for _, r := range key {
		next, ok := node.children[r]
		if !ok {
			node = nil
			break
		}
		node = next
	}
	if node != nil {
		subs := node.subs
		for i, c := range subs {
			if c == ch {
				subs[i] = subs[len(subs)-1]
				subs = subs[:len(subs)-1]
				node.subs = subs
				close(c)
				break
			}
		}
	}
	b.mu.Unlock()
	return nil
}
