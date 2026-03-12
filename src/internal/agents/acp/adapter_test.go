package acp

import "testing"

func TestSoftTerminalIdleSec_DefaultDisabled(t *testing.T) {
	t.Setenv("ACP_SOFT_TERMINAL_IDLE_SEC", "")
	if got := softTerminalIdleSec(); got != 0 {
		t.Fatalf("expected soft terminal idle to default to 0, got %d", got)
	}
}

func TestSoftTerminalIdleSec_UsesPositiveOverride(t *testing.T) {
	t.Setenv("ACP_SOFT_TERMINAL_IDLE_SEC", "12")
	if got := softTerminalIdleSec(); got != 12 {
		t.Fatalf("expected override to be used, got %d", got)
	}
}

func TestSoftTerminalIdleSec_InvalidOverrideFallsBackToDisabled(t *testing.T) {
	t.Setenv("ACP_SOFT_TERMINAL_IDLE_SEC", "invalid")
	if got := softTerminalIdleSec(); got != 0 {
		t.Fatalf("expected invalid override to fall back to 0, got %d", got)
	}
}
