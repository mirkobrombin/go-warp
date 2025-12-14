package cache

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	stdErrors "errors"
)

// Codec defines methods for encoding and decoding values.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// JSONCodec implements Codec using encoding/json.
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// GobCodec implements Codec using encoding/gob.
type GobCodec struct{}

func (GobCodec) Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (GobCodec) Unmarshal(data []byte, v any) error {
	b := bytes.NewBuffer(data)
	dec := gob.NewDecoder(b)
	return dec.Decode(v)
}

// ByteCodec implements Codec for raw byte slices (Zero-Allocation/Zero-Copy friendly).
// It fails if the value is not []byte.
type ByteCodec struct{}

func (ByteCodec) Marshal(v any) ([]byte, error) {
	if b, ok := v.([]byte); ok {
		return b, nil
	}
	return nil, stdErrors.New("ByteCodec: value is not []byte")
}

func (ByteCodec) Unmarshal(data []byte, v any) error {
	if ptr, ok := v.(*[]byte); ok {
		*ptr = data
		return nil
	}
	return stdErrors.New("ByteCodec: v is not *[]byte")
}
