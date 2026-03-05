package acp

import (
	"errors"
	"testing"
)

func TestWrapACPError_ClassifiesTimeout(t *testing.T) {
	err := wrapACPError("session/new", errors.New("request timeout method=session/new"))
	var ae *ACPError
	if !errors.As(err, &ae) {
		t.Fatalf("expected ACPError, got %T", err)
	}
	if ae.Kind != ErrTimeout {
		t.Fatalf("expected timeout kind, got %s", ae.Kind)
	}
	if ae.Method != "session/new" {
		t.Fatalf("unexpected method: %s", ae.Method)
	}
}

func TestWrapACPError_ClassifiesTransport(t *testing.T) {
	err := wrapACPError("initialize", errors.New("broken pipe"))
	var ae *ACPError
	if !errors.As(err, &ae) {
		t.Fatalf("expected ACPError, got %T", err)
	}
	if ae.Kind != ErrTransport {
		t.Fatalf("expected transport kind, got %s", ae.Kind)
	}
}

func TestNewProtocolError(t *testing.T) {
	err := newProtocolError("session/prompt", "invalid result payload")
	var ae *ACPError
	if !errors.As(err, &ae) {
		t.Fatalf("expected ACPError, got %T", err)
	}
	if ae.Kind != ErrProtocol {
		t.Fatalf("expected protocol kind, got %s", ae.Kind)
	}
	if ae.Method != "session/prompt" {
		t.Fatalf("unexpected method: %s", ae.Method)
	}
	if ae.Detail == "" {
		t.Fatalf("expected non-empty detail")
	}
}
