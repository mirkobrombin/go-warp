package webtransport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/mirkobrombin/go-warp/v2/streambus"
	wt "github.com/quic-go/webtransport-go"
)

const defaultMaxMessageBytes = 64 << 20
const defaultMaxDatagramBytes = 1200

// Handler upgrades HTTP/3 requests and binds WebTransport sessions to a Bus.
type Handler struct {
	Server          *wt.Server
	Bus             streambus.Bus
	Authenticate    func(*http.Request) error
	MaxMessageBytes int
	// MaxDatagramBytes bounds one encoded datagram. Larger unreliable frames
	// fall back to the subscription's reliable stream instead of being lost.
	MaxDatagramBytes int
	OnError          func(error)
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h.Server == nil || h.Bus == nil {
		http.Error(writer, "streambus webtransport is not configured", http.StatusServiceUnavailable)
		return
	}
	if h.Authenticate != nil {
		if err := h.Authenticate(request); err != nil {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	session, err := h.Server.Upgrade(writer, request)
	if err != nil {
		h.report(err)
		return
	}
	if err := h.ServeSession(session.Context(), session); err != nil && !errors.Is(err, context.Canceled) {
		h.report(err)
	}
}

// ServeSession runs the StreamBus protocol over an upgraded session.
func (h *Handler) ServeSession(parent context.Context, session *wt.Session) error {
	if h.Bus == nil {
		return errors.New("streambus webtransport: nil bus")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer session.CloseWithError(0, "")
	control, err := session.AcceptStream(ctx)
	if err != nil {
		return fmt.Errorf("accept control stream: %w", err)
	}
	defer control.Close()

	state := &sessionState{
		handler:       h,
		session:       session,
		control:       control,
		subscriptions: make(map[uint64]*streambus.Subscription),
		acks:          make(map[uint64]uint64),
	}
	defer state.closeSubscriptions()

	errCh := make(chan error, 2)
	go func() { errCh <- state.readControl(ctx) }()
	workers := 1
	datagrams := session.SessionState().ConnectionState.SupportsDatagrams
	if datagrams.Local && datagrams.Remote {
		workers++
		go func() { errCh <- state.readDatagrams(ctx) }()
	}
	err = <-errCh
	cancel()
	_ = session.CloseWithError(0, "")
	for i := 1; i < workers; i++ {
		<-errCh
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func (h *Handler) maximum() int {
	if h.MaxMessageBytes <= 0 {
		return defaultMaxMessageBytes
	}
	return h.MaxMessageBytes
}

func (h *Handler) maximumDatagram() int {
	if h.MaxDatagramBytes <= 0 {
		return defaultMaxDatagramBytes
	}
	return h.MaxDatagramBytes
}

func (h *Handler) report(err error) {
	if h.OnError != nil {
		h.OnError(err)
	}
}

type sessionState struct {
	handler       *Handler
	session       *wt.Session
	control       *wt.Stream
	writeMu       sync.Mutex
	subsMu        sync.Mutex
	subscriptions map[uint64]*streambus.Subscription
	acks          map[uint64]uint64
}

func (s *sessionState) readControl(ctx context.Context) error {
	reader := streambus.NewReader(s.control, s.handler.maximum())
	for {
		message, err := reader.ReadMessage()
		if err != nil {
			return err
		}
		if err := s.handleMessage(ctx, message, false); err != nil {
			if writeErr := s.writeControl(streambus.Message{Kind: streambus.MessageError, ID: message.ID, Error: err.Error()}); writeErr != nil {
				return writeErr
			}
		}
	}
}

func (s *sessionState) readDatagrams(ctx context.Context) error {
	for {
		data, err := s.session.ReceiveDatagram(ctx)
		if err != nil {
			return err
		}
		if len(data) > s.handler.maximum() {
			continue
		}
		message, err := streambus.DecodeMessage(data)
		if err != nil {
			continue
		}
		if err := s.handleMessage(ctx, message, true); err != nil {
			s.handler.report(err)
		}
	}
}

func (s *sessionState) handleMessage(ctx context.Context, message streambus.Message, datagram bool) error {
	switch message.Kind {
	case streambus.MessageSubscribe:
		if datagram {
			return errors.New("subscribe requires the reliable control stream")
		}
		return s.subscribe(ctx, message)
	case streambus.MessageUnsubscribe:
		if datagram {
			return errors.New("unsubscribe requires the reliable control stream")
		}
		return s.unsubscribe(message.SubscriptionID)
	case streambus.MessagePublish:
		if datagram {
			message.Frame.Reliability = streambus.Unreliable
		}
		sequence, err := s.handler.Bus.Publish(ctx, message.Frame)
		if err != nil {
			return err
		}
		if !datagram {
			return s.writeControl(streambus.Message{Kind: streambus.MessageAck, ID: message.ID, Ack: sequence})
		}
		return nil
	case streambus.MessageAck:
		if datagram {
			return errors.New("ack requires the reliable control stream")
		}
		s.subsMu.Lock()
		if _, exists := s.subscriptions[message.SubscriptionID]; exists {
			s.acks[message.SubscriptionID] = message.Ack
		}
		s.subsMu.Unlock()
		return nil
	case streambus.MessagePing:
		if datagram {
			data, err := streambus.EncodeMessage(streambus.Message{Kind: streambus.MessagePong, ID: message.ID})
			if err != nil {
				return err
			}
			return s.session.SendDatagram(data)
		}
		return s.writeControl(streambus.Message{Kind: streambus.MessagePong, ID: message.ID})
	default:
		return fmt.Errorf("unsupported message kind %d", message.Kind)
	}
}

func (s *sessionState) subscribe(ctx context.Context, message streambus.Message) error {
	if message.SubscriptionID == 0 {
		return errors.New("subscription id is required")
	}
	s.subsMu.Lock()
	if _, exists := s.subscriptions[message.SubscriptionID]; exists {
		s.subsMu.Unlock()
		return errors.New("subscription id already exists")
	}
	s.subsMu.Unlock()

	options := message.Options
	if options.Topic == "" {
		options.Topic = message.Frame.Topic
	}
	subscription, err := s.handler.Bus.Subscribe(ctx, options)
	if err != nil {
		return err
	}
	s.subsMu.Lock()
	s.subscriptions[message.SubscriptionID] = subscription
	s.acks[message.SubscriptionID] = message.Options.Since
	s.subsMu.Unlock()
	go func() {
		if err := s.pump(ctx, message.SubscriptionID, subscription); err != nil && !errors.Is(err, context.Canceled) {
			s.handler.report(err)
		}
	}()
	return s.writeControl(streambus.Message{Kind: streambus.MessageAck, ID: message.ID, SubscriptionID: message.SubscriptionID})
}

func (s *sessionState) unsubscribe(id uint64) error {
	s.subsMu.Lock()
	subscription := s.subscriptions[id]
	delete(s.subscriptions, id)
	delete(s.acks, id)
	s.subsMu.Unlock()
	if subscription == nil {
		return errors.New("unknown subscription")
	}
	return subscription.Close()
}

func (s *sessionState) pump(ctx context.Context, id uint64, subscription *streambus.Subscription) error {
	defer func() {
		s.subsMu.Lock()
		delete(s.subscriptions, id)
		delete(s.acks, id)
		s.subsMu.Unlock()
		_ = subscription.Close()
	}()

	var reliable *wt.SendStream
	for {
		select {
		case <-ctx.Done():
			if reliable != nil {
				_ = reliable.Close()
			}
			return ctx.Err()
		case frame, ok := <-subscription.Frames():
			if !ok {
				if reliable != nil {
					_ = reliable.Close()
				}
				return nil
			}
			message := streambus.Message{Kind: streambus.MessageData, SubscriptionID: id, Frame: frame}
			if frame.Reliability == streambus.Unreliable {
				data, err := streambus.EncodeMessage(message)
				if err != nil {
					return err
				}
				state := s.session.SessionState().ConnectionState.SupportsDatagrams
				if len(data) <= s.handler.maximumDatagram() && state.Local && state.Remote {
					if err := s.session.SendDatagram(data); err == nil {
						continue
					}
				}
			}
			if reliable == nil {
				stream, err := s.session.OpenUniStreamSync(ctx)
				if err != nil {
					return err
				}
				reliable = stream
				if err := streambus.WriteMessage(reliable, streambus.Message{Kind: streambus.MessageStreamOpen, SubscriptionID: id}); err != nil {
					_ = reliable.Close()
					return err
				}
			}
			if err := streambus.WriteMessage(reliable, message); err != nil {
				_ = reliable.Close()
				return err
			}
		}
	}
}

func (s *sessionState) writeControl(message streambus.Message) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return streambus.WriteMessage(s.control, message)
}

func (s *sessionState) closeSubscriptions() {
	s.subsMu.Lock()
	subscriptions := make([]*streambus.Subscription, 0, len(s.subscriptions))
	for _, subscription := range s.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	s.subscriptions = make(map[uint64]*streambus.Subscription)
	s.acks = make(map[uint64]uint64)
	s.subsMu.Unlock()
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
}
