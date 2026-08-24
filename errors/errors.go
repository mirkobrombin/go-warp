package errors

import "errors"

var (
	ErrTimeout          = errors.New("timeout")
	ErrConnectionClosed = errors.New("connection closed")
)
