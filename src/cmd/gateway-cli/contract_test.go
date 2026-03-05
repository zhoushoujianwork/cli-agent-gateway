package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIJSONContractLifecycle(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	workdir := filepath.Join(repo, "workdir")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}
	env := strings.Join([]string{
		"CODEX_WORKDIR=" + workdir,
		"CHANNEL_TYPE=command",
		"SMS_FETCH_CMD=printf '[]'",
		"SMS_SEND_CMD=/bin/true",
		"REMOTE_USER_ID=tester",
		"ALLOWED_FROM=tester",
		"ACP_AGENT_CMD=/bin/echo",
		"AGENT_TIMEOUT_SEC=60",
		"ACP_PERMISSION_POLICY=auto_allow",
		"ACP_INITIALIZE_TIMEOUT_SEC=1",
		"ACP_SESSION_NEW_TIMEOUT_SEC=1",
		"ACP_SESSION_NEW_RETRIES=1",
		"ACP_SESSION_NEW_RETRY_BACKOFF_SEC=1",
		"POLL_INTERVAL_SEC=1",
		"PROCESS_ONLY_LATEST=0",
		"STORAGE_BACKEND=localfile",
		"LOCK_FILE=.cli_agent_gateway.lock",
		"STATE_FILE=.agent_gateway_state.json",
		"REPORT_DIR=.agent_gateway_reports",
		"INTERACTION_LOG_FILE=.agent_gateway_interactions.jsonl",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte(env), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	assertJSONStatus := func(action, wantStatus string) map[string]any {
		t.Helper()
		code, payload, stdout, stderr := runCLIJSON(t, bin, repo, action, "--json")
		if code != 0 {
			t.Fatalf("%s exit=%d stdout=%s stderr=%s", action, code, stdout, stderr)
		}
		if ok, _ := payload["ok"].(bool); !ok {
			t.Fatalf("%s returned ok=false payload=%v", action, payload)
		}
		if got, _ := payload["action"].(string); got != action {
			t.Fatalf("%s action mismatch: got=%q payload=%v", action, got, payload)
		}
		if got, _ := payload["status"].(string); got != wantStatus {
			t.Fatalf("%s status mismatch: got=%q want=%q payload=%v", action, got, wantStatus, payload)
		}
		return payload
	}

	assertJSONStatus("status", "stopped")
	assertJSONStatus("health", "healthy")
	assertJSONStatus("doctor", "healthy")
	started := assertJSONStatus("start", "running")
	if _, ok := started["pid"]; !ok {
		t.Fatalf("start missing pid payload=%v", started)
	}
	assertJSONStatus("status", "running")
	assertJSONStatus("restart", "running")
	assertJSONStatus("stop", "stopped")
	assertJSONStatus("status", "stopped")
}

func buildGatewayBinary(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	srcRoot := filepath.Clean(filepath.Join(wd, "../.."))
	bin := filepath.Join(t.TempDir(), "cag-test")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gateway-cli")
	cmd.Dir = srcRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build gateway binary failed: %v\n%s", err, string(out))
	}
	return bin
}

func runCLIJSON(t *testing.T, bin, cwd string, args ...string) (int, map[string]any, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run cli failed: %v", err)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid json output: %v output=%s", err, string(out))
	}
	return code, payload, string(out), ""
}
