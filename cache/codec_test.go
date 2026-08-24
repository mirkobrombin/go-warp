package cache

import (
	"bytes"
	"testing"
)

func TestByteCodec(t *testing.T) {
	codec := ByteCodec{}

	t.Run("Marshal []byte", func(t *testing.T) {
		input := []byte("hello")
		data, err := codec.Marshal(input)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if !bytes.Equal(data, input) {
			t.Fatalf("Marshal returned unexpected data: got %s, want %s", data, input)
		}
	})

	t.Run("Marshal Invalid Type", func(t *testing.T) {
		input := "string"
		_, err := codec.Marshal(input)
		if err == nil {
			t.Fatal("Marshal expected error for non-[]byte input")
		}
	})

	t.Run("Unmarshal *[]byte", func(t *testing.T) {
		input := []byte("world")
		var output []byte
		if err := codec.Unmarshal(input, &output); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		if !bytes.Equal(output, input) {
			t.Fatalf("Unmarshal returned unexpected data: got %s, want %s", output, input)
		}
	})

	t.Run("Unmarshal Invalid Type", func(t *testing.T) {
		input := []byte("world")
		var output string
		if err := codec.Unmarshal(input, &output); err == nil {
			t.Fatal("Unmarshal expected error for non-*[]byte target")
		}
	})
}
