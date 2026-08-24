package watchbus

import "context"

// WatchBus provides a simple message bus for streaming events.
// Clients can publish messages to a key and watch for updates.
type WatchBus interface {
	// Publish sends the given data to all watchers of key.
	Publish(ctx context.Context, key string, data []byte) error
	// PublishPrefix sends the data to all watchers of keys matching prefix.
	PublishPrefix(ctx context.Context, prefix string, data []byte) error
	// Watch subscribes to messages for key. Returned channel receives
	// message payloads until the context is canceled or Unwatch is called.
	Watch(ctx context.Context, key string) (chan []byte, error)
	// SubscribePrefix subscribes to all messages for keys that have the given prefix.
	SubscribePrefix(ctx context.Context, prefix string) (chan []byte, error)
	// Unwatch stops delivering messages for key to ch.
	Unwatch(ctx context.Context, key string, ch chan []byte) error
}
