package acp

import "testing"

func TestIsTerminal_StopReasonEndTurn(t *testing.T) {
	payload := map[string]any{"stopReason": "end_turn"}
	if !isTerminal(payload) {
		t.Fatalf("expected stopReason=end_turn to be terminal")
	}
}

func TestTerminalReasonFrom_StopReasonEndTurn(t *testing.T) {
	payload := map[string]any{"stopReason": "end_turn"}
	if got := terminalReasonFrom(payload); got != "completed" {
		t.Fatalf("expected completed terminal reason, got %q", got)
	}
}

func TestStatusFrom_StopReasonEndTurn(t *testing.T) {
	payload := map[string]any{"stopReason": "end_turn"}
	if got := statusFrom(payload); got != "ok" {
		t.Fatalf("expected ok status, got %q", got)
	}
}
