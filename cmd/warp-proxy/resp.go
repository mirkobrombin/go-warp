package main

import (
	"bufio"
	"errors"
	"io"
	"strconv"
)

var (
	errInvalidProtocol = errors.New("ERR protocol error")
	errInvalidInt      = errors.New("ERR invalid integer")
)

// RESPReader is a zero-allocation RESP parser wrapper around bufio.Reader.
type RESPReader struct {
	rd *bufio.Reader
}

func NewRESPReader(rd *bufio.Reader) *RESPReader {
	return &RESPReader{rd: rd}
}

func (r *RESPReader) ReadCommand() ([][]byte, error) {
	line, err := r.rd.ReadSlice('\n')
	if err != nil {
		return nil, err
	}

	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errInvalidProtocol
	}

	if line[0] != '*' {
		// Minimal inline command support (e.g. "PING\r\n")
		return [][]byte{line[:len(line)-2]}, nil
	}

	countStr := string(line[1 : len(line)-2])
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, errInvalidProtocol
	}

	args := make([][]byte, 0, count)

	for i := 0; i < count; i++ {
		line, err = r.rd.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		if line[0] != '$' {
			return nil, errInvalidProtocol
		}

		lenStr := string(line[1 : len(line)-2])
		length, err := strconv.Atoi(lenStr)
		if err != nil {
			return nil, errInvalidProtocol
		}

		if length == -1 {
			args = append(args, nil)
			continue
		}

		data := make([]byte, length)
		_, err = io.ReadFull(r.rd, data)
		if err != nil {
			return nil, err
		}

		_, err = r.rd.Discard(2)
		if err != nil {
			return nil, err
		}

		args = append(args, data)
	}

	return args, nil
}

// RESPWriter handles writing RESP responses without fmt.
type RESPWriter struct {
	wr      *bufio.Writer
	scratch []byte // reused buffer for integer formatting
}

func NewRESPWriter(wr *bufio.Writer) *RESPWriter {
	return &RESPWriter{
		wr:      wr,
		scratch: make([]byte, 0, 32),
	}
}

func (w *RESPWriter) WriteError(msg string) {
	w.wr.WriteByte('-')
	w.wr.WriteString(msg)
	w.wr.Write([]byte("\r\n"))
}

func (w *RESPWriter) WriteSimpleString(msg string) {
	w.wr.WriteByte('+')
	w.wr.WriteString(msg)
	w.wr.Write([]byte("\r\n"))
}

func (w *RESPWriter) WriteBulk(data []byte) {
	w.wr.WriteByte('$')
	w.scratch = w.scratch[:0]
	w.scratch = strconv.AppendInt(w.scratch, int64(len(data)), 10)
	w.wr.Write(w.scratch)
	w.wr.Write([]byte("\r\n"))
	w.wr.Write(data)
	w.wr.Write([]byte("\r\n"))
}

func (w *RESPWriter) WriteNull() {
	w.wr.Write([]byte("$-1\r\n"))
}

func (w *RESPWriter) WriteInt(n int64) {
	w.wr.WriteByte(':')
	w.scratch = w.scratch[:0]
	w.scratch = strconv.AppendInt(w.scratch, n, 10)
	w.wr.Write(w.scratch)
	w.wr.Write([]byte("\r\n"))
}

func (w *RESPWriter) Flush() error {
	return w.wr.Flush()
}
