package mesh

import (
	"encoding/binary"
	"errors"
	"sync"
)

const (
	magicByte           = 0x57
	typeInvalidate byte = 0x01
	typeHeartbeat  byte = 0x02
	typeBatch      byte = 0x03
)

var (
	errInvalidMagic = errors.New("mesh: invalid magic byte")
	errShortBuffer  = errors.New("mesh: buffer too short")
)

var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 2048)
		},
	}
)

type packet struct {
	Magic   byte
	Type    byte
	NodeID  [16]byte
	KeyHash uint64
	KeyLen  uint16
	Key     []byte
	Keys    []string
}

func (p *packet) marshal(b []byte) (int, error) {
	if len(b) < 18 {
		return 0, errShortBuffer
	}

	b[0] = p.Magic
	b[1] = p.Type
	copy(b[2:18], p.NodeID[:])

	switch p.Type {
	case typeInvalidate, typeHeartbeat:
		if len(b) < 28+len(p.Key) {
			return 0, errShortBuffer
		}
		binary.BigEndian.PutUint64(b[18:26], p.KeyHash)
		binary.BigEndian.PutUint16(b[26:28], p.KeyLen)
		copy(b[28:], p.Key)
		return 28 + int(p.KeyLen), nil

	case typeBatch:
		if len(b) < 20 {
			return 0, errShortBuffer
		}
		binary.BigEndian.PutUint16(b[18:20], uint16(len(p.Keys)))
		curr := 20
		for _, k := range p.Keys {
			kLen := len(k)
			if len(b) < curr+2+kLen {
				return curr, errShortBuffer
			}
			binary.BigEndian.PutUint16(b[curr:curr+2], uint16(kLen))
			copy(b[curr+2:], k)
			curr += 2 + kLen
		}
		return curr, nil
	}

	return 18, nil
}

func (p *packet) unmarshal(b []byte) error {
	if len(b) < 18 {
		return errShortBuffer
	}

	p.Magic = b[0]
	if p.Magic != magicByte {
		return errInvalidMagic
	}

	p.Type = b[1]
	copy(p.NodeID[:], b[2:18])

	switch p.Type {
	case typeInvalidate, typeHeartbeat:
		if len(b) < 28 {
			return errShortBuffer
		}
		p.KeyHash = binary.BigEndian.Uint64(b[18:26])
		p.KeyLen = binary.BigEndian.Uint16(b[26:28])
		if len(b) < 28+int(p.KeyLen) {
			return errShortBuffer
		}
		if p.KeyLen > 0 {
			p.Key = make([]byte, p.KeyLen)
			copy(p.Key, b[28:28+int(p.KeyLen)])
		}

	case typeBatch:
		if len(b) < 20 {
			return errShortBuffer
		}
		count := int(binary.BigEndian.Uint16(b[18:20]))
		p.Keys = make([]string, 0, count)
		curr := 20
		for i := 0; i < count; i++ {
			if len(b) < curr+2 {
				return errShortBuffer
			}
			kLen := int(binary.BigEndian.Uint16(b[curr : curr+2]))
			if len(b) < curr+2+kLen {
				return errShortBuffer
			}
			p.Keys = append(p.Keys, string(b[curr+2:curr+2+kLen]))
			curr += 2 + kLen
		}
	}

	return nil
}
