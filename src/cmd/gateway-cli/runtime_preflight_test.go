package main

import (
	"strings"
	"testing"

	"cli-agent-gateway/internal/config"
)

func TestResolveACPBinary(t *testing.T) {
	if got := resolveACPBinary("codex-acp --stdio"); got != "codex-acp" {
		t.Fatalf("expected codex-acp, got %q", got)
	}
	if got := resolveACPBinary("   "); got != "" {
		t.Fatalf("expected empty binary, got %q", got)
	}
}

func TestEnsureACPCommandAvailable(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("ACP_AGENT_CMD", "")

	if _, _, err := config.Set(repoRoot, "ACP_AGENT_CMD", "sh", false); err != nil {
		t.Fatalf("set ACP_AGENT_CMD failed: %v", err)
	}
	if err := ensureACPCommandAvailable(repoRoot); err != nil {
		t.Fatalf("expected ACP preflight success, got err=%v", err)
	}

	if _, _, err := config.Set(repoRoot, "ACP_AGENT_CMD", "definitely-not-a-real-binary", false); err != nil {
		t.Fatalf("set missing ACP_AGENT_CMD failed: %v", err)
	}
	t.Setenv("ACP_AGENT_CMD", "")
	err := ensureACPCommandAvailable(repoRoot)
	if err == nil {
		t.Fatalf("expected ACP preflight failure")
	}
	if !strings.Contains(err.Error(), "acp command not found") {
		t.Fatalf("expected actionable ACP error, got=%v", err)
	}
}

func TestNewRuntimeCmdIncludesRestart(t *testing.T) {
	cmd := newRuntimeCmd(t.TempDir())
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "restart" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected runtime restart command to be registered")
	}
}
