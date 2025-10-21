package watchbus

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSSEHandlerStream(t *testing.T) {
	bus := NewInMemory()
	srv := httptest.NewServer(SSEHandler(bus))
	defer srv.Close()

	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get(srv.URL + "?key=foo")
		if err != nil {
			t.Errorf("get: %v", err)
			return
		}
		respCh <- resp
	}()

	// wait for watcher registration
	for i := 0; i < 100; i++ {
		bus.mu.Lock()
		if len(bus.subs["foo"]) == 1 {
			bus.mu.Unlock()
			break
		}
		bus.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	if err := bus.Publish(context.Background(), "foo", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for response")
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	line = strings.TrimSpace(line)
	if line != "data: hello" {
		t.Fatalf("unexpected line %q", line)
	}
}

func TestSSEHandlerMissingKey(t *testing.T) {
	bus := NewInMemory()
	srv := httptest.NewServer(SSEHandler(bus))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSSEHandlerContextCancel(t *testing.T) {
	bus := NewInMemory()
	srv := httptest.NewServer(SSEHandler(bus))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?key=foo", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	respCh := make(chan struct{})
	go func() {
		_, _ = http.DefaultClient.Do(req)
		close(respCh)
	}()

	// wait for watcher registration
	for i := 0; i < 100; i++ {
		bus.mu.Lock()
		if len(bus.subs["foo"]) == 1 {
			bus.mu.Unlock()
			break
		}
		bus.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-respCh:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for request to end")
	}

	time.Sleep(50 * time.Millisecond)
	bus.mu.Lock()
	if len(bus.subs["foo"]) != 0 {
		bus.mu.Unlock()
		t.Fatalf("expected watcher removed")
	}
	bus.mu.Unlock()
}

type failingWriter struct {
	header http.Header
}

func newFailingWriter() *failingWriter {
	return &failingWriter{header: make(http.Header)}
}

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (w *failingWriter) WriteHeader(int)           {}
func (w *failingWriter) Flush()                    {}

func TestSSEHandlerWriteErrorUnwatches(t *testing.T) {
	bus := NewInMemory()
	handler := SSEHandler(bus)
	req := httptest.NewRequest(http.MethodGet, "/?key=foo", nil)
	resp := newFailingWriter()

	done := make(chan struct{})
	go func() {
		handler(resp, req)
		close(done)
	}()

	for i := 0; i < 100; i++ {
		bus.mu.Lock()
		if len(bus.subs["foo"]) == 1 {
			bus.mu.Unlock()
			break
		}
		bus.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}

	if err := bus.Publish(context.Background(), "foo", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit on write error")
	}

	time.Sleep(50 * time.Millisecond)

	bus.mu.Lock()
	if len(bus.subs["foo"]) != 0 {
		bus.mu.Unlock()
		t.Fatalf("expected watcher removed after write error")
	}
	bus.mu.Unlock()
}

func TestWebSocketHandlerStream(t *testing.T) {
	bus := NewInMemory()
	srv := httptest.NewServer(WebSocketHandler(bus))
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "?key=foo"
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := bus.Publish(context.Background(), "foo", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "hello" {
		t.Fatalf("unexpected %s", msg)
	}
}

func TestWebSocketHandlerMissingKey(t *testing.T) {
	bus := NewInMemory()
	srv := httptest.NewServer(WebSocketHandler(bus))
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	_, resp, err := websocket.DefaultDialer.Dial(u, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %v", resp)
	}
}

func TestWebSocketHandlerContextCancel(t *testing.T) {
	bus := NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewUnstartedServer(WebSocketHandler(bus))
	srv.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	srv.Start()
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "?key=foo"
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	bus.mu.Lock()
	if len(bus.subs["foo"]) != 1 {
		bus.mu.Unlock()
		t.Fatalf("expected watcher registered")
	}
	bus.mu.Unlock()

	cancel()
	time.Sleep(50 * time.Millisecond)

	bus.mu.Lock()
	if len(bus.subs["foo"]) != 0 {
		bus.mu.Unlock()
		t.Fatalf("expected watcher removed")
	}
	bus.mu.Unlock()
	conn.Close()
}
