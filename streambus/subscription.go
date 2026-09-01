package streambus

import (
	"context"
	"sync"
	"sync/atomic"
)

// Subscription exposes a bounded stream of frames.
type Subscription struct {
	id         uint64
	options    SubscribeOptions
	frames     chan Frame
	done       chan struct{}
	notify     chan struct{}
	space      chan struct{}
	queue      frameQueue
	mu         sync.Mutex
	closed     bool
	closeOnce  sync.Once
	unregister func()

	enqueued  atomic.Uint64
	delivered atomic.Uint64
	dropped   atomic.Uint64
}

func newSubscription(id uint64, options SubscribeOptions, unregister func()) *Subscription {
	s := &Subscription{
		id:         id,
		options:    options,
		frames:     make(chan Frame),
		done:       make(chan struct{}),
		notify:     make(chan struct{}, 1),
		space:      make(chan struct{}, 1),
		queue:      newFrameQueue(options.Buffer),
		unregister: unregister,
	}
	go s.run()
	return s
}

// ID is stable for the lifetime of the subscription.
func (s *Subscription) ID() uint64 { return s.id }

// Frames returns the delivery channel. It is closed when the subscription is
// closed or its parent context is canceled.
func (s *Subscription) Frames() <-chan Frame { return s.frames }

// Done is closed when the subscription stops.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Stats returns current counters and queue depth.
func (s *Subscription) Stats() SubscriptionStats {
	s.mu.Lock()
	queued := s.queue.len
	s.mu.Unlock()
	return SubscriptionStats{
		Enqueued:  s.enqueued.Load(),
		Delivered: s.delivered.Load(),
		Dropped:   s.dropped.Load(),
		Queued:    queued,
	}
}

// Close releases the subscription. It is safe to call more than once.
func (s *Subscription) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		if s.unregister != nil {
			s.unregister()
		}
	})
	return nil
}

func (s *Subscription) preload(frames []Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, frame := range frames {
		if !s.queue.push(frame) {
			_, _ = s.queue.popOldest()
			_ = s.queue.push(frame)
			s.dropped.Add(1)
		}
		s.enqueued.Add(1)
	}
	if s.queue.len > 0 {
		s.signal(s.notify)
	}
}

func (s *Subscription) enqueue(ctx context.Context, frame Frame) error {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return ErrSubscriptionDone
		}
		if s.queue.push(frame) {
			s.enqueued.Add(1)
			s.mu.Unlock()
			s.signal(s.notify)
			return nil
		}

		switch s.options.Overflow {
		case DropNewest:
			s.dropped.Add(1)
			s.mu.Unlock()
			return nil
		case DropOldest:
			_, _ = s.queue.popOldest()
			_ = s.queue.push(frame)
			s.dropped.Add(1)
			s.enqueued.Add(1)
			s.mu.Unlock()
			s.signal(s.notify)
			return nil
		case LatestOnly:
			dropped := s.queue.clear()
			_ = s.queue.push(frame)
			s.dropped.Add(uint64(dropped))
			s.enqueued.Add(1)
			s.mu.Unlock()
			s.signal(s.notify)
			return nil
		default:
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.done:
				return ErrSubscriptionDone
			case <-s.space:
			}
		}
	}
}

func (s *Subscription) run() {
	defer close(s.frames)
	for {
		select {
		case <-s.done:
			return
		case <-s.notify:
			for {
				s.mu.Lock()
				frame, ok := s.queue.pop()
				s.mu.Unlock()
				if !ok {
					break
				}
				s.signal(s.space)
				select {
				case <-s.done:
					return
				case s.frames <- frame:
					s.delivered.Add(1)
				}
			}
		}
	}
}

func (s *Subscription) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
