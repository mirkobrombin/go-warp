package webtransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mirkobrombin/go-warp/v2/streambus"
	wt "github.com/quic-go/webtransport-go"
)

func TestHandlerRequiresConfiguration(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodConnect, "https://example.test/stream", nil)
	(&Handler{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerAuthenticatesBeforeUpgrade(t *testing.T) {
	bus := streambus.NewInMemory(streambus.Config{})
	t.Cleanup(func() { _ = bus.Close() })
	handler := &Handler{
		Server: &wt.Server{},
		Bus:    bus,
		Authenticate: func(*http.Request) error {
			return errors.New("denied")
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodConnect, "https://example.test/stream", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestDefaults(t *testing.T) {
	handler := &Handler{}
	if handler.maximum() != defaultMaxMessageBytes {
		t.Fatalf("maximum = %d", handler.maximum())
	}
	if handler.maximumDatagram() != defaultMaxDatagramBytes {
		t.Fatalf("maximum datagram = %d", handler.maximumDatagram())
	}
}

func TestClientAckIsTracked(t *testing.T) {
	bus := streambus.NewInMemory(streambus.Config{})
	t.Cleanup(func() { _ = bus.Close() })
	subscription, err := bus.Subscribe(context.Background(), streambus.SubscribeOptions{Topic: "x", Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	state := &sessionState{
		subscriptions: map[uint64]*streambus.Subscription{7: subscription},
		acks:          make(map[uint64]uint64),
	}
	if err := state.handleMessage(context.Background(), streambus.Message{
		Kind: streambus.MessageAck, SubscriptionID: 7, Ack: 42,
	}, false); err != nil {
		t.Fatal(err)
	}
	if state.acks[7] != 42 {
		t.Fatalf("ack = %d", state.acks[7])
	}
}
