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
		ch, err := bus.Watch(r.Context(), key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				_ = bus.Unwatch(context.Background(), key, ch)
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
		ch, err := bus.Watch(r.Context(), key)
		if err != nil {
			return
		}
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-r.Context().Done():
				_ = bus.Unwatch(context.Background(), key, ch)
				return
			}
		}
	}
}
