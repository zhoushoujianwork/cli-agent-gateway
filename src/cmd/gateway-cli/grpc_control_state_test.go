package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGatewaydStatePathForHomeIsGlobal(t *testing.T) {
	home := t.TempDir()
	repoA := "/tmp/repo-a"
	repoB := "/tmp/repo-b"

	pathA := gatewaydStatePathForHome(home, repoA)
	pathB := gatewaydStatePathForHome(home, repoB)
	want := filepath.Join(home, ".cag", "gatewayd", gatewaydStateFileName)

	if pathA != want {
		t.Fatalf("expected global gatewayd state path, got=%s want=%s", pathA, want)
	}
	if pathB != want {
		t.Fatalf("expected global gatewayd state path, got=%s want=%s", pathB, want)
	}
}

func TestLoadGatewaydStateMigratesLegacyPath(t *testing.T) {
	home := t.TempDir()
	repoRoot := filepath.Join(home, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo failed: %v", err)
	}

	t.Setenv("HOME", home)

	legacyPath := legacyGatewaydStatePathForHome(home, repoRoot)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy dir failed: %v", err)
	}
	raw := []byte("{\"pid\":1234,\"listen\":\"127.0.0.1:58473\",\"started_at\":\"2026-03-09T00:00:00Z\"}\n")
	if err := os.WriteFile(legacyPath, raw, 0o644); err != nil {
		t.Fatalf("write legacy state failed: %v", err)
	}

	state, err := loadGatewaydState(repoRoot)
	if err != nil {
		t.Fatalf("loadGatewaydState failed: %v", err)
	}
	if state.PID != 1234 {
		t.Fatalf("expected migrated state pid=1234, got=%d", state.PID)
	}

	globalPath := gatewaydStatePath(repoRoot)
	if _, err := os.Stat(globalPath); err != nil {
		t.Fatalf("expected migrated global state file at %s: %v", globalPath, err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy state to be removed, stat err=%v", err)
	}
}
