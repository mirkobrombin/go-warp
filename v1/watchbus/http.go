package watchbus

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

// SSEHandler streams WatchBus events over Server-Sent Events.
// The watched key is taken from the "key" query parameter.
func SSEHandler(bus WatchBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "missing key", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		ch, err := bus.Watch(ctx, key)
		if err != nil {
			cancel()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() {
			cancel()
			_ = bus.Unwatch(context.Background(), key, ch)
		}()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", http.StatusInternalServerError)
			return
		}
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
					return
				}
				flusher.Flush()
			case <-ctx.Done():
				return
			}
		}
	}
}

var upgrader = websocket.Upgrader{}

// WebSocketHandler streams WatchBus events over WebSocket.
// The watched key is taken from the "key" query parameter.
func WebSocketHandler(bus WatchBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "missing key", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(r.Context())
		ch, err := bus.Watch(ctx, key)
		if err != nil {
			cancel()
			return
		}
		defer func() {
			cancel()
			_ = bus.Unwatch(context.Background(), key, ch)
		}()
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}
