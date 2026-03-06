package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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
	if send.MsgType != "text" || send.Source != "text" {
		t.Fatalf("unexpected send payload: %+v", send)
	}

	msgFile := filepath.Join(repo, "message.md")
	if err := os.WriteFile(msgFile, []byte("# hello\nfrom file\n"), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	res = runBin(t, bin, repo, "send", "--to", "tester", "--file", msgFile, "--msgtype", "markdown", "--dry-run", "--json")
	if res.Code != 0 {
		t.Fatalf("send dry-run file failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	dry := parseSendJSON(t, res.Stdout)
	if !dry.OK || !dry.DryRun || dry.MsgType != "markdown" || dry.Source != "file" {
		t.Fatalf("unexpected send dry-run payload: %+v", dry)
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

func TestCLIConfigGlobalCommand(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	home := t.TempDir()
	res := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", "--global")
	if res.Code != 0 {
		t.Fatalf("config --global failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	envPath := filepath.Join(home, ".cag", ".env")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("global .env not created: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "GATEWAYD_ADDR=127.0.0.1:58473") {
		t.Fatalf("expected default GATEWAYD_ADDR in global env, content=%q", content)
	}
	if !strings.Contains(res.Stdout, envPath) {
		t.Fatalf("config --global output should mention env path, out=%q", res.Stdout)
	}
}

func TestCLIConfigGlobalCommandWithGatewayAddr(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	home := t.TempDir()
	addr := "127.0.0.1:61001"
	res := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", "--global", "--gatewayd-addr", addr)
	if res.Code != 0 {
		t.Fatalf("config --global --gatewayd-addr failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	envPath := filepath.Join(home, ".cag", ".env")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("global .env not created: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "GATEWAYD_ADDR="+addr) {
		t.Fatalf("expected configured GATEWAYD_ADDR in global env, content=%q", content)
	}
}

func TestCLIStartWithLogFileFlag(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := createTempRepo(t)
	logPath := filepath.Join(repo, "tmp", "gateway-custom.log")

	res := runBin(t, bin, repo, "start", "--json", "--log-file", logPath)
	if res.Code != 0 {
		t.Fatalf("start with log-file failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	started := parseStatusJSON(t, res.Stdout)
	if !started.Running {
		t.Fatalf("expected running after start, got: %+v", started)
	}
	t.Cleanup(func() {
		_ = runBin(t, bin, repo, "stop", "--json")
	})

	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected custom log file created at %s: %v", logPath, err)
	}
}

func TestCLISendDryRunWithoutDotEnv(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	res := runBin(t, bin, repo, "send", "--to", "tester", "--text", "hello", "--dry-run", "--json")
	if strings.Contains(res.Stdout, "panic:") || strings.Contains(res.Stderr, "panic:") {
		t.Fatalf("send should not panic without .env, stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	if res.Code != 0 {
		t.Fatalf("send dry-run without .env failed: code=%d stderr=%s stdout=%s", res.Code, res.Stderr, res.Stdout)
	}
	node := parseSendJSON(t, res.Stdout)
	if !node.OK || !node.DryRun {
		t.Fatalf("unexpected send payload without .env: %+v", node)
	}
}

func TestGatewaydAddrReadsGlobalCagEnv(t *testing.T) {
	t.Parallel()

	origHome, hadHome := os.LookupEnv("HOME")
	origAddr, hadAddr := os.LookupEnv("GATEWAYD_ADDR")
	defer func() {
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
		gatewayAddrEnvOnce = sync.Once{}
	}()

	_ = os.Unsetenv("GATEWAYD_ADDR")
	tmpHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, ".cag"), 0o755); err != nil {
		t.Fatalf("mkdir .cag failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpHome, ".cag", ".env"), []byte("GATEWAYD_ADDR=127.0.0.1:59999\n"), 0o644); err != nil {
		t.Fatalf("write ~/.cag/.env failed: %v", err)
	}
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("set HOME failed: %v", err)
	}
	gatewayAddrEnvOnce = sync.Once{}

	if got := gatewaydAddr(); got != "127.0.0.1:59999" {
		t.Fatalf("expected gateway addr from ~/.cag/.env, got=%s", got)
	}
}

func TestGatewaydStatePathForHomeScopedByRepo(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	repoA := filepath.Join(t.TempDir(), "repo-a")
	repoB := filepath.Join(t.TempDir(), "repo-b")
	pathA := gatewaydStatePathForHome(home, repoA)
	pathB := gatewaydStatePathForHome(home, repoB)
	base := filepath.Join(home, ".cag", "gatewayd") + string(os.PathSeparator)
	if !strings.HasPrefix(pathA, base) {
		t.Fatalf("state path should be under ~/.cag/gatewayd, path=%s", pathA)
	}
	if !strings.HasPrefix(pathB, base) {
		t.Fatalf("state path should be under ~/.cag/gatewayd, path=%s", pathB)
	}
	if pathA == pathB {
		t.Fatalf("state paths should differ per repo, path=%s", pathA)
	}
}

func TestCLISessionDeleteAndRecreateClosedLoop(t *testing.T) {
	t.Parallel()

	bin := buildGatewayBinary(t)
	repo := createTempRepo(t)
	setStorageBackend(t, repo, "localfile")
	msgID := "msg-delete-1"
	sender := "u-test"
	channel := "command"
	threadID := "-"
	key := buildSessionKey(channel, sender, threadID)
	ts := "2026-03-06T03:00:00Z"
	interactionPath := filepath.Join(repo, ".agent_gateway_interactions.jsonl")
	lines := []map[string]any{
		{
			"kind":   "inbound_received",
			"msg_id": msgID,
			"sender": sender,
			"text":   "hello",
			"time":   ts,
			"user_profile": map[string]any{
				"channel":     channel,
				"sender":      sender,
				"sender_name": "UTest",
				"thread_id":   threadID,
			},
		},
		{
			"kind":        "trace",
			"stage":       "session_resolved",
			"msg_id":      msgID,
			"session_key": key,
			"session_id":  "sid-delete-1",
			"ts":          ts,
		},
	}
	writeJSONL(t, interactionPath, lines)

	res := runBin(t, bin, repo, "sessions", "--json")
	if res.Code != 0 {
		t.Fatalf("sessions before delete failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	before := parseSessionsJSON(t, res.Stdout)
	if !containsSessionKey(before.Items, key) {
		t.Fatalf("expected key present before delete: key=%s items=%+v", key, before.Items)
	}

	res = runBin(t, bin, repo, "session-delete", "--session-key", key, "--json")
	if res.Code != 0 {
		t.Fatalf("session-delete failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	del := parseSessionMutationJSON(t, res.Stdout)
	if !del.OK || del.Action != "session-delete" || del.SessionKey != key {
		t.Fatalf("unexpected session-delete payload: %+v", del)
	}

	res = runBin(t, bin, repo, "sessions", "--json")
	if res.Code != 0 {
		t.Fatalf("sessions after delete failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	afterDelete := parseSessionsJSON(t, res.Stdout)
	if containsSessionKey(afterDelete.Items, key) {
		t.Fatalf("expected key hidden after delete: key=%s items=%+v", key, afterDelete.Items)
	}

	res = runBin(t, bin, repo, "session-new", "--session-key", key, "--workdir", repo, "--json")
	if res.Code != 0 {
		t.Fatalf("session-new failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	created := parseSessionMutationJSON(t, res.Stdout)
	if !created.OK || created.Action != "session-new" || created.SessionKey != key {
		t.Fatalf("unexpected session-new payload: %+v", created)
	}
	if filepath.Clean(created.Workdir) != filepath.Clean(repo) {
		t.Fatalf("expected session-new workdir=%s got=%s", repo, created.Workdir)
	}

	res = runBin(t, bin, repo, "send", "--session-key", key, "--text", "ping", "--dry-run", "--json")
	if res.Code != 0 {
		t.Fatalf("send dry-run failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	send := parseSendJSON(t, res.Stdout)
	if !send.OK {
		t.Fatalf("expected send ok after session-new, got: %+v", send)
	}
	if send.TerminalReason != "dry_run" {
		t.Fatalf("expected terminal_reason=dry_run, got: %+v", send)
	}

	res = runBin(t, bin, repo, "sessions", "--json")
	if res.Code != 0 {
		t.Fatalf("sessions after recreate failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	afterCreate := parseSessionsJSON(t, res.Stdout)
	if !containsSessionKey(afterCreate.Items, key) {
		t.Fatalf("expected key present after session-new: key=%s items=%+v", key, afterCreate.Items)
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
	cmd.Env = append(os.Environ(), "GATEWAYD_ADDR="+testGatewaydAddr(dir))
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

func runBinWithEnv(t *testing.T, bin, dir string, env []string, args ...string) cmdResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GATEWAYD_ADDR="+testGatewaydAddr(dir)), env...)
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

func testGatewaydAddr(dir string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.TrimSpace(dir)))
	port := 59000 + int(h.Sum32()%3000)
	return fmt.Sprintf("127.0.0.1:%d", port)
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
	if strings.TrimSpace(node.MsgType) == "" {
		t.Fatalf("send.msg_type should not be empty")
	}
	if strings.TrimSpace(node.Source) == "" {
		t.Fatalf("send.source should not be empty")
	}
	return node
}

func parseSessionsJSON(t *testing.T, out string) SessionsPayload {
	t.Helper()
	var node SessionsPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid sessions json: %v\nraw=%q", err, out)
	}
	return node
}

func parseSessionMutationJSON(t *testing.T, out string) SessionMutationPayload {
	t.Helper()
	var node SessionMutationPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid session mutation json: %v\nraw=%q", err, out)
	}
	return node
}

func containsSessionKey(items []SessionsItem, key string) bool {
	for _, it := range items {
		if strings.TrimSpace(it.SessionKey) == strings.TrimSpace(key) {
			return true
		}
	}
	return false
}

func writeJSONL(t *testing.T, path string, nodes []map[string]any) {
	t.Helper()
	lines := make([]string, 0, len(nodes))
	for i, node := range nodes {
		raw, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("marshal jsonl node[%d] failed: %v", i, err)
		}
		lines = append(lines, string(raw))
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write jsonl failed path=%s err=%v", path, err)
	}
}

func setStorageBackend(t *testing.T, repo, backend string) {
	t.Helper()
	envPath := filepath.Join(repo, ".env")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "STORAGE_BACKEND=sqlite", "STORAGE_BACKEND="+strings.TrimSpace(backend))
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}
}
