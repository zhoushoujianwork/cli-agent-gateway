package acp

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorKind string

const (
	ErrTimeout   ErrorKind = "timeout"
	ErrTransport ErrorKind = "transport"
	ErrProtocol  ErrorKind = "protocol"
)

type ACPError struct {
	Kind   ErrorKind
	Method string
	Cause  error
	Detail string
}

func (e *ACPError) Error() string {
	method := strings.TrimSpace(e.Method)
	if method == "" {
		method = "unknown"
	}
	if e.Cause != nil {
		return fmt.Sprintf("acp %s error method=%s: %v", e.Kind, method, e.Cause)
	}
	if strings.TrimSpace(e.Detail) != "" {
		return fmt.Sprintf("acp %s error method=%s: %s", e.Kind, method, strings.TrimSpace(e.Detail))
	}
	return fmt.Sprintf("acp %s error method=%s", e.Kind, method)
}

func (e *ACPError) Unwrap() error {
	return e.Cause
}

func wrapACPError(method string, err error) error {
	if err == nil {
		return nil
	}
	var ae *ACPError
	if errors.As(err, &ae) {
		return err
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	kind := ErrTransport
	if strings.Contains(msg, "timeout") {
		kind = ErrTimeout
	}
	return &ACPError{
		Kind:   kind,
		Method: method,
		Cause:  err,
	}
}

func newProtocolError(method, detail string) error {
	return &ACPError{
		Kind:   ErrProtocol,
		Method: method,
		Detail: detail,
	}
}
