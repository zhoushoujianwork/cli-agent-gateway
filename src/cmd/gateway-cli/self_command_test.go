package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelfCommandSpecUsesExecutableWhenPresent(t *testing.T) {
	repoRoot := t.TempDir()
	exePath := filepath.Join(repoRoot, "gateway-cli")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake executable failed: %v", err)
	}

	name, args := selfCommandSpec(exePath, "stop", "--json")
	if name != exePath {
		t.Fatalf("expected executable path, got %q", name)
	}
	if !reflect.DeepEqual(args, []string{"stop", "--json"}) {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestSelfCommandSpecFallsBackToGoRunWhenExecutableMissing(t *testing.T) {
	name, args := selfCommandSpec("/tmp/definitely-missing-gateway-cli", "restart", "--json")
	if name != "go" {
		t.Fatalf("expected go fallback, got %q", name)
	}
	want := []string{"run", "./cmd/gateway-cli", "restart", "--json"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected go-run args: got=%#v want=%#v", args, want)
	}
}

func TestShouldRetryWithGoRun(t *testing.T) {
	if !shouldRetryWithGoRun(errors.New("fork/exec /tmp/go-build123/exe/gateway-cli: no such file or directory")) {
		t.Fatalf("expected stale executable error to trigger go-run retry")
	}
	if shouldRetryWithGoRun(errors.New("context deadline exceeded")) {
		t.Fatalf("did not expect unrelated error to trigger go-run retry")
	}
}
