package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cli-agent-gateway/internal/config"
)

func TestRenderPlainStatusIncludesLogPathAndRecentLines(t *testing.T) {
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "gatewayd.log")
	content := strings.Join([]string{
		"[2026-03-09T03:00:00Z] startup channel=command workdir=/tmp/work",
		"[2026-03-09T03:00:01Z] startup acp_cmd=true permission_policy=auto_allow",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log file failed: %v", err)
	}

	pid := 1234
	rendered := renderPlainStatus("/tmp/repo", StatusPayload{
		Running:   true,
		PID:       &pid,
		StartedAt: "2026-03-09T03:00:00Z",
		LockFile:  "/Users/test/.cag/runtime/gateway.lock",
	}, config.AppConfig{}, logPath)

	if !strings.Contains(rendered, "RUNNING pid=1234 started_at=2026-03-09T03:00:00Z lock=/Users/test/.cag/runtime/gateway.lock") {
		t.Fatalf("expected running summary, got=%q", rendered)
	}
	if !strings.Contains(rendered, "log="+logPath) {
		t.Fatalf("expected log path, got=%q", rendered)
	}
	if !strings.Contains(rendered, "recent_log:\n") {
		t.Fatalf("expected recent_log header, got=%q", rendered)
	}
	if !strings.Contains(rendered, "startup channel=command") {
		t.Fatalf("expected recent log lines, got=%q", rendered)
	}
}

func TestRenderPlainStatusNotRunningStillShowsLogPath(t *testing.T) {
	rendered := renderPlainStatus("/tmp/repo", StatusPayload{
		Running:  false,
		LockFile: "/Users/test/.cag/runtime/gateway.lock",
	}, config.AppConfig{}, "/Users/test/.cag/gatewayd/gatewayd.log")

	if !strings.Contains(rendered, "NOT_RUNNING lock=/Users/test/.cag/runtime/gateway.lock") {
		t.Fatalf("expected stopped summary, got=%q", rendered)
	}
	if !strings.Contains(rendered, "log=/Users/test/.cag/gatewayd/gatewayd.log") {
		t.Fatalf("expected log path for stopped status, got=%q", rendered)
	}
	if strings.Contains(rendered, "recent_log:") {
		t.Fatalf("did not expect recent logs for stopped status, got=%q", rendered)
	}
}

func TestResolveLogPathUsesSharedGatewaydLogByDefault(t *testing.T) {
	origHome, hadHome := os.LookupEnv("HOME")
	origLog, hadLog := os.LookupEnv("GATEWAY_LOG_FILE")
	defer func() {
		if hadHome {
			_ = os.Setenv("HOME", origHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
		if hadLog {
			_ = os.Setenv("GATEWAY_LOG_FILE", origLog)
		} else {
			_ = os.Unsetenv("GATEWAY_LOG_FILE")
		}
	}()

	home := t.TempDir()
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	_ = os.Unsetenv("GATEWAY_LOG_FILE")

	got := resolveLogPath(t.TempDir(), nil)
	want := filepath.Join(home, ".cag", "gatewayd", "gatewayd.log")
	if got != want {
		t.Fatalf("expected shared gatewayd log path, got=%s want=%s", got, want)
	}
}

func TestStatusJSONUsesRuntimeMetadataForChannelAndWorkdir(t *testing.T) {
	node := statusJSON("status", StatusPayload{
		Running:   true,
		LockFile:  "/Users/test/.cag/runtime/gateway.lock",
		StartedAt: "2026-03-09T03:00:00Z",
		Metadata: map[string]any{
			"channel":  "dingtalk",
			"workdir":  "/Users/test/.cag",
			"log_file": "/Users/test/.cag/gatewayd/gatewayd.log",
		},
	}, config.AppConfig{
		ChannelType: "command",
		Workdir:     "/tmp/wrong",
	}, "")

	if got := node["channel"]; got != "dingtalk" {
		t.Fatalf("expected runtime channel, got=%v", got)
	}
	if got := node["workdir"]; got != "/Users/test/.cag" {
		t.Fatalf("expected runtime workdir, got=%v", got)
	}
}

func TestStatusJSONDoesNotFallBackToConfigWhenStopped(t *testing.T) {
	node := statusJSON("status", StatusPayload{
		Running:  false,
		LockFile: "/Users/test/.cag/runtime/gateway.lock",
	}, config.AppConfig{
		ChannelType: "command",
		Workdir:     "/tmp/wrong",
	}, "")

	if _, ok := node["channel"]; ok {
		raw, _ := json.Marshal(node)
		t.Fatalf("did not expect channel fallback in stopped status, payload=%s", raw)
	}
	if _, ok := node["workdir"]; ok {
		raw, _ := json.Marshal(node)
		t.Fatalf("did not expect workdir fallback in stopped status, payload=%s", raw)
	}
}
