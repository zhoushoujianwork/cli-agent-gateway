package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type cmdResult struct {
	Code   int
	Stdout string
	Stderr string
}

func TestCLIContractJSONFlow(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := createTempRepo(t)

	res := runBin(t, bin, repo, "status", "--json")
	if res.Code != 0 {
		t.Fatalf("status failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	initial := parseStatusJSON(t, res.Stdout)
	if initial.Running {
		t.Fatalf("expected stopped before start: %+v", initial)
	}

	res = runBin(t, bin, repo, "health", "--json")
	if res.Code != 0 {
		t.Fatalf("health failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	health := parseHealthJSON(t, res.Stdout)
	if !health.OK {
		t.Fatalf("expected health ok=true, got: %+v", health)
	}

	res = runBin(t, bin, repo, "send", "--to", "tester", "--text", "hello", "--json")
	if res.Code != 0 {
		t.Fatalf("send failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	send := parseSendJSON(t, res.Stdout)
	if !send.OK {
		t.Fatalf("expected send ok=true, got: %+v", send)
	}

	res = runBin(t, bin, repo, "start", "--json")
	if res.Code != 0 {
		t.Fatalf("start failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	started := parseStatusJSON(t, res.Stdout)
	if !started.Running || started.PID == nil || *started.PID <= 0 {
		t.Fatalf("expected running after start, got: %+v", started)
	}

	t.Cleanup(func() {
		_ = runBin(t, bin, repo, "stop", "--json")
	})

	res = runBin(t, bin, repo, "restart", "--json")
	if res.Code != 0 {
		t.Fatalf("restart failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	restarted := parseStatusJSON(t, res.Stdout)
	if !restarted.Running || restarted.PID == nil || *restarted.PID <= 0 {
		t.Fatalf("expected running after restart, got: %+v", restarted)
	}

	res = runBin(t, bin, repo, "stop", "--json")
	if res.Code != 0 {
		t.Fatalf("stop failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	stopped := parseStatusJSON(t, res.Stdout)
	if stopped.Running {
		t.Fatalf("expected stopped after stop, got: %+v", stopped)
	}
}

func TestCLIConfigCommand(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	workdir := repo
	res := runBin(t, bin, repo, "config", workdir)
	if res.Code != 0 {
		t.Fatalf("config failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	envPath := filepath.Join(repo, ".env")
	if _, err := os.Stat(envPath); err != nil {
		t.Fatalf(".env not created: %v", err)
	}
	if !strings.Contains(res.Stdout, envPath) {
		t.Fatalf("config output should mention env path, out=%q", res.Stdout)
	}
}

func buildGatewayBinary(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("failed to locate test file path")
	}
	cmdDir := filepath.Dir(thisFile)
	moduleRoot := filepath.Clean(filepath.Join(cmdDir, "..", ".."))
	binPath := filepath.Join(t.TempDir(), "cag-test-bin")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/gateway-cli")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build binary failed: %v\n%s", err, string(out))
	}
	return binPath
}

func createTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	envPath := filepath.Join(repo, ".env")
	content := strings.Join([]string{
		"CODEX_WORKDIR=" + repo,
		"CHANNEL_TYPE=command",
		"SMS_FETCH_CMD=printf '[]'",
		"SMS_SEND_CMD=true",
		"ALLOWED_FROM=tester",
		"REMOTE_USER_ID=tester",
		"ACP_AGENT_CMD=true",
		"LOCK_FILE=.cli_agent_gateway.lock",
		"STATE_FILE=.agent_gateway_state.json",
		"INTERACTION_LOG_FILE=.agent_gateway_interactions.jsonl",
		"REPORT_DIR=.agent_gateway_reports",
		"STORAGE_BACKEND=sqlite",
		"STORAGE_SQLITE_PATH=.agent_gateway.db",
		"POLL_INTERVAL_SEC=1",
	}, "\n") + "\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}
	return repo
}

func runBin(t *testing.T, bin, dir string, args ...string) cmdResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	res := cmdResult{Stdout: string(out)}
	if err == nil {
		return res
	}
	var ee *exec.ExitError
	if strings.TrimSpace(string(out)) == "" {
		res.Stderr = err.Error()
	} else {
		res.Stderr = string(out)
	}
	if errors.As(err, &ee) {
		res.Code = ee.ExitCode()
	} else {
		res.Code = 1
	}
	return res
}

func parseStatusJSON(t *testing.T, out string) StatusPayload {
	t.Helper()
	var node StatusPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid status json: %v\nraw=%q", err, out)
	}
	if strings.TrimSpace(node.LockFile) == "" {
		t.Fatalf("status.lock_file should not be empty")
	}
	return node
}

func parseHealthJSON(t *testing.T, out string) HealthPayload {
	t.Helper()
	var node HealthPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid health json: %v\nraw=%q", err, out)
	}
	if len(node.Items) == 0 {
		t.Fatalf("health.items should not be empty")
	}
	return node
}

func parseSendJSON(t *testing.T, out string) SendPayload {
	t.Helper()
	var node SendPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid send json: %v\nraw=%q", err, out)
	}
	if strings.TrimSpace(node.MessageID) == "" {
		t.Fatalf("send.message_id should not be empty")
	}
	return node
}
