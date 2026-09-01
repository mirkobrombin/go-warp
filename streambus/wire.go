package streambus

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

const WireVersion byte = 1

var (
	ErrInvalidWireVersion = errors.New("streambus: unsupported wire version")
	ErrInvalidWireMessage = errors.New("streambus: invalid wire message")
)

// MessageKind identifies StreamBus control and data messages on a transport.
type MessageKind uint8

const (
	MessageData MessageKind = iota + 1
	MessagePublish
	MessageSubscribe
	MessageUnsubscribe
	MessageAck
	MessageError
	MessageStreamOpen
	MessagePing
	MessagePong
)

// Message is the binary protocol envelope shared by transport adapters.
type Message struct {
	Kind           MessageKind
	ID             uint64
	SubscriptionID uint64
	Ack            uint64
	Frame          Frame
	Options        SubscribeOptions
	Error          string
}

// EncodeMessage encodes one message without stream-length framing. It is
// suitable for a WebTransport datagram.
func EncodeMessage(message Message) ([]byte, error) {
	topic := message.Frame.Topic
	if message.Kind == MessageSubscribe && topic == "" {
		topic = message.Options.Topic
	}
	if len(topic) > 1<<20 || len(message.Frame.Payload) > 64<<20 || len(message.Error) > 1<<20 {
		return nil, ErrInvalidWireMessage
	}
	var out bytes.Buffer
	out.WriteByte(WireVersion)
	out.WriteByte(byte(message.Kind))
	putUvarint(&out, message.ID)
	putUvarint(&out, message.SubscriptionID)
	putUvarint(&out, message.Ack)
	putUvarint(&out, message.Frame.Sequence)
	timestamp := int64(0)
	if !message.Frame.Timestamp.IsZero() {
		timestamp = message.Frame.Timestamp.UnixNano()
	}
	putVarint(&out, timestamp)
	out.WriteByte(byte(message.Frame.Reliability))
	out.WriteByte(byte(message.Frame.Priority))
	putBytes(&out, []byte(topic))
	putBytes(&out, message.Frame.Payload)
	if message.Options.Prefix {
		out.WriteByte(1)
	} else {
		out.WriteByte(0)
	}
	putUvarint(&out, uint64(message.Options.Buffer))
	out.WriteByte(byte(message.Options.Overflow))
	putUvarint(&out, uint64(message.Options.Replay))
	putUvarint(&out, message.Options.Since)
	putBytes(&out, []byte(message.Error))
	return out.Bytes(), nil
}

// DecodeMessage decodes one unframed message.
func DecodeMessage(data []byte) (Message, error) {
	reader := bytes.NewReader(data)
	version, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if version != WireVersion {
		return Message{}, ErrInvalidWireVersion
	}
	kind, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	message := Message{Kind: MessageKind(kind)}
	if message.Kind < MessageData || message.Kind > MessagePong {
		return Message{}, ErrInvalidWireMessage
	}
	if message.ID, err = binary.ReadUvarint(reader); err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if message.SubscriptionID, err = binary.ReadUvarint(reader); err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if message.Ack, err = binary.ReadUvarint(reader); err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if message.Frame.Sequence, err = binary.ReadUvarint(reader); err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	timestamp, err := binary.ReadVarint(reader)
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if timestamp != 0 {
		message.Frame.Timestamp = time.Unix(0, timestamp).UTC()
	}
	reliability, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	message.Frame.Reliability = Reliability(reliability)
	if message.Frame.Reliability > Unreliable {
		return Message{}, ErrInvalidWireMessage
	}
	priority, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	message.Frame.Priority = Priority(priority)
	if message.Frame.Priority > PriorityCritical {
		return Message{}, ErrInvalidWireMessage
	}
	topic, err := readBytes(reader, 1<<20)
	if err != nil {
		return Message{}, err
	}
	message.Frame.Topic = string(topic)
	if message.Frame.Payload, err = readBytes(reader, 64<<20); err != nil {
		return Message{}, err
	}
	prefix, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	if message.Kind == MessageSubscribe {
		message.Options.Topic = message.Frame.Topic
	}
	message.Options.Prefix = prefix == 1
	buffer, err := binary.ReadUvarint(reader)
	if err != nil || buffer > uint64(^uint(0)>>1) {
		return Message{}, ErrInvalidWireMessage
	}
	message.Options.Buffer = int(buffer)
	overflow, err := reader.ReadByte()
	if err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	message.Options.Overflow = OverflowPolicy(overflow)
	if message.Options.Overflow > LatestOnly {
		return Message{}, ErrInvalidWireMessage
	}
	replay, err := binary.ReadUvarint(reader)
	if err != nil || replay > uint64(^uint(0)>>1) {
		return Message{}, ErrInvalidWireMessage
	}
	message.Options.Replay = int(replay)
	if message.Options.Since, err = binary.ReadUvarint(reader); err != nil {
		return Message{}, ErrInvalidWireMessage
	}
	errorText, err := readBytes(reader, 1<<20)
	if err != nil {
		return Message{}, err
	}
	message.Error = string(errorText)
	if reader.Len() != 0 {
		return Message{}, ErrInvalidWireMessage
	}
	return message, nil
}

// WriteMessage writes a length-delimited message to a reliable stream.
func WriteMessage(writer io.Writer, message Message) error {
	data, err := EncodeMessage(message)
	if err != nil {
		return err
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(len(data)))
	if err := writeAll(writer, prefix[:n]); err != nil {
		return err
	}
	return writeAll(writer, data)
}

// Reader decodes length-delimited StreamBus messages.
type Reader struct {
	reader *bufio.Reader
	max    int
}

// NewReader creates a stream decoder. A non-positive maximum uses 64 MiB.
func NewReader(reader io.Reader, maximum int) *Reader {
	if maximum <= 0 {
		maximum = 64 << 20
	}
	return &Reader{reader: bufio.NewReader(reader), max: maximum}
}

// ReadMessage reads one length-delimited message.
func (r *Reader) ReadMessage() (Message, error) {
	size, err := binary.ReadUvarint(r.reader)
	if err != nil {
		return Message{}, err
	}
	if size > uint64(r.max) {
		return Message{}, fmt.Errorf("%w: frame length %d exceeds %d", ErrInvalidWireMessage, size, r.max)
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(r.reader, data); err != nil {
		return Message{}, err
	}
	return DecodeMessage(data)
}

func putUvarint(out *bytes.Buffer, value uint64) {
	var data [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(data[:], value)
	out.Write(data[:n])
}

func putVarint(out *bytes.Buffer, value int64) {
	var data [binary.MaxVarintLen64]byte
	n := binary.PutVarint(data[:], value)
	out.Write(data[:n])
}

func putBytes(out *bytes.Buffer, data []byte) {
	putUvarint(out, uint64(len(data)))
	out.Write(data)
}

func readBytes(reader *bytes.Reader, maximum uint64) ([]byte, error) {
	size, err := binary.ReadUvarint(reader)
	if err != nil || size > maximum || size > uint64(reader.Len()) {
		return nil, ErrInvalidWireMessage
	}
	if size == 0 {
		return nil, nil
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, ErrInvalidWireMessage
	}
	return data, nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
