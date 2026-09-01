package streambus

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestMessageRoundTrip(t *testing.T) {
	want := Message{
		Kind:           MessageSubscribe,
		ID:             41,
		SubscriptionID: 9,
		Ack:            20,
		Frame: Frame{
			Sequence:    21,
			Timestamp:   time.Unix(123, 456).UTC(),
			Topic:       "component:chart",
			Payload:     []byte{0, 1, 2, 255},
			Reliability: Unreliable,
			Priority:    PriorityInteractive,
		},
		Options: SubscribeOptions{
			Topic:    "component:chart",
			Prefix:   true,
			Buffer:   128,
			Overflow: LatestOnly,
			Replay:   3,
			Since:    99,
		},
		Error: "example",
	}
	encoded, err := EncodeMessage(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFramedMessages(t *testing.T) {
	var stream bytes.Buffer
	want := []Message{
		{Kind: MessagePing, ID: 1},
		{Kind: MessageData, ID: 2, Frame: Frame{Topic: "x", Payload: []byte("payload")}},
	}
	for _, message := range want {
		if err := WriteMessage(&stream, message); err != nil {
			t.Fatal(err)
		}
	}
	reader := NewReader(&stream, 1024)
	for i := range want {
		got, err := reader.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Fatalf("message %d = %#v, want %#v", i, got, want[i])
		}
	}
}

func TestInvalidWireData(t *testing.T) {
	if _, err := DecodeMessage(nil); !errors.Is(err, ErrInvalidWireMessage) {
		t.Fatalf("empty message = %v", err)
	}
	data, err := EncodeMessage(Message{Kind: MessagePing})
	if err != nil {
		t.Fatal(err)
	}
	data[0]++
	if _, err := DecodeMessage(data); !errors.Is(err, ErrInvalidWireVersion) {
		t.Fatalf("wrong version = %v", err)
	}
}

func TestWriteMessageHandlesShortWrites(t *testing.T) {
	writer := &shortWriter{limit: 2}
	want := Message{Kind: MessageData, Frame: Frame{Topic: "topic", Payload: []byte("payload")}}
	if err := WriteMessage(writer, want); err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(bytes.NewReader(writer.data), 1024).ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}

type shortWriter struct {
	data  []byte
	limit int
}

func (w *shortWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, io.ErrShortWrite
	}
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	w.data = append(w.data, data...)
	return len(data), nil
}
