package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

var sharedTestHome string
var sharedTestGatewayAddr string
var sharedTestBin string
var sharedTestBinErr error
var sharedTestBinOnce sync.Once
var sharedCLIMu sync.Mutex

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cag-gatewayd-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create shared test home failed: %v\n", err)
		os.Exit(1)
	}
	sharedTestHome = dir
	addr, err := reserveTestGatewayAddr()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve test gateway addr failed: %v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	sharedTestGatewayAddr = addr
	code := m.Run()
	cleanupSharedTestGatewayd()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func cleanupSharedTestGatewayd() {
	if strings.TrimSpace(sharedTestHome) == "" || strings.TrimSpace(sharedTestGatewayAddr) == "" {
		return
	}
	origHome, hadHome := os.LookupEnv("HOME")
	origAddr, hadAddr := os.LookupEnv("GATEWAYD_ADDR")
	_ = os.Setenv("HOME", sharedTestHome)
	_ = os.Setenv("GATEWAYD_ADDR", sharedTestGatewayAddr)
	gatewayAddrEnvOnce = sync.Once{}
	_, _ = shutdownManagedGatewayd(".")
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
}

func resetSharedTestGatewayd(t *testing.T) {
	t.Helper()
	sharedCLIMu.Lock()
	defer sharedCLIMu.Unlock()
	cleanupSharedTestGatewayd()
}

func reserveTestGatewayAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func TestCLIContractJSONFlow(t *testing.T) {
	resetSharedTestGatewayd(t)

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
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "CHANNEL_TYPE=command") {
		t.Fatalf("expected CHANNEL_TYPE default in generated env, content=%q", content)
	}
	if strings.Contains(content, "CODEX_WORKDIR=") {
		t.Fatalf("CODEX_WORKDIR should not be generated anymore, content=%q", content)
	}
	if strings.Contains(content, "POLL_INTERVAL_SEC=") {
		t.Fatalf("runtime config should not stay in repo .env, content=%q", content)
	}
	if strings.Contains(content, "REPLY_STYLE_ENABLED=") {
		t.Fatalf("runtime config should be removed from repo .env, content=%q", content)
	}
}

func TestCLIConfigGlobalCommand(t *testing.T) {
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

func TestCLIConfigSetAndGetRuntimeValue(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	if res := runBin(t, bin, repo, "config", repo); res.Code != 0 {
		t.Fatalf("config init failed: code=%d stderr=%s", res.Code, res.Stderr)
	}

	res := runBin(t, bin, repo, "config", "set", "POLL_INTERVAL_SEC", "9")
	if res.Code != 0 {
		t.Fatalf("config set runtime failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "runtime_db") {
		t.Fatalf("expected runtime_db scope in output, out=%q", res.Stdout)
	}

	res = runBin(t, bin, repo, "config", "get", "POLL_INTERVAL_SEC")
	if res.Code != 0 {
		t.Fatalf("config get runtime failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "POLL_INTERVAL_SEC=9") || !strings.Contains(res.Stdout, "source=runtime_db") {
		t.Fatalf("unexpected runtime get output: %q", res.Stdout)
	}

	raw, err := os.ReadFile(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	if strings.Contains(string(raw), "POLL_INTERVAL_SEC=") {
		t.Fatalf("runtime key should not be stored in .env, content=%q", string(raw))
	}
}

func TestCLIConfigShowAliasesListAndGet(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	if res := runBin(t, bin, repo, "config", repo); res.Code != 0 {
		t.Fatalf("config init failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	if res := runBin(t, bin, repo, "config", "set", "POLL_INTERVAL_SEC", "7"); res.Code != 0 {
		t.Fatalf("config set failed: code=%d stderr=%s", res.Code, res.Stderr)
	}

	listRes := runBin(t, bin, repo, "config", "show")
	if listRes.Code != 0 {
		t.Fatalf("config show failed: code=%d stderr=%s", listRes.Code, listRes.Stderr)
	}
	if !strings.Contains(listRes.Stdout, "POLL_INTERVAL_SEC=7") {
		t.Fatalf("expected config show to list values, out=%q", listRes.Stdout)
	}

	getRes := runBin(t, bin, repo, "config", "show", "POLL_INTERVAL_SEC")
	if getRes.Code != 0 {
		t.Fatalf("config show key failed: code=%d stderr=%s", getRes.Code, getRes.Stderr)
	}
	if !strings.Contains(getRes.Stdout, "POLL_INTERVAL_SEC=7") || !strings.Contains(getRes.Stdout, "source=runtime_db") {
		t.Fatalf("unexpected config show key output: %q", getRes.Stdout)
	}
}

func TestCLIConfigMigratesRuntimeKeysOutOfEnv(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	envPath := filepath.Join(repo, ".env")
	content := strings.Join([]string{
		"POLL_INTERVAL_SEC=11",
		"REPLY_STYLE_ENABLED=0",
		"CHANNEL_TYPE=command",
	}, "\n") + "\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}

	res := runBin(t, bin, repo, "config", repo)
	if res.Code != 0 {
		t.Fatalf("config migrate failed: code=%d stderr=%s", res.Code, res.Stderr)
	}

	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read migrated .env failed: %v", err)
	}
	envContent := string(raw)
	if strings.Contains(envContent, "POLL_INTERVAL_SEC=") || strings.Contains(envContent, "REPLY_STYLE_ENABLED=") {
		t.Fatalf("runtime keys should be removed from repo .env after migrate, content=%q", envContent)
	}

	getRes := runBin(t, bin, repo, "config", "get", "POLL_INTERVAL_SEC")
	if getRes.Code != 0 {
		t.Fatalf("config get migrated runtime failed: code=%d stderr=%s", getRes.Code, getRes.Stderr)
	}
	if !strings.Contains(getRes.Stdout, "POLL_INTERVAL_SEC=11") || !strings.Contains(getRes.Stdout, "source=runtime_db") {
		t.Fatalf("unexpected migrated runtime output: %q", getRes.Stdout)
	}
}

func TestCLIConfigSetGlobalViaUnifiedCommand(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	home := t.TempDir()
	addr := "127.0.0.1:62002"
	res := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", "set", "GATEWAYD_ADDR", addr, "--global")
	if res.Code != 0 {
		t.Fatalf("config set global failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".cag", ".env"))
	if err != nil {
		t.Fatalf("read global .env failed: %v", err)
	}
	if !strings.Contains(string(raw), "GATEWAYD_ADDR="+addr) {
		t.Fatalf("expected unified global set to write addr, content=%q", string(raw))
	}
}

func TestCLIConfigSetGlobalDingTalkValue(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	home := t.TempDir()
	res := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", "set", "DINGTALK_APP_KEY", "app-key-1", "--global")
	if res.Code != 0 {
		t.Fatalf("config set global dingtalk failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".cag", ".env"))
	if err != nil {
		t.Fatalf("read global .env failed: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "DINGTALK_APP_KEY=app-key-1") {
		t.Fatalf("expected dingtalk key in ~/.cag/.env, content=%q", content)
	}
	getRes := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", "get", "DINGTALK_APP_KEY")
	if getRes.Code != 0 {
		t.Fatalf("config get dingtalk failed: code=%d stderr=%s", getRes.Code, getRes.Stderr)
	}
	if !strings.Contains(getRes.Stdout, "source=user_env") {
		t.Fatalf("expected user_env source, out=%q", getRes.Stdout)
	}
}

func TestCLIConfigMigratesDingTalkKeysToUserEnv(t *testing.T) {
	bin := buildGatewayBinary(t)
	repo := t.TempDir()
	home := t.TempDir()
	envPath := filepath.Join(repo, ".env")
	content := strings.Join([]string{
		"CHANNEL_TYPE=dingtalk",
		"DINGTALK_APP_KEY=legacy-app-key",
		"DINGTALK_SEND_MODE=api",
	}, "\n") + "\n"
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write repo .env failed: %v", err)
	}

	res := runBinWithEnv(t, bin, repo, []string{"HOME=" + home}, "config", repo)
	if res.Code != 0 {
		t.Fatalf("config migrate dingtalk failed: code=%d stderr=%s", res.Code, res.Stderr)
	}

	repoRaw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read repo .env failed: %v", err)
	}
	if strings.Contains(string(repoRaw), "DINGTALK_APP_KEY=") || strings.Contains(string(repoRaw), "DINGTALK_SEND_MODE=") {
		t.Fatalf("expected dingtalk keys removed from repo .env, content=%q", string(repoRaw))
	}

	userRaw, err := os.ReadFile(filepath.Join(home, ".cag", ".env"))
	if err != nil {
		t.Fatalf("read ~/.cag/.env failed: %v", err)
	}
	if !strings.Contains(string(userRaw), "DINGTALK_APP_KEY=legacy-app-key") || !strings.Contains(string(userRaw), "DINGTALK_SEND_MODE=api") {
		t.Fatalf("expected dingtalk keys migrated to ~/.cag/.env, content=%q", string(userRaw))
	}
}

func TestCLIStartWithLogFileFlag(t *testing.T) {
	resetSharedTestGatewayd(t)

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

	if err := waitForPath(logPath, 3*time.Second); err != nil {
		t.Fatalf("expected custom log file created at %s: %v", logPath, err)
	}
}

func TestCLIStartSendsStartupGreeting(t *testing.T) {
	resetSharedTestGatewayd(t)

	bin := buildGatewayBinary(t)
	repo := createTempRepo(t)
	greetingLog := filepath.Join(repo, "tmp", "startup-greeting.log")
	sendScript := filepath.Join(repo, "tmp", "send.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + shellQuoteForTest(greetingLog) + "\n"
	if err := os.MkdirAll(filepath.Dir(sendScript), 0o755); err != nil {
		t.Fatalf("mkdir send script dir failed: %v", err)
	}
	if err := os.WriteFile(sendScript, []byte(script), 0o755); err != nil {
		t.Fatalf("write send script failed: %v", err)
	}
	replaceEnvValue(t, repo, "SMS_SEND_CMD=true", "SMS_SEND_CMD="+sendScript)

	res := runBin(t, bin, repo, "start", "--json")
	if res.Code != 0 {
		t.Fatalf("start failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	t.Cleanup(func() {
		_ = runBin(t, bin, repo, "stop", "--json")
	})

	if err := waitForPath(greetingLog, 4*time.Second); err != nil {
		t.Fatalf("read startup greeting log failed: %v", err)
	}
	raw, err := os.ReadFile(greetingLog)
	if err != nil {
		t.Fatalf("read startup greeting log failed: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "你好，我是 CLI Agent Gateway。") {
		t.Fatalf("expected startup greeting, content=%q", content)
	}
	if !strings.Contains(content, "服务已经启动") {
		t.Fatalf("expected startup intro, content=%q", content)
	}
}

func TestCLISendDryRunWithoutDotEnv(t *testing.T) {
	resetSharedTestGatewayd(t)

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
	home := t.TempDir()
	repoA := filepath.Join(t.TempDir(), "repo-a")
	repoB := filepath.Join(t.TempDir(), "repo-b")
	pathA := gatewaydStatePathForHome(home, repoA)
	pathB := gatewaydStatePathForHome(home, repoB)
	want := filepath.Join(home, ".cag", "gatewayd", gatewaydStateFileName)
	if pathA != want {
		t.Fatalf("state path should be global under ~/.cag/gatewayd, got=%s want=%s", pathA, want)
	}
	if pathB != want {
		t.Fatalf("state path should ignore repo root, got=%s want=%s", pathB, want)
	}
}

func TestCLISessionDeleteAndRecreateClosedLoop(t *testing.T) {
	resetSharedTestGatewayd(t)

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
	if workdirForKey(before.Items, key) != "" {
		t.Fatalf("expected empty workdir before first send, key=%s items=%+v", key, before.Items)
	}

	res = runBin(t, bin, repo, "send", "--session-key", key, "--text", "ping-with-default-workdir", "--dry-run", "--json")
	if res.Code != 0 {
		t.Fatalf("send dry-run with default workdir failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	sendWithDefault := parseSendJSON(t, res.Stdout)
	if !sendWithDefault.OK {
		t.Fatalf("expected send ok with default workdir fallback, got: %+v", sendWithDefault)
	}
	defaultWorkdir := filepath.Join(sharedTestHome, ".cag", "workspace", "default")
	if err := waitForPath(defaultWorkdir, 2*time.Second); err != nil {
		t.Fatalf("expected default workdir created at %s: %v", defaultWorkdir, err)
	}
	res = runBin(t, bin, repo, "sessions", "--json")
	if res.Code != 0 {
		t.Fatalf("sessions after default send failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	afterDefaultSend := parseSessionsJSON(t, res.Stdout)
	gotDefaultWorkdir := workdirForKey(afterDefaultSend.Items, key)
	if filepath.Clean(gotDefaultWorkdir) != filepath.Clean(defaultWorkdir) {
		t.Fatalf("expected default workdir=%s got=%s", defaultWorkdir, gotDefaultWorkdir)
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
	res = runBin(t, bin, repo, "messages", "--session-key", key, "--json")
	if res.Code != 0 {
		t.Fatalf("messages after recreate failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	msgAfterRecreate := parseMessagesJSON(t, res.Stdout)
	if len(msgAfterRecreate.Messages) != 0 {
		t.Fatalf("expected no old messages after session recreate, got=%+v", msgAfterRecreate.Messages)
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

func TestCLISessionDeleteThenInboundCreatesNewSessionSegment(t *testing.T) {
	resetSharedTestGatewayd(t)

	bin := buildGatewayBinary(t)
	repo := createTempRepo(t)
	setStorageBackend(t, repo, "localfile")
	msgID := "msg-segment-1"
	sender := "u-test-segment"
	channel := "dingtalk"
	threadID := "-"
	key := buildSessionKey(channel, sender, threadID)
	ts := "2026-03-06T03:00:00Z"

	res := runBin(t, bin, repo, "user-allow", "--channel", channel, "--user-id", sender, "--json")
	if res.Code != 0 {
		t.Fatalf("user-allow failed: code=%d stderr=%s", res.Code, res.Stderr)
	}

	interactionPath := filepath.Join(repo, ".agent_gateway_interactions.jsonl")
	lines := []map[string]any{
		{
			"kind":   "inbound_received",
			"msg_id": msgID,
			"sender": sender,
			"text":   "hello-1",
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
			"session_id":  "sid-segment-1",
			"ts":          ts,
		},
	}
	writeJSONL(t, interactionPath, lines)

	res = runBin(t, bin, repo, "session-delete", "--session-key", key, "--json")
	if res.Code != 0 {
		t.Fatalf("session-delete failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	res = runBin(t, bin, repo, "users", "--json")
	if res.Code != 0 {
		t.Fatalf("users after delete failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	usersAfterDelete := parseUsersJSON(t, res.Stdout)
	if !containsUserWithStatus(usersAfterDelete.Items, channel, sender, "allowed") {
		t.Fatalf("expected user access unchanged after session-delete, users=%+v", usersAfterDelete.Items)
	}
	newInboundTS := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339Nano)

	appendJSONL(t, interactionPath, map[string]any{
		"kind":   "inbound_received",
		"msg_id": "msg-segment-2",
		"sender": sender,
		"text":   "hello-2",
		"time":   newInboundTS,
		"user_profile": map[string]any{
			"channel":     channel,
			"sender":      sender,
			"sender_name": "UTest",
			"thread_id":   threadID,
		},
	})

	res = runBin(t, bin, repo, "sessions", "--json")
	if res.Code != 0 {
		t.Fatalf("sessions failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	items := parseSessionsJSON(t, res.Stdout).Items
	if len(items) == 0 {
		t.Fatalf("expected reopened session segment, got empty items")
	}
	if containsSessionKey(items, key) {
		t.Fatalf("expected old session key to stay deleted, key=%s items=%+v", key, items)
	}
	newKey := strings.TrimSpace(items[0].SessionKey)
	if !strings.HasPrefix(newKey, key+"_r") {
		t.Fatalf("expected reopened key prefix=%s_r got=%s", key, newKey)
	}

	res = runBin(t, bin, repo, "messages", "--session-key", key, "--json")
	if res.Code != 0 {
		t.Fatalf("messages old key failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	oldMsgs := parseMessagesJSON(t, res.Stdout)
	if len(oldMsgs.Messages) != 0 {
		t.Fatalf("expected no messages for deleted key, got=%+v", oldMsgs.Messages)
	}

	res = runBin(t, bin, repo, "messages", "--session-key", newKey, "--json")
	if res.Code != 0 {
		t.Fatalf("messages new key failed: code=%d stderr=%s", res.Code, res.Stderr)
	}
	newMsgs := parseMessagesJSON(t, res.Stdout)
	if len(newMsgs.Messages) == 0 {
		t.Fatalf("expected messages for reopened key, got empty")
	}
}

func buildGatewayBinary(t *testing.T) string {
	t.Helper()
	sharedTestBinOnce.Do(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			sharedTestBinErr = fmt.Errorf("failed to locate test file path")
			return
		}
		cmdDir := filepath.Dir(thisFile)
		moduleRoot := filepath.Clean(filepath.Join(cmdDir, "..", ".."))
		sharedTestBin = filepath.Join(sharedTestHome, "cag-test-bin")
		cmd := exec.Command("go", "build", "-o", sharedTestBin, "./cmd/gateway-cli")
		cmd.Dir = moduleRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			sharedTestBinErr = fmt.Errorf("build binary failed: %v\n%s", err, string(out))
		}
	})
	if sharedTestBinErr != nil {
		t.Fatal(sharedTestBinErr)
	}
	return sharedTestBin
}

func createTempRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	envPath := filepath.Join(repo, ".env")
	content := strings.Join([]string{
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

func waitForPath(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(120 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = os.ErrNotExist
	}
	return lastErr
}

func runBin(t *testing.T, bin, dir string, args ...string) cmdResult {
	t.Helper()
	sharedCLIMu.Lock()
	defer sharedCLIMu.Unlock()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+sharedTestHome,
		"GATEWAYD_ADDR="+testGatewaydAddr(dir),
	)
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
	sharedCLIMu.Lock()
	defer sharedCLIMu.Unlock()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	baseEnv := append(os.Environ(),
		"HOME="+sharedTestHome,
		"GATEWAYD_ADDR="+testGatewaydAddr(dir),
	)
	cmd.Env = append(baseEnv, env...)
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
	_ = dir
	return sharedTestGatewayAddr
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

func parseMessagesJSON(t *testing.T, out string) MessagesPayload {
	t.Helper()
	var node MessagesPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid messages json: %v\nraw=%q", err, out)
	}
	return node
}

func parseUsersJSON(t *testing.T, out string) UsersPayload {
	t.Helper()
	var node UsersPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &node); err != nil {
		t.Fatalf("invalid users json: %v\nraw=%q", err, out)
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

func containsUserWithStatus(items []UserAccessItem, channel, userID, status string) bool {
	for _, it := range items {
		if strings.EqualFold(strings.TrimSpace(it.Channel), strings.TrimSpace(channel)) &&
			strings.EqualFold(strings.TrimSpace(it.UserID), strings.TrimSpace(userID)) &&
			strings.EqualFold(strings.TrimSpace(it.Status), strings.TrimSpace(status)) {
			return true
		}
	}
	return false
}

func workdirForKey(items []SessionsItem, key string) string {
	for _, it := range items {
		if strings.TrimSpace(it.SessionKey) == strings.TrimSpace(key) {
			return strings.TrimSpace(it.Workdir)
		}
	}
	return ""
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

func appendJSONL(t *testing.T, path string, node map[string]any) {
	t.Helper()
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal jsonl append node failed: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open jsonl append failed: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(string(raw) + "\n"); err != nil {
		t.Fatalf("append jsonl failed: %v", err)
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

func replaceEnvValue(t *testing.T, repo, from, to string) {
	t.Helper()
	envPath := filepath.Join(repo, ".env")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	content := strings.ReplaceAll(string(raw), from, to)
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}
}

func shellQuoteForTest(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
