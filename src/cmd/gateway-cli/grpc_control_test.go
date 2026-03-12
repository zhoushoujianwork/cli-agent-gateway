package main

import (
	"os"
	"testing"
	"time"

	"cli-agent-gateway/internal/config"
)

func TestSendViaSessionGRPCTimeoutUsesResolvedRuntimeConfig(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()

	origHome, hadHome := os.LookupEnv("HOME")
	origAgentTimeout, hadAgentTimeout := os.LookupEnv("AGENT_TIMEOUT_SEC")
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadAgentTimeout {
			_ = os.Setenv("AGENT_TIMEOUT_SEC", origAgentTimeout)
		} else {
			_ = os.Unsetenv("AGENT_TIMEOUT_SEC")
		}
	})

	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	if err := os.Setenv("AGENT_TIMEOUT_SEC", "90"); err != nil {
		t.Fatalf("set AGENT_TIMEOUT_SEC failed: %v", err)
	}

	runtimeValue := "1800"
	if _, _, err := config.Set(repoRoot, "AGENT_TIMEOUT_SEC", runtimeValue, false); err != nil {
		t.Fatalf("set runtime config failed: %v", err)
	}

	got := sendViaSessionGRPCTimeout(repoRoot)
	want := (1800 + 30) * time.Second
	if got != want {
		t.Fatalf("expected timeout=%s from resolved runtime config, got=%s", want, got)
	}
}

func TestInspectGatewaydStatusStoppedWithoutState(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()

	origHome, hadHome := os.LookupEnv("HOME")
	origAddr, hadAddr := os.LookupEnv("GATEWAYD_ADDR")
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadAddr {
			_ = os.Setenv("GATEWAYD_ADDR", origAddr)
		} else {
			_ = os.Unsetenv("GATEWAYD_ADDR")
		}
	})

	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	if err := os.Setenv("GATEWAYD_ADDR", "127.0.0.1:1"); err != nil {
		t.Fatalf("set GATEWAYD_ADDR failed: %v", err)
	}

	payload := inspectGatewaydStatus(repoRoot)
	if got := payload["status"]; got != "stopped" {
		t.Fatalf("expected stopped status, got=%v", got)
	}
	if got := payload["running"]; got != false {
		t.Fatalf("expected running=false, got=%v", got)
	}
	if got := payload["managed"]; got != false {
		t.Fatalf("expected managed=false without state file, got=%v", got)
	}
}

func TestInspectGatewaydStatusWithStaleState(t *testing.T) {
	repoRoot := t.TempDir()
	home := t.TempDir()

	origHome, hadHome := os.LookupEnv("HOME")
	origAddr, hadAddr := os.LookupEnv("GATEWAYD_ADDR")
	t.Cleanup(func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadAddr {
			_ = os.Setenv("GATEWAYD_ADDR", origAddr)
		} else {
			_ = os.Unsetenv("GATEWAYD_ADDR")
		}
	})

	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	if err := os.Setenv("GATEWAYD_ADDR", "127.0.0.1:1"); err != nil {
		t.Fatalf("set GATEWAYD_ADDR failed: %v", err)
	}
	if err := saveGatewaydState(repoRoot, gatewaydState{
		PID:       999999,
		Listen:    "127.0.0.1:1",
		StartedAt: "2026-03-12T07:00:41Z",
	}); err != nil {
		t.Fatalf("save gatewayd state failed: %v", err)
	}

	payload := inspectGatewaydStatus(repoRoot)
	if got := payload["status"]; got != "stopped" {
		t.Fatalf("expected stopped status for stale state, got=%v", got)
	}
	if got := payload["managed"]; got != true {
		t.Fatalf("expected managed=true with saved state, got=%v", got)
	}
	if got := payload["pid_alive"]; got != false {
		t.Fatalf("expected pid_alive=false for stale state, got=%v", got)
	}
}
