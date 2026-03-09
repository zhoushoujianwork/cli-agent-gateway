package main

import (
	"bufio"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cli-agent-gateway/internal/agents/acp"
	"cli-agent-gateway/internal/channels/command"
	"cli-agent-gateway/internal/channels/dingtalk"
	"cli-agent-gateway/internal/channels/imessage"
	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/core"
	"cli-agent-gateway/internal/infra/envfile"
	"cli-agent-gateway/internal/infra/lockfile"
	"cli-agent-gateway/internal/storage"
	"cli-agent-gateway/internal/utils/sessionctl"

	_ "modernc.org/sqlite"
)

type HealthItem struct {
	Key        string `json:"key"`
	OK         bool   `json:"ok"`
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion,omitempty"`
}

type HealthPayload struct {
	OK      bool         `json:"ok"`
	Action  string       `json:"action"`
	Status  string       `json:"status"`
	Channel string       `json:"channel,omitempty"`
	Items   []HealthItem `json:"items"`
}

type JSONError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type StatusPayload struct {
	Running   bool           `json:"running"`
	PID       *int           `json:"pid,omitempty"`
	StartedAt string         `json:"started_at,omitempty"`
	LockFile  string         `json:"lock_file"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type SendPayload struct {
	OK             bool   `json:"ok"`
	Channel        string `json:"channel"`
	To             string `json:"to"`
	MessageID      string `json:"message_id"`
	MsgType        string `json:"msg_type"`
	DryRun         bool   `json:"dry_run"`
	Source         string `json:"source"`
	SessionKey     string `json:"session_key,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Result         string `json:"result,omitempty"`
	RawOutput      string `json:"raw_output,omitempty"`
	ResultJSON     any    `json:"result_json,omitempty"`
	TerminalReason string `json:"terminal_reason"`
	ElapsedSec     int    `json:"elapsed_sec"`
	Error          string `json:"error,omitempty"`
	Warning        string `json:"warning,omitempty"`
}

type SessionsItem struct {
	SessionKey  string `json:"session_key"`
	SessionID   string `json:"session_id,omitempty"`
	Channel     string `json:"channel"`
	Sender      string `json:"sender"`
	SenderName  string `json:"sender_name"`
	ThreadID    string `json:"thread_id"`
	LastMessage string `json:"last_message"`
	LastTime    string `json:"last_time"`
	Workdir     string `json:"workdir,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Status      string `json:"status,omitempty"`
	Latest      bool   `json:"latest,omitempty"`
}

type SessionsPayload struct {
	OK     bool           `json:"ok"`
	Action string         `json:"action"`
	Items  []SessionsItem `json:"items"`
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve cwd: %v\n", err)
		os.Exit(1)
	}
	repoRoot := detectRepoRoot(cwd)
	os.Exit(executeRoot(repoRoot, os.Args[1:]))
}

func runGoMain(repoRoot string, args []string) int {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		fmt.Fprintln(os.Stderr, "run does not accept workdir arg; session workdir should be supplied per session")
		return 2
	}
	workdir := ""
	cfg, err := config.Load(repoRoot, workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return 1
	}

	if _, err := os.Stat(cfg.Workdir); err != nil {
		fmt.Fprintf(os.Stderr, "[FATAL] invalid workdir: %s\n", cfg.Workdir)
		return 2
	}

	lock := lockfile.NewProcessLock(cfg.LockFile)
	ok, err := lock.Acquire()
	if err != nil {
		fmt.Fprintf(os.Stderr, "acquire lock failed: %v\n", err)
		return 2
	}
	if !ok {
		st, _ := lockfile.Inspect(cfg.LockFile)
		pid := "unknown"
		started := "unknown"
		if st.OwnerPID != nil {
			pid = fmt.Sprintf("%d", *st.OwnerPID)
		}
		if st.OwnerStartedAt != nil {
			started = *st.OwnerStartedAt
		}
		fmt.Fprintf(os.Stderr, "[FATAL] 检测到上一个网关实例未结束，请先关闭后再启动。 lock=%s pid=%s started_at=%s\n", cfg.LockFile, pid, started)
		return 2
	}
	defer lock.Close()
	runtimeLogFile := strings.TrimSpace(os.Getenv("GATEWAY_LOG_FILE"))
	if runtimeLogFile == "" {
		runtimeLogFile = resolveLogPath(repoRoot, nil)
	}
	_ = lock.WriteMetadata(map[string]any{
		"workdir":    cfg.Workdir,
		"lock_file":  cfg.LockFile,
		"channel":    cfg.ChannelType,
		"log_file":   runtimeLogFile,
		"started_at": time.Now().UTC().Format(time.RFC3339),
	})

	fmt.Printf("[%s] startup channel=%s workdir=%s\n", time.Now().UTC().Format(time.RFC3339), cfg.ChannelType, cfg.Workdir)
	fmt.Printf("[%s] startup acp_cmd=%s permission_policy=%s\n", time.Now().UTC().Format(time.RFC3339), cfg.ACPAgentCmd, cfg.PermissionPolicy)

	if err := ensureGatewaydRunning(repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "ensure gatewayd failed: %v\n", err)
		return 1
	}

	channel := buildChannelAdapter(cfg)
	agent := &gatewaySessionProxyAgent{repoRoot: repoRoot}
	defer agent.Close()

	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init storage backend failed: %v\n", err)
		return 1
	}
	loop := &core.Loop{
		Channel:             channel,
		Agent:               agent,
		Storage:             store,
		RemoteUserID:        cfg.RemoteUserID,
		AllowedFrom:         cfg.AllowedFrom,
		PendingUnknownUsers: false,
		ProcessOnlyLatest:   cfg.ProcessOnlyLatest,
		PollIntervalSec:     cfg.PollIntervalSec,
		ReplyStyleEnabled:   cfg.ReplyStyleEnabled,
		ReplyStylePrompt:    cfg.ReplyStylePrompt,
	}
	if strings.EqualFold(strings.TrimSpace(cfg.ChannelType), "dingtalk") {
		loop.AllowedFrom = cfg.DingTalkAllowedFrom
		loop.RemoteUserID = strings.TrimSpace(cfg.DingTalkDefaultTo)
		loop.PendingUnknownUsers = !strings.EqualFold(strings.TrimSpace(cfg.DingTalkDMPolicy), "allow_all")
	}
	if err := sendStartupGreeting(cfg, channel); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] send startup greeting failed: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[INFO] startup greeting sent channel=%s target=%s\n", cfg.ChannelType, nonEmpty(startupGreetingTarget(cfg), "-"))
		if warning := popChannelSendWarning(channel); warning != "" {
			fmt.Fprintf(os.Stderr, "[WARN] startup greeting degraded: %s\n", warning)
		}
	}
	if err := loop.RunForever(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway loop failed: %v\n", err)
		return 1
	}
	return 0
}

func hasAllowedUserAccess(repoRoot, channel string) bool {
	items, err := loadUserAccessItems(repoRoot)
	if err != nil {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Channel), channel) && strings.EqualFold(strings.TrimSpace(item.Status), "allowed") {
			return true
		}
	}
	return false
}

func runStatus(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if !grpcDisabled() {
		grpc, err := tryStatusViaGRPC(repoRoot)
		if err != nil {
			if jsonOut {
				printJSONActionError("status", "gateway_unreachable", formatGatewayUnavailable(err))
				return 1
			}
			fmt.Fprintf(os.Stderr, "status failed: %s\n", formatGatewayUnavailable(err))
			return 1
		}
		if !grpc.GetOk() {
			if jsonOut {
				printJSONActionError("status", "grpc_status_failed", grpc.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "status failed: %s\n", grpc.GetError())
			return 1
		}
		payload := StatusPayload{
			Running:   grpc.GetRunning(),
			StartedAt: grpc.GetStartedAt(),
			LockFile:  grpc.GetLockFile(),
			Metadata:  map[string]any{},
		}
		if grpc.GetHasPid() {
			pid := int(grpc.GetPid())
			payload.PID = &pid
		}
		payload.Metadata["channel"] = grpc.GetChannel()
		payload.Metadata["workdir"] = grpc.GetWorkdir()
		payload.Metadata["log_file"] = grpc.GetLogFile()
		payload.Metadata["lock_file"] = grpc.GetLockFile()
		if grpc.GetHasPid() {
			payload.Metadata["pid"] = grpc.GetPid()
		}
		if strings.TrimSpace(grpc.GetStartedAt()) != "" {
			payload.Metadata["started_at"] = grpc.GetStartedAt()
		}
		cfg, _ := config.Load(repoRoot, "")
		if jsonOut {
			printJSON(statusJSON("status", payload, cfg, grpc.GetLogFile()))
			return 0
		}
		fmt.Print(renderPlainStatus(repoRoot, payload, cfg, grpc.GetLogFile()))
		return 0
	}

	payload, err := getStatusPayload(repoRoot)
	if err != nil {
		if jsonOut {
			printJSONActionError("status", "status_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
		return 1
	}
	cfg, _ := config.Load(repoRoot, "")
	if jsonOut {
		printJSON(statusJSON("status", payload, cfg, ""))
		return 0
	}
	fmt.Print(renderPlainStatus(repoRoot, payload, cfg, ""))
	return 0
}

func runStart(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if !grpcDisabled() {
		if err := ensureGatewaydRunning(repoRoot); err != nil {
			if jsonOut {
				printJSONActionError("start", "gateway_unreachable", formatGatewayUnavailable(err))
				return 1
			}
			fmt.Fprintf(os.Stderr, "start failed: %s\n", formatGatewayUnavailable(err))
			return 1
		}
		requestedLog := strings.TrimSpace(flagValue(args, "--log-file"))
		grpcRes, gerr := tryStartViaGRPC(repoRoot, requestedLog)
		if gerr != nil {
			if jsonOut {
				printJSONActionError("start", "gateway_unreachable", formatGatewayUnavailable(gerr))
				return 1
			}
			fmt.Fprintf(os.Stderr, "start failed: %s\n", formatGatewayUnavailable(gerr))
			return 1
		}
		if !grpcRes.GetOk() {
			if jsonOut {
				printJSONActionError("start", "grpc_start_failed", grpcRes.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "start failed: %s\n", grpcRes.GetError())
			return 1
		}
		payload := StatusPayload{
			Running:   grpcRes.GetRunning(),
			StartedAt: grpcRes.GetStartedAt(),
			LockFile:  grpcRes.GetLockFile(),
			Metadata:  map[string]any{},
		}
		if grpcRes.GetHasPid() {
			pid := int(grpcRes.GetPid())
			payload.PID = &pid
		}
		payload.Metadata["channel"] = grpcRes.GetChannel()
		payload.Metadata["workdir"] = grpcRes.GetWorkdir()
		payload.Metadata["log_file"] = grpcRes.GetLogFile()
		payload.Metadata["lock_file"] = grpcRes.GetLockFile()
		cfg, _ := config.Load(repoRoot, "")
		if jsonOut {
			printJSON(statusJSON("start", payload, cfg, grpcRes.GetLogFile()))
		} else {
			fmt.Printf("started lock=%s log=%s\n", payload.LockFile, nonEmpty(strings.TrimSpace(grpcRes.GetLogFile()), resolveLogPath(repoRoot, nil)))
		}
		return 0
	}

	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		if jsonOut {
			printJSONActionError("start", "config_load_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return 1
	}
	current, err := getStatusPayload(repoRoot)
	if err == nil && current.Running {
		if jsonOut {
			printJSON(statusJSON("start", current, cfg, ""))
		} else {
			fmt.Println("already running")
		}
		return 0
	}

	logPath := resolveFreshLogPath(repoRoot, args)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		if jsonOut {
			printJSONActionError("start", "log_prepare_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "create log dir failed: %v\n", err)
		return 1
	}
	logFile, err := os.OpenFile(logPath, runtimeLogOpenFlags(args), 0o644)
	if err != nil {
		if jsonOut {
			printJSONActionError("start", "log_open_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "open log file failed: %v\n", err)
		return 1
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		if jsonOut {
			printJSONActionError("start", "executable_resolve_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "resolve executable failed: %v\n", err)
		return 1
	}
	proc := exec.Command(exe, "run")
	proc.Dir = repoRoot
	proc.Stdout = logFile
	proc.Stderr = logFile
	proc.Env = managedChildEnv("GATEWAY_LOG_FILE="+logPath, "CAG_GRPC_DISABLE=")
	configureDetachedProcess(proc)

	if err := proc.Start(); err != nil {
		if jsonOut {
			printJSONActionError("start", "start_process_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "start process failed: %v\n", err)
		return 1
	}
	_ = proc.Process.Release()

	payload, err := waitForRunningStatus(repoRoot, 5*time.Second)
	if err != nil {
		if jsonOut {
			printJSONActionError("start", "status_after_start_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "status after start failed: %v\n", err)
		return 1
	}
	if !payload.Running {
		if jsonOut {
			printJSONActionError("start", "start_not_running", "start requested but process is not running yet")
		} else {
			fmt.Printf("start requested but not running yet, check log=%s\n", logPath)
		}
		return 1
	}
	if jsonOut {
		printJSON(statusJSON("start", payload, cfg, logPath))
	} else {
		fmt.Printf("started lock=%s log=%s\n", payload.LockFile, logPath)
	}
	_ = cfg
	return 0
}

func runStop(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	quiet := hasFlag(args, "--quiet")
	cfg, _ := config.Load(repoRoot, "")
	if !grpcDisabled() {
		grpcRes, gerr := tryStopViaGRPC(repoRoot, quiet)
		if gerr != nil {
			if jsonOut {
				printJSONActionError("stop", "gateway_unreachable", formatGatewayUnavailable(gerr))
				return 1
			}
			fmt.Fprintf(os.Stderr, "stop failed: %s\n", formatGatewayUnavailable(gerr))
			return 1
		}
		if !grpcRes.GetOk() {
			if jsonOut {
				printJSONActionError("stop", "grpc_stop_failed", grpcRes.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "stop failed: %s\n", grpcRes.GetError())
			return 1
		}
		if quiet {
			return 0
		}
		payload := StatusPayload{
			Running:   grpcRes.GetRunning(),
			StartedAt: grpcRes.GetStartedAt(),
			LockFile:  grpcRes.GetLockFile(),
			Metadata:  map[string]any{},
		}
		if grpcRes.GetHasPid() {
			pid := int(grpcRes.GetPid())
			payload.PID = &pid
		}
		payload.Metadata["channel"] = grpcRes.GetChannel()
		payload.Metadata["workdir"] = grpcRes.GetWorkdir()
		payload.Metadata["log_file"] = grpcRes.GetLogFile()
		payload.Metadata["lock_file"] = grpcRes.GetLockFile()
		if jsonOut {
			printJSON(statusJSON("stop", payload, cfg, grpcRes.GetLogFile()))
		} else if !payload.Running {
			if payload.PID != nil && *payload.PID > 0 {
				fmt.Printf("stopped pid=%d lock=%s\n", *payload.PID, payload.LockFile)
			} else {
				fmt.Printf("stopped lock=%s\n", payload.LockFile)
			}
		} else {
			if payload.PID != nil && *payload.PID > 0 {
				fmt.Printf("stop requested but still running pid=%d\n", *payload.PID)
			} else {
				fmt.Printf("stop requested but still running lock=%s\n", payload.LockFile)
			}
			return 1
		}
		return 0
	}

	payload, err := getStatusPayload(repoRoot)
	if err != nil {
		if jsonOut {
			printJSONActionError("stop", "status_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
		return 1
	}
	if !payload.Running {
		if quiet {
			return 0
		}
		if jsonOut {
			printJSON(statusJSON("stop", payload, cfg, ""))
		} else {
			fmt.Println("already stopped")
		}
		return 0
	}
	if payload.PID == nil || *payload.PID <= 0 {
		if jsonOut {
			printJSONActionError("stop", "pid_missing", "cannot stop: lock is held but pid missing")
			return 1
		}
		fmt.Fprintln(os.Stderr, "cannot stop: lock is held but pid missing")
		return 1
	}
	proc, err := os.FindProcess(*payload.PID)
	if err != nil {
		if jsonOut {
			printJSONActionError("stop", "find_process_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "find process failed: %v\n", err)
		return 1
	}
	if err := signalTerminate(proc); err != nil {
		if jsonOut {
			printJSONActionError("stop", "terminate_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "terminate failed: %v\n", err)
		return 1
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(*payload.PID) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if processAlive(*payload.PID) {
		_ = signalKill(proc)
		time.Sleep(300 * time.Millisecond)
	}

	after, err := getStatusPayload(repoRoot)
	if err != nil {
		if jsonOut {
			printJSONActionError("stop", "status_after_stop_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "status after stop failed: %v\n", err)
		return 1
	}
	if quiet {
		return 0
	}
	if jsonOut {
		printJSON(statusJSON("stop", after, cfg, ""))
	} else if !after.Running {
		fmt.Printf("stopped pid=%d lock=%s\n", *payload.PID, after.LockFile)
	} else {
		if jsonOut {
			printJSONActionError("stop", "stop_still_running", fmt.Sprintf("stop requested but still running pid=%d", *payload.PID))
			return 1
		}
		fmt.Printf("stop requested but still running pid=%d\n", *payload.PID)
		return 1
	}
	return 0
}

func runRestart(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if !grpcDisabled() {
		if err := ensureGatewaydRunning(repoRoot); err != nil {
			if jsonOut {
				printJSONActionError("restart", "gateway_unreachable", formatGatewayUnavailable(err))
				return 1
			}
			fmt.Fprintf(os.Stderr, "restart failed: %s\n", formatGatewayUnavailable(err))
			return 1
		}
		requestedLog := strings.TrimSpace(flagValue(args, "--log-file"))
		grpcRes, gerr := tryRestartViaGRPC(repoRoot, requestedLog)
		if gerr == nil && grpcRes != nil && !grpcRes.GetOk() && shouldRefreshGatewaydForRestart(grpcRes.GetError()) {
			_, _ = shutdownManagedGatewayd(repoRoot)
			if ensureErr := ensureGatewaydRunning(repoRoot); ensureErr == nil {
				grpcRes, gerr = tryRestartViaGRPC(repoRoot, requestedLog)
			}
		}
		if gerr != nil {
			if jsonOut {
				printJSONActionError("restart", "gateway_unreachable", formatGatewayUnavailable(gerr))
				return 1
			}
			fmt.Fprintf(os.Stderr, "restart failed: %s\n", formatGatewayUnavailable(gerr))
			return 1
		}
		if !grpcRes.GetOk() {
			if jsonOut {
				printJSONActionError("restart", "grpc_restart_failed", grpcRes.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "restart failed: %s\n", grpcRes.GetError())
			return 1
		}
		payload := StatusPayload{
			Running:   grpcRes.GetRunning(),
			StartedAt: grpcRes.GetStartedAt(),
			LockFile:  grpcRes.GetLockFile(),
			Metadata:  map[string]any{},
		}
		if grpcRes.GetHasPid() {
			pid := int(grpcRes.GetPid())
			payload.PID = &pid
		}
		payload.Metadata["channel"] = grpcRes.GetChannel()
		payload.Metadata["workdir"] = grpcRes.GetWorkdir()
		payload.Metadata["log_file"] = grpcRes.GetLogFile()
		payload.Metadata["lock_file"] = grpcRes.GetLockFile()
		cfg, _ := config.Load(repoRoot, "")
		if jsonOut {
			printJSON(statusJSON("restart", payload, cfg, grpcRes.GetLogFile()))
		} else {
			fmt.Printf("restarted lock=%s log=%s\n", payload.LockFile, nonEmpty(strings.TrimSpace(grpcRes.GetLogFile()), resolveLogPath(repoRoot, nil)))
		}
		return 0
	}

	if !jsonOut {
		if code := runStop(repoRoot, args); code != 0 {
			return code
		}
		return runStart(repoRoot, args)
	}

	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		printJSONActionError("restart", "config_load_failed", err.Error())
		return 1
	}
	current, err := getStatusPayload(repoRoot)
	if err != nil {
		printJSONActionError("restart", "status_failed", err.Error())
		return 1
	}
	if current.Running {
		if current.PID == nil || *current.PID <= 0 {
			printJSONActionError("restart", "pid_missing", "cannot restart: lock is held but pid missing")
			return 1
		}
		proc, err := os.FindProcess(*current.PID)
		if err != nil {
			printJSONActionError("restart", "find_process_failed", err.Error())
			return 1
		}
		if err := signalTerminate(proc); err != nil {
			printJSONActionError("restart", "terminate_failed", err.Error())
			return 1
		}
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(*current.PID) {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if processAlive(*current.PID) {
			_ = signalKill(proc)
			time.Sleep(300 * time.Millisecond)
		}
	}

	logPath := resolveFreshLogPath(repoRoot, args)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		printJSONActionError("restart", "log_prepare_failed", err.Error())
		return 1
	}
	logFile, err := os.OpenFile(logPath, runtimeLogOpenFlags(args), 0o644)
	if err != nil {
		printJSONActionError("restart", "log_open_failed", err.Error())
		return 1
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		printJSONActionError("restart", "executable_resolve_failed", err.Error())
		return 1
	}
	p := exec.Command(exe, "run")
	p.Dir = repoRoot
	p.Stdout = logFile
	p.Stderr = logFile
	p.Env = managedChildEnv("GATEWAY_LOG_FILE="+logPath, "CAG_GRPC_DISABLE=")
	configureDetachedProcess(p)
	if err := p.Start(); err != nil {
		printJSONActionError("restart", "start_process_failed", err.Error())
		return 1
	}
	_ = p.Process.Release()
	time.Sleep(800 * time.Millisecond)

	after, err := waitForRunningStatus(repoRoot, 5*time.Second)
	if err != nil {
		printJSONActionError("restart", "status_after_restart_failed", err.Error())
		return 1
	}
	printJSON(statusJSON("restart", after, cfg, logPath))
	return 0
}

func shouldRefreshGatewaydForRestart(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(lower, "fork/exec") && strings.Contains(lower, "no such file or directory")
}

func runHealth(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if !grpcDisabled() {
		grpcRes, gerr := tryHealthViaGRPC(repoRoot, false)
		if gerr != nil {
			if jsonOut {
				printJSONActionError("health", "gateway_unreachable", formatGatewayUnavailable(gerr))
				return 1
			}
			fmt.Fprintf(os.Stderr, "health failed: %s\n", formatGatewayUnavailable(gerr))
			return 1
		}
		if !grpcRes.GetOk() && len(grpcRes.GetItems()) == 0 && strings.TrimSpace(grpcRes.GetError()) != "" {
			if jsonOut {
				printJSONActionError("health", "grpc_health_failed", grpcRes.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "health failed: %s\n", grpcRes.GetError())
			return 1
		}
		p := HealthPayload{
			OK:      grpcRes.GetOk(),
			Action:  nonEmpty(strings.TrimSpace(grpcRes.GetAction()), "health"),
			Status:  nonEmpty(strings.TrimSpace(grpcRes.GetStatus()), "unhealthy"),
			Channel: strings.TrimSpace(grpcRes.GetChannel()),
			Items:   make([]HealthItem, 0, len(grpcRes.GetItems())),
		}
		for _, it := range grpcRes.GetItems() {
			p.Items = append(p.Items, HealthItem{
				Key:        it.GetKey(),
				OK:         it.GetOk(),
				Detail:     it.GetDetail(),
				Suggestion: it.GetSuggestion(),
			})
		}
		if jsonOut {
			printJSON(p)
		} else {
			for _, it := range p.Items {
				if it.OK {
					fmt.Printf("[OK] %s: %s\n", it.Key, it.Detail)
				} else {
					fmt.Printf("[FAIL] %s: %s\n", it.Key, it.Detail)
				}
			}
		}
		if p.OK {
			return 0
		}
		return 1
	}

	p := buildHealthPayload(repoRoot, "health", false)
	if jsonOut {
		printJSON(p)
	} else {
		for _, it := range p.Items {
			if it.OK {
				fmt.Printf("[OK] %s: %s\n", it.Key, it.Detail)
			} else {
				fmt.Printf("[FAIL] %s: %s\n", it.Key, it.Detail)
			}
		}
	}
	if p.OK {
		return 0
	}
	return 1
}

func runDoctor(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if !grpcDisabled() {
		grpcRes, gerr := tryDoctorViaGRPC(repoRoot, true)
		if gerr != nil {
			if jsonOut {
				printJSONActionError("doctor", "gateway_unreachable", formatGatewayUnavailable(gerr))
				return 1
			}
			fmt.Fprintf(os.Stderr, "doctor failed: %s\n", formatGatewayUnavailable(gerr))
			return 1
		}
		if !grpcRes.GetOk() && len(grpcRes.GetItems()) == 0 && strings.TrimSpace(grpcRes.GetError()) != "" {
			if jsonOut {
				printJSONActionError("doctor", "grpc_doctor_failed", grpcRes.GetError())
				return 1
			}
			fmt.Fprintf(os.Stderr, "doctor failed: %s\n", grpcRes.GetError())
			return 1
		}
		p := HealthPayload{
			OK:      grpcRes.GetOk(),
			Action:  nonEmpty(strings.TrimSpace(grpcRes.GetAction()), "doctor"),
			Status:  nonEmpty(strings.TrimSpace(grpcRes.GetStatus()), "unhealthy"),
			Channel: strings.TrimSpace(grpcRes.GetChannel()),
			Items:   make([]HealthItem, 0, len(grpcRes.GetItems())),
		}
		for _, it := range grpcRes.GetItems() {
			p.Items = append(p.Items, HealthItem{
				Key:        it.GetKey(),
				OK:         it.GetOk(),
				Detail:     it.GetDetail(),
				Suggestion: it.GetSuggestion(),
			})
		}
		if jsonOut {
			printJSON(p)
		} else {
			for _, it := range p.Items {
				if it.OK {
					fmt.Printf("[OK] %s: %s\n", it.Key, it.Detail)
				} else {
					fmt.Printf("[FAIL] %s: %s\n", it.Key, it.Detail)
				}
			}
		}
		if p.OK {
			return 0
		}
		return 1
	}

	p := buildHealthPayload(repoRoot, "doctor", true)
	if jsonOut {
		printJSON(p)
	} else {
		for _, it := range p.Items {
			if it.OK {
				fmt.Printf("[OK] %s: %s\n", it.Key, it.Detail)
			} else {
				fmt.Printf("[FAIL] %s: %s\n", it.Key, it.Detail)
			}
		}
	}
	if p.OK {
		return 0
	}
	return 1
}

func runSend(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	text := fs.String("text", "", "message text")
	fileInput := fs.String("file", "", "read message body from file")
	to := fs.String("to", "", "target receiver/user")
	sessionKey := fs.String("session-key", "", "execute in an existing session key (GUI)")
	workdirArg := fs.String("workdir", "", "workdir override for session execution")
	sessionWebhook := fs.String("session-webhook", "", "dingtalk session webhook URL for in-thread reply")
	channelOverride := fs.String("channel", "", "channel override: command|dingtalk|imessage")
	msgType := fs.String("msgtype", "text", "message type: text|markdown")
	messageID := fs.String("message-id", "", "message id")
	reportFile := fs.String("report-file", "", "report file path")
	dryRun := fs.Bool("dry-run", false, "validate and print payload without sending")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mt := strings.ToLower(strings.TrimSpace(*msgType))
	if mt != "text" && mt != "markdown" {
		fmt.Fprintln(os.Stderr, "send --msgtype must be one of: text, markdown")
		return 2
	}

	textIn := strings.TrimSpace(*text)
	fileIn := strings.TrimSpace(*fileInput)
	if (textIn == "" && fileIn == "") || (textIn != "" && fileIn != "") {
		fmt.Fprintln(os.Stderr, "send requires exactly one input source: --text or --file")
		return 2
	}

	body := textIn
	source := "text"
	if fileIn != "" {
		path := fileIn
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read send --file failed: %v\n", err)
			return 1
		}
		body = strings.TrimSpace(string(raw))
		source = "file"
	}
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(os.Stderr, "send body is empty")
		return 2
	}

	origChannel, hasOrigChannel := os.LookupEnv("CHANNEL_TYPE")
	if strings.TrimSpace(*channelOverride) != "" {
		_ = os.Setenv("CHANNEL_TYPE", strings.TrimSpace(*channelOverride))
		defer func() {
			if hasOrigChannel {
				_ = os.Setenv("CHANNEL_TYPE", origChannel)
				return
			}
			_ = os.Unsetenv("CHANNEL_TYPE")
		}()
	}
	origMsgType, hasOrigMsgType := os.LookupEnv("DINGTALK_SEND_MSGTYPE")
	_ = os.Setenv("DINGTALK_SEND_MSGTYPE", mt)
	defer func() {
		if hasOrigMsgType {
			_ = os.Setenv("DINGTALK_SEND_MSGTYPE", origMsgType)
			return
		}
		_ = os.Unsetenv("DINGTALK_SEND_MSGTYPE")
	}()
	webhookURL := strings.TrimSpace(*sessionWebhook)
	if webhookURL != "" {
		origMode, hasOrigMode := os.LookupEnv("DINGTALK_SEND_MODE")
		origWebhook, hasOrigWebhook := os.LookupEnv("DINGTALK_BOT_WEBHOOK")
		_ = os.Setenv("DINGTALK_SEND_MODE", "webhook")
		_ = os.Setenv("DINGTALK_BOT_WEBHOOK", webhookURL)
		defer func() {
			if hasOrigMode {
				_ = os.Setenv("DINGTALK_SEND_MODE", origMode)
			} else {
				_ = os.Unsetenv("DINGTALK_SEND_MODE")
			}
			if hasOrigWebhook {
				_ = os.Setenv("DINGTALK_BOT_WEBHOOK", origWebhook)
			} else {
				_ = os.Unsetenv("DINGTALK_BOT_WEBHOOK")
			}
		}()
	}

	key := strings.TrimSpace(*sessionKey)
	if key == "" && strings.TrimSpace(*workdirArg) != "" {
		fmt.Fprintln(os.Stderr, "send --workdir requires --session-key")
		return 2
	}
	if key != "" {
		return runDeprecatedCLI("send --session-key", "session send --key <session_key> --text <text>", *jsonOut)
	}

	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return 1
	}

	channel := buildChannelAdapter(cfg)

	target := strings.TrimSpace(*to)
	if target == "" && strings.EqualFold(cfg.ChannelType, "dingtalk") {
		target = strings.TrimSpace(cfg.DingTalkDefaultTo)
	}
	if target == "" && !(strings.EqualFold(cfg.ChannelType, "dingtalk") && webhookURL != "") {
		fmt.Fprintln(os.Stderr, "send requires --to (or DINGTALK_DEFAULT_TO_USER for dingtalk)")
		return 2
	}

	msgID := strings.TrimSpace(*messageID)
	if msgID == "" {
		msgID = fmt.Sprintf("manual-%d", time.Now().UnixMilli())
	}

	payload := SendPayload{
		OK:        true,
		Channel:   cfg.ChannelType,
		To:        nonEmpty(target, "-"),
		MessageID: msgID,
		MsgType:   mt,
		DryRun:    *dryRun,
		Source:    source,
	}

	if *dryRun {
		if *jsonOut {
			printJSON(payload)
		} else {
			fmt.Printf("dry-run channel=%s to=%s message_id=%s msgtype=%s source=%s\n", payload.Channel, payload.To, payload.MessageID, payload.MsgType, payload.Source)
		}
		return 0
	}

	sendErr := channel.Send(body, target, msgID, strings.TrimSpace(*reportFile))
	sendWarning := popChannelSendWarning(channel)
	if *jsonOut {
		payload.OK = sendErr == nil
		if sendErr != nil {
			payload.Error = sendErr.Error()
		}
		if sendWarning != "" {
			payload.Warning = sendWarning
		}
		printJSON(payload)
		if sendErr != nil {
			return 1
		}
		return 0
	}
	if sendErr != nil {
		fmt.Fprintf(os.Stderr, "send failed: %v\n", sendErr)
		return 1
	}
	if sendWarning != "" {
		fmt.Fprintf(os.Stderr, "[WARN] send warning: %s\n", sendWarning)
	}
	fmt.Printf("sent channel=%s to=%s message_id=%s msgtype=%s source=%s\n", cfg.ChannelType, target, msgID, mt, source)
	return 0
}

func sendViaSessionKey(cfg config.AppConfig, key, body, mt, source, msgID string, dryRun bool, workdirOverride string) (SendPayload, error) {
	fmt.Fprintf(os.Stderr, "[INFO] cli send start msg_id=%s session_key=%s channel=%s text=%s\n", msgID, key, cfg.ChannelType, shortLogText(body, 80))
	items, err := collectSessions(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] cli send session load failed msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return SendPayload{
			OK:         false,
			Channel:    cfg.ChannelType,
			To:         "-",
			MessageID:  msgID,
			MsgType:    mt,
			DryRun:     dryRun,
			Source:     source,
			SessionKey: key,
			Error:      err.Error(),
		}, err
	}
	var sess *SessionsItem
	for i := range items {
		if items[i].SessionKey == key {
			sess = &items[i]
			break
		}
	}
	if sess == nil {
		err := fmt.Errorf("session not found for key=%s", key)
		fmt.Fprintf(os.Stderr, "[WARN] cli send session missing msg_id=%s session_key=%s\n", msgID, key)
		return SendPayload{
			OK:         false,
			Channel:    cfg.ChannelType,
			To:         "-",
			MessageID:  msgID,
			MsgType:    mt,
			DryRun:     dryRun,
			Source:     source,
			SessionKey: key,
			Error:      err.Error(),
		}, err
	}

	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] cli send storage init failed msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return SendPayload{
			OK:         false,
			Channel:    nonEmpty(sess.Channel, cfg.ChannelType),
			To:         nonEmpty(sess.Sender, "-"),
			MessageID:  msgID,
			MsgType:    mt,
			DryRun:     dryRun,
			Source:     source,
			SessionKey: key,
			Error:      err.Error(),
		}, err
	}
	st, err := store.LoadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] cli send state load failed msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return SendPayload{
			OK:         false,
			Channel:    nonEmpty(sess.Channel, cfg.ChannelType),
			To:         nonEmpty(sess.Sender, "-"),
			MessageID:  msgID,
			MsgType:    mt,
			DryRun:     dryRun,
			Source:     source,
			SessionKey: key,
			Error:      err.Error(),
		}, err
	}
	if st.SessionMap == nil {
		st.SessionMap = map[string]string{}
	}
	if st.SessionMeta == nil {
		st.SessionMeta = map[string]storage.SessionMetaRecord{}
	}
	if st.SessionDeleted == nil {
		st.SessionDeleted = map[string]string{}
	}
	payload := SendPayload{
		OK:         true,
		Channel:    nonEmpty(sess.Channel, cfg.ChannelType),
		To:         nonEmpty(sess.Sender, "-"),
		MessageID:  msgID,
		MsgType:    mt,
		DryRun:     dryRun,
		Source:     source,
		SessionKey: key,
	}
	meta := st.SessionMeta[key]
	resolvedWorkdir := ""
	if wd, err := normalizeWorkdirPath("", workdirOverride); err != nil {
		payload.OK = false
		payload.Error = err.Error()
		fmt.Fprintf(os.Stderr, "[WARN] cli send invalid workdir msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return payload, err
	} else if strings.TrimSpace(wd) != "" {
		resolvedWorkdir = wd
	}
	if resolvedWorkdir == "" {
		resolvedWorkdir = strings.TrimSpace(meta.Workdir)
	}
	if resolvedWorkdir == "" {
		resolvedWorkdir = strings.TrimSpace(sess.Workdir)
	}
	if resolvedWorkdir == "" {
		defaultWorkdir, derr := ensureDefaultSessionWorkdir()
		if derr != nil {
			payload.OK = false
			payload.Error = derr.Error()
			fmt.Fprintf(os.Stderr, "[WARN] cli send default workdir init failed msg_id=%s session_key=%s err=%v\n", msgID, key, derr)
			return payload, derr
		}
		resolvedWorkdir = defaultWorkdir
		fmt.Fprintf(os.Stderr, "[INFO] cli send default workdir initialized msg_id=%s session_key=%s workdir=%s\n", msgID, key, resolvedWorkdir)
	}
	fmt.Fprintf(os.Stderr, "[INFO] cli send session resolved msg_id=%s session_key=%s workdir=%s sender=%s\n", msgID, key, resolvedWorkdir, nonEmpty(sess.Sender, "-"))
	stInfo, err := os.Stat(resolvedWorkdir)
	if err != nil {
		payload.OK = false
		payload.Error = fmt.Sprintf("invalid workdir: %s", resolvedWorkdir)
		fmt.Fprintf(os.Stderr, "[WARN] cli send workdir stat failed msg_id=%s session_key=%s path=%s err=%v\n", msgID, key, resolvedWorkdir, err)
		return payload, err
	}
	if !stInfo.IsDir() {
		err := fmt.Errorf("invalid workdir (not a directory): %s", resolvedWorkdir)
		payload.OK = false
		payload.Error = err.Error()
		fmt.Fprintf(os.Stderr, "[WARN] cli send workdir not dir msg_id=%s session_key=%s path=%s\n", msgID, key, resolvedWorkdir)
		return payload, err
	}
	meta.Workdir = resolvedWorkdir
	if strings.TrimSpace(meta.Status) == "" {
		meta.Status = "ready"
	}
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	st.SessionMeta[key] = meta
	if err := store.SaveState(st); err != nil {
		payload.OK = false
		payload.Error = err.Error()
		fmt.Fprintf(os.Stderr, "[WARN] cli send save state failed msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return payload, err
	}
	if dryRun {
		payload.TerminalReason = "dry_run"
		fmt.Fprintf(os.Stderr, "[INFO] cli send dry-run msg_id=%s session_key=%s\n", msgID, key)
		return payload, nil
	}

	agent := acp.NewAdapter(
		cfg.ACPAgentCmd,
		resolvedWorkdir,
		cfg.PermissionPolicy,
		cfg.TimeoutSec,
		cfg.InitializeTimeoutSec,
		cfg.SessionNewTimeoutSec,
		cfg.SessionNewRetries,
		cfg.SessionNewBackoffSec,
	)
	defer agent.Close()

	threadID := strings.TrimSpace(sess.ThreadID)
	if threadID == "-" {
		threadID = ""
	}
	senderName := strings.TrimSpace(sess.SenderName)
	if senderName == "" || senderName == "-" {
		senderName = strings.TrimSpace(sess.Sender)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	userProfile := map[string]any{
		"channel":     nonEmpty(sess.Channel, cfg.ChannelType),
		"sender":      nonEmpty(sess.Sender, "-"),
		"sender_name": nonEmpty(senderName, nonEmpty(sess.Sender, "-")),
		"thread_id":   threadID,
	}
	_ = store.AppendInteraction(map[string]any{
		"kind":         "inbound_received",
		"msg_id":       msgID,
		"session_key":  key,
		"sender":       nonEmpty(sess.Sender, "-"),
		"text":         body,
		"time":         now,
		"user_profile": userProfile,
		"message_metadata": map[string]any{
			"source": "gui",
		},
	})
	_ = store.AppendInteraction(map[string]any{
		"kind":        "trace",
		"stage":       "session_resolved",
		"msg_id":      msgID,
		"session_key": key,
		"ts":          now,
	})

	req := core.TaskRequest{
		TraceID:    traceIDForSend(msgID),
		SessionKey: key,
		UserText:   body,
		Sender:     nonEmpty(sess.Sender, "-"),
		Channel:    nonEmpty(sess.Channel, cfg.ChannelType),
		ThreadID:   threadID,
		Metadata: map[string]any{
			"received_ts": now,
			"message_id":  msgID,
			"workdir":     resolvedWorkdir,
			"source":      "gui",
			"sender_name": senderName,
		},
	}
	_ = store.AppendInteraction(map[string]any{
		"kind":     "trace",
		"stage":    "execute_start",
		"msg_id":   msgID,
		"trace_id": req.TraceID,
		"ts":       time.Now().UTC().Format(time.RFC3339),
	})
	result, execErr := agent.Execute(req)
	if execErr != nil {
		fmt.Fprintf(os.Stderr, "[WARN] cli send execute failed msg_id=%s session_key=%s err=%v\n", msgID, key, execErr)
		errText := fmt.Sprintf("执行失败: %v", execErr)
		_ = store.AppendInteraction(map[string]any{
			"msg_id":       msgID,
			"error":        errText,
			"ts":           time.Now().UTC().Format(time.RFC3339),
			"user_profile": userProfile,
		})
		payload.OK = false
		payload.Error = errText
		payload.TerminalReason = "error"
		meta.Status = "error"
		meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		st.SessionMeta[key] = meta
		_ = store.SaveState(st)
		return payload, execErr
	}

	_ = store.AppendInteraction(map[string]any{
		"kind":      "trace",
		"stage":     "execute_done",
		"msg_id":    msgID,
		"status":    result.Status,
		"elapsed_s": result.ElapsedSec,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	})
	for i, ev := range result.RawEvents {
		method := strings.TrimSpace(fmt.Sprint(ev["method"]))
		event := "-"
		if params, ok := ev["params"].(map[string]any); ok {
			if su, ok := params["sessionUpdate"].(string); ok && strings.TrimSpace(su) != "" {
				event = strings.TrimSpace(su)
			}
		}
		_ = store.AppendInteraction(map[string]any{
			"kind":   "trace",
			"stage":  "acp_event",
			"msg_id": msgID,
			"index":  i + 1,
			"method": nonEmpty(method, "-"),
			"event":  event,
			"ts":     time.Now().UTC().Format(time.RFC3339),
		})
	}

	delete(st.SessionMap, key)
	meta.Workdir = resolvedWorkdir
	meta.Status = statusForSession(result.Status, result.TerminalReason)
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	st.SessionMeta[key] = meta
	if err := store.SaveState(st); err != nil {
		payload.OK = false
		payload.Error = err.Error()
		fmt.Fprintf(os.Stderr, "[WARN] cli send final state save failed msg_id=%s session_key=%s err=%v\n", msgID, key, err)
		return payload, err
	}
	reportPath, _ := store.WriteReport(map[string]any{
		"message": core.InboundMessage{
			ID:       msgID,
			Sender:   req.Sender,
			Text:     body,
			TS:       now,
			Channel:  req.Channel,
			ThreadID: req.ThreadID,
			Metadata: req.Metadata,
		},
		"request": req,
		"result":  result,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	}, msgID)
	_ = store.AppendInteraction(map[string]any{
		"kind":   "trace",
		"stage":  "send_final_ok",
		"msg_id": msgID,
		"to":     nonEmpty(sess.Sender, "-"),
		"ts":     time.Now().UTC().Format(time.RFC3339),
	})
	_ = store.AppendInteraction(map[string]any{
		"msg_id":       msgID,
		"session_key":  key,
		"sender":       nonEmpty(sess.Sender, "-"),
		"text":         body,
		"trace_id":     req.TraceID,
		"result":       result.Summary,
		"raw_output":   result.OutputText,
		"status":       result.Status,
		"elapsed_sec":  result.ElapsedSec,
		"report_file":  reportPath,
		"ts":           time.Now().UTC().Format(time.RFC3339),
		"user_profile": userProfile,
	})

	payload.Result = strings.TrimSpace(result.Summary)
	payload.RawOutput = result.OutputText
	if resultNode, ok := parseResultJSON(payload.RawOutput); ok {
		payload.ResultJSON = resultNode
	}
	payload.TerminalReason = nonEmpty(strings.TrimSpace(result.TerminalReason), terminalReasonForStatus(result.Status))
	payload.ElapsedSec = result.ElapsedSec
	fmt.Fprintf(os.Stderr, "[INFO] cli send done msg_id=%s session_key=%s status=%s elapsed=%ds\n", msgID, key, result.Status, result.ElapsedSec)
	return payload, nil
}

func traceIDForSend(msgID string) string {
	m := strings.TrimSpace(msgID)
	if m == "" {
		m = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	if len(m) <= 8 {
		return m
	}
	return m[:8]
}

type sendWarningProvider interface {
	PopSendWarning() string
}

func popChannelSendWarning(channel core.ChannelAdapter) string {
	if channel == nil {
		return ""
	}
	provider, ok := channel.(sendWarningProvider)
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider.PopSendWarning())
}

func shortLogText(s string, n int) string {
	t := strings.TrimSpace(s)
	if n <= 0 || len(t) <= n {
		return t
	}
	return t[:n-3] + "..."
}

func runSessions(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 200, "max session rows")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if grpcDisabled() {
		cfg, err := config.Load(repoRoot, "")
		if err != nil {
			if *jsonOut {
				printJSONActionError("sessions", "config_load_failed", err.Error())
				return 1
			}
			fmt.Fprintf(os.Stderr, "sessions failed: %v\n", err)
			return 1
		}
		items, err := collectSessions(cfg)
		if err != nil {
			if *jsonOut {
				printJSONActionError("sessions", "sessions_collect_failed", err.Error())
				return 1
			}
			fmt.Fprintf(os.Stderr, "sessions failed: %v\n", err)
			return 1
		}
		if *limit > 0 && len(items) > *limit {
			items = items[:*limit]
		}
		if *jsonOut {
			printJSON(SessionsPayload{OK: true, Action: "sessions", Items: items})
			return 0
		}
		for _, it := range items {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
				it.LastTime,
				it.SessionKey,
				nonEmpty(it.SenderName, "-"),
				nonEmpty(it.Channel, "-"),
				nonEmpty(it.Workdir, "-"),
				it.LastMessage,
			)
		}
		return 0
	}
	grpc, err := trySessionsViaGRPC(repoRoot, *limit)
	if err != nil {
		if *jsonOut {
			printJSONActionError("sessions", "gateway_unreachable", formatGatewayUnavailable(err))
			return 1
		}
		fmt.Fprintf(os.Stderr, "sessions failed: %s\n", formatGatewayUnavailable(err))
		return 1
	}
	if !grpc.GetOk() {
		if *jsonOut {
			printJSONActionError("sessions", "grpc_sessions_failed", grpc.GetError())
			return 1
		}
		fmt.Fprintf(os.Stderr, "sessions failed: %s\n", grpc.GetError())
		return 1
	}
	items := make([]SessionsItem, 0, len(grpc.GetItems()))
	for _, it := range grpc.GetItems() {
		items = append(items, SessionsItem{
			SessionKey:  it.GetSessionKey(),
			SessionID:   it.GetSessionId(),
			Channel:     it.GetChannel(),
			Sender:      it.GetSender(),
			SenderName:  it.GetSenderName(),
			ThreadID:    it.GetThreadId(),
			LastMessage: it.GetLastMessage(),
			LastTime:    it.GetLastTime(),
			Workdir:     it.GetWorkdir(),
			UpdatedAt:   it.GetUpdatedAt(),
			Status:      it.GetStatus(),
			Latest:      it.GetLatest(),
		})
	}
	if *jsonOut {
		printJSON(SessionsPayload{OK: true, Action: "sessions", Items: items})
		return 0
	}
	for _, it := range items {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			it.LastTime,
			it.SessionKey,
			nonEmpty(it.SenderName, "-"),
			nonEmpty(it.Channel, "-"),
			nonEmpty(it.Workdir, "-"),
			it.LastMessage,
		)
	}
	return 0
}

func collectSessions(cfg config.AppConfig) ([]SessionsItem, error) {
	type inbound struct {
		msgID      string
		sender     string
		senderName string
		channel    string
		threadID   string
		text       string
		ts         string
	}
	inboundByMsg := map[string]inbound{}
	sessionKeyByMsg := map[string]string{}
	lastByKey := map[string]SessionsItem{}

	sessionMeta, err := loadSessionMeta(cfg)
	if err != nil {
		return nil, err
	}
	sessionDeleted, err := loadDeletedSessionMap(cfg)
	if err != nil {
		return nil, err
	}

	records, err := loadInteractionRecords(cfg)
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		kind := strings.TrimSpace(fmt.Sprint(rec["kind"]))
		switch kind {
		case "inbound_received":
			msgID := sessionctl.CleanString(rec["msg_id"])
			if msgID == "" {
				continue
			}
			profile, _ := rec["user_profile"].(map[string]any)
			channel := sessionctl.CleanString(profile["channel"])
			threadID := sessionctl.CleanString(profile["thread_id"])
			senderName := sessionctl.CleanString(profile["sender_name"])
			sender := sessionctl.CleanString(rec["sender"])
			text := sessionctl.CleanString(rec["text"])
			ts := sessionctl.CleanString(rec["time"])
			explicitKey := sessionctl.CleanString(rec["session_key"])
			in := inbound{
				msgID:      msgID,
				sender:     sender,
				senderName: senderName,
				channel:    channel,
				threadID:   threadID,
				text:       text,
				ts:         ts,
			}
			inboundByMsg[msgID] = in
			key := explicitKey
			if key == "" {
				baseKey := buildSessionKey(channel, sender, threadID)
				if isSessionDeleted(sessionDeleted, baseKey, ts) {
					continue
				}
				key = sessionKeyForInbound(baseKey, ts, sessionDeleted)
			} else if isSessionDeleted(sessionDeleted, baseSessionKeyForLifecycle(key), ts) {
				continue
			}
			sessionKeyByMsg[msgID] = key
			prev, ok := lastByKey[key]
			if !ok || ts >= prev.LastTime {
				lastByKey[key] = SessionsItem{
					SessionKey:  key,
					Channel:     nonEmpty(channel, "-"),
					Sender:      nonEmpty(sender, "-"),
					SenderName:  nonEmpty(senderName, nonEmpty(sender, "-")),
					ThreadID:    nonEmpty(threadID, "-"),
					LastMessage: nonEmpty(text, "(empty)"),
					LastTime:    ts,
				}
			}
		case "trace":
			stage := sessionctl.CleanString(rec["stage"])
			if stage != "session_resolved" {
				continue
			}
			msgID := sessionctl.CleanString(rec["msg_id"])
			key := sessionctl.CleanString(rec["session_key"])
			if msgID != "" && key != "" {
				if isSessionDeleted(sessionDeleted, key, "") {
					continue
				}
				sessionKeyByMsg[msgID] = key
			}
		}
	}
	for msgID, in := range inboundByMsg {
		key := sessionKeyByMsg[msgID]
		if key == "" {
			baseKey := buildSessionKey(in.channel, in.sender, in.threadID)
			if isSessionDeleted(sessionDeleted, baseKey, in.ts) {
				continue
			}
			key = sessionKeyForInbound(baseKey, in.ts, sessionDeleted)
		}
		baseKey := baseSessionKeyForLifecycle(key)
		if isSessionDeleted(sessionDeleted, baseKey, in.ts) {
			continue
		}
		prev, ok := lastByKey[key]
		if !ok || in.ts >= prev.LastTime {
			lastByKey[key] = SessionsItem{
				SessionKey:  key,
				Channel:     nonEmpty(in.channel, "-"),
				Sender:      nonEmpty(in.sender, "-"),
				SenderName:  nonEmpty(in.senderName, nonEmpty(in.sender, "-")),
				ThreadID:    nonEmpty(in.threadID, "-"),
				LastMessage: nonEmpty(in.text, "(empty)"),
				LastTime:    in.ts,
			}
		}
	}
	for key, meta := range sessionMeta {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if isSessionDeleted(sessionDeleted, key, strings.TrimSpace(meta.UpdatedAt)) {
			continue
		}
		if _, ok := lastByKey[key]; ok {
			continue
		}
		lastByKey[key] = SessionsItem{
			SessionKey:  key,
			Channel:     "-",
			Sender:      "-",
			SenderName:  "-",
			ThreadID:    "-",
			LastMessage: "(no messages)",
			LastTime:    strings.TrimSpace(meta.UpdatedAt),
			Workdir:     strings.TrimSpace(meta.Workdir),
			UpdatedAt:   strings.TrimSpace(meta.UpdatedAt),
			Status:      strings.TrimSpace(meta.Status),
		}
	}
	items := make([]SessionsItem, 0, len(lastByKey))
	for _, it := range lastByKey {
		meta := sessionMeta[it.SessionKey]
		if strings.TrimSpace(it.Workdir) == "" {
			it.Workdir = strings.TrimSpace(meta.Workdir)
		}
		if strings.TrimSpace(it.UpdatedAt) == "" {
			it.UpdatedAt = strings.TrimSpace(meta.UpdatedAt)
		}
		if strings.TrimSpace(it.Status) == "" {
			it.Status = strings.TrimSpace(meta.Status)
		}
		if strings.TrimSpace(it.UpdatedAt) == "" {
			it.UpdatedAt = strings.TrimSpace(it.LastTime)
		}
		if strings.TrimSpace(it.Status) == "" {
			it.Status = "discovered"
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].LastTime > items[j].LastTime
	})
	if len(items) > 0 {
		items[0].Latest = true
	}
	return items, nil
}

func loadInteractionRecords(cfg config.AppConfig) ([]map[string]any, error) {
	if strings.EqualFold(strings.TrimSpace(cfg.StorageBackend), "sqlite") {
		return loadInteractionRecordsFromSQLite(cfg.StorageSQLitePath)
	}
	return loadInteractionRecordsFromFile(cfg.InteractionLogFile)
}

func loadInteractionRecordsFromFile(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	defer f.Close()

	out := make([]map[string]any, 0, 512)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadInteractionRecordsFromSQLite(dbPath string) ([]map[string]any, error) {
	if strings.TrimSpace(dbPath) == "" {
		return []map[string]any{}, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT payload_json FROM interactions ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 1024)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadSessionMap(cfg config.AppConfig) (map[string]string, error) {
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return nil, err
	}
	st, err := store.LoadState()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range st.SessionMap {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func loadSessionMeta(cfg config.AppConfig) (map[string]storage.SessionMetaRecord, error) {
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return nil, err
	}
	st, err := store.LoadState()
	if err != nil {
		return nil, err
	}
	out := map[string]storage.SessionMetaRecord{}
	for k, v := range st.SessionMeta {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = storage.SessionMetaRecord{
			Workdir:   strings.TrimSpace(v.Workdir),
			UpdatedAt: strings.TrimSpace(v.UpdatedAt),
			Status:    strings.TrimSpace(v.Status),
		}
	}
	return out, nil
}

func loadDeletedSessionMap(cfg config.AppConfig) (map[string]string, error) {
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return nil, err
	}
	st, err := store.LoadState()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, deletedAt := range st.SessionDeleted {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(deletedAt)
	}
	return out, nil
}

func isSessionDeleted(deleted map[string]string, key, activityTS string) bool {
	deletedAt, ok := deleted[strings.TrimSpace(key)]
	if !ok {
		return false
	}
	deletedAt = strings.TrimSpace(deletedAt)
	activityTS = strings.TrimSpace(activityTS)
	if deletedAt == "" || activityTS == "" {
		return true
	}
	return !isAfterTimestamp(activityTS, deletedAt)
}

func isAfterTimestamp(currentTS, cutoffTS string) bool {
	currentTS = strings.TrimSpace(currentTS)
	cutoffTS = strings.TrimSpace(cutoffTS)
	if currentTS == "" || cutoffTS == "" {
		return false
	}
	currentTime, currentOK := parseTimestamp(currentTS)
	cutoffTime, cutoffOK := parseTimestamp(cutoffTS)
	if currentOK && cutoffOK {
		return currentTime.After(cutoffTime)
	}
	return currentTS > cutoffTS
}

func parseTimestamp(raw string) (time.Time, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, v); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func normalizeWorkdirPath(repoRoot, workdir string) (string, error) {
	raw := strings.TrimSpace(workdir)
	if raw == "" {
		return "", nil
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(repoRoot, abs)
	}
	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("invalid workdir path: %w", err)
	}
	return resolved, nil
}

func ensureDefaultSessionWorkdir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir failed: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("resolve home dir failed: empty")
	}
	path := filepath.Join(home, ".cag", "workspace", "default")
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create default workdir failed: %w", err)
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve default workdir failed: %w", err)
	}
	return resolved, nil
}

func parseResultJSON(raw string) (any, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var node any
	if err := json.Unmarshal([]byte(trimmed), &node); err != nil {
		return nil, false
	}
	return node, true
}

func terminalReasonForStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok":
		return "completed"
	case "timeout":
		return "timeout"
	case "cancelled":
		return "cancelled"
	case "error":
		return "error"
	default:
		return ""
	}
}

func statusForSession(status, terminalReason string) string {
	t := strings.ToLower(strings.TrimSpace(terminalReason))
	if t != "" {
		switch t {
		case "completed", "idle_after_chunk":
			return "ready"
		case "timeout":
			return "timeout"
		case "error":
			return "error"
		case "cancelled":
			return "cancelled"
		default:
			return t
		}
	}
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "ok":
		return "ready"
	case "timeout":
		return "timeout"
	case "error":
		return "error"
	case "cancelled":
		return "cancelled"
	default:
		if s == "" {
			return "ready"
		}
		return s
	}
}

func buildSessionKey(channel, sender, threadID string) string {
	raw := channel + "|" + sender + "|" + nonEmpty(threadID, "-")
	sum := sha1.Sum([]byte(raw))
	return "sess_" + hex.EncodeToString(sum[:])[:24]
}

func baseSessionKeyForLifecycle(key string) string {
	k := strings.TrimSpace(key)
	if !strings.HasPrefix(k, "sess_") {
		return k
	}
	if idx := strings.Index(k, "_r"); idx > 5 {
		return k[:idx]
	}
	return k
}

func deriveReopenedSessionKey(baseKey, deletedAt string) string {
	base := strings.TrimSpace(baseKey)
	if base == "" {
		return base
	}
	cutoff := strings.TrimSpace(deletedAt)
	if cutoff == "" {
		return base
	}
	sum := sha1.Sum([]byte(base + "|" + cutoff))
	return base + "_r" + hex.EncodeToString(sum[:])[:8]
}

func sessionKeyForInbound(baseKey, activityTS string, deleted map[string]string) string {
	base := strings.TrimSpace(baseKey)
	if base == "" {
		return base
	}
	deletedAt := strings.TrimSpace(deleted[base])
	if deletedAt == "" {
		return base
	}
	if !isAfterTimestamp(activityTS, deletedAt) {
		return base
	}
	return deriveReopenedSessionKey(base, deletedAt)
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func buildChannelAdapter(cfg config.AppConfig) core.ChannelAdapter {
	switch strings.ToLower(strings.TrimSpace(cfg.ChannelType)) {
	case "dingtalk":
		return dingtalk.NewAdapter(dingtalk.Options{
			FetchMaxEvents:        cfg.DingTalkFetchMax,
			DMPolicy:              cfg.DingTalkDMPolicy,
			GroupPolicy:           cfg.DingTalkGroupPolicy,
			AllowedFrom:           cfg.DingTalkAllowedFrom,
			GroupAllowlist:        cfg.DingTalkGroupAllowed,
			RequireMentionInGroup: cfg.DingTalkRequireAt,
			SendMode:              cfg.DingTalkSendMode,
			SendMsgType:           cfg.DingTalkSendMsgType,
			SendTimeoutSec:        cfg.DingTalkSendTimeout,
			MarkdownTitle:         cfg.DingTalkTitle,
			PrettyStatus:          cfg.DingTalkPrettyStatus,
			BotWebhook:            cfg.DingTalkBotWebhook,
			BotSecret:             cfg.DingTalkBotSecret,
			AppKey:                cfg.DingTalkAppKey,
			AppSecret:             cfg.DingTalkAppSecret,
			AgentID:               cfg.DingTalkAgentID,
			DefaultToUser:         cfg.DingTalkDefaultTo,
			TokenURL:              cfg.DingTalkTokenURL,
			SendURL:               cfg.DingTalkSendURL,
		})
	case "imessage":
		return imessage.NewAdapter(imessage.Options{
			FetchCmd:        cfg.IMessageFetchCmd,
			SendCmd:         cfg.IMessageSendCmd,
			FetchTimeoutSec: cfg.IMessageFetchTimeoutSec,
			SendTimeoutSec:  cfg.IMessageSendTimeoutSec,
		})
	default:
		return &command.Adapter{
			FetchCmd:        cfg.FetchCmd,
			SendCmd:         cfg.SendCmd,
			ChannelID:       cfg.ChannelType,
			FetchTimeoutSec: 120,
			SendTimeoutSec:  120,
		}
	}
}

func sendStartupGreeting(cfg config.AppConfig, channel core.ChannelAdapter) error {
	target := startupGreetingTarget(cfg)
	if strings.TrimSpace(target) == "" && !startupGreetingCanUseEmptyTarget(cfg) {
		return nil
	}
	return channel.Send(startupGreetingText(cfg), target, "startup-greeting", "")
}

func startupGreetingTarget(cfg config.AppConfig) string {
	switch strings.ToLower(strings.TrimSpace(cfg.ChannelType)) {
	case "dingtalk":
		if strings.EqualFold(strings.TrimSpace(cfg.DingTalkSendMode), "webhook") {
			return ""
		}
		if strings.TrimSpace(cfg.DingTalkDefaultTo) != "" {
			return strings.TrimSpace(cfg.DingTalkDefaultTo)
		}
	}
	return strings.TrimSpace(cfg.RemoteUserID)
}

func startupGreetingCanUseEmptyTarget(cfg config.AppConfig) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.ChannelType), "dingtalk") &&
		strings.EqualFold(strings.TrimSpace(cfg.DingTalkSendMode), "webhook")
}

func startupGreetingText(cfg config.AppConfig) string {
	channel := nonEmpty(strings.TrimSpace(cfg.ChannelType), "command")
	return fmt.Sprintf("你好，我是 CLI Agent Gateway。\n服务已经启动，当前通道：%s。\n我负责接收消息、调用 ACP 代理执行任务，并返回结果与进度。", channel)
}

func getStatusPayload(repoRoot string) (StatusPayload, error) {
	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		return StatusPayload{}, err
	}
	lockPath := strings.TrimSpace(cfg.LockFile)
	if lockPath == "" {
		lockPath = config.DefaultRuntimePaths(repoRoot).LockFile
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return StatusPayload{}, err
	}
	st, err := lockfile.Inspect(lockPath)
	if err != nil {
		return StatusPayload{}, err
	}
	metadata := st.Metadata
	if !st.Locked {
		metadata = nil
	}
	payload := StatusPayload{Running: st.Locked, LockFile: lockPath, Metadata: metadata}
	if st.OwnerPID != nil {
		pid := *st.OwnerPID
		payload.PID = &pid
	}
	if st.OwnerStartedAt != nil {
		payload.StartedAt = *st.OwnerStartedAt
	}
	return payload, nil
}

func waitForRunningStatus(repoRoot string, timeout time.Duration) (StatusPayload, error) {
	deadline := time.Now().Add(timeout)
	var last StatusPayload
	var lastErr error
	for time.Now().Before(deadline) {
		payload, err := getStatusPayload(repoRoot)
		if err == nil {
			last = payload
			if payload.Running {
				return payload, nil
			}
			lastErr = fmt.Errorf("start requested but process is not running yet")
		} else {
			lastErr = err
		}
		time.Sleep(120 * time.Millisecond)
	}
	if last.Running {
		return last, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("start requested but process is not running yet")
	}
	return StatusPayload{}, lastErr
}

func loadEnvDefaults(repoRoot string) {
	_ = repoRoot
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return
	}
	_ = envfile.LoadDotEnvSetDefault(filepath.Join(home, ".cag", ".env"))
}

func statusJSON(action string, p StatusPayload, cfg config.AppConfig, logPath string) map[string]any {
	status := "stopped"
	if p.Running {
		status = "running"
	}
	out := map[string]any{
		"ok":           true,
		"action":       action,
		"status":       status,
		"running":      p.Running,
		"lock_file":    strings.TrimSpace(p.LockFile),
		"gateway_addr": gatewaydAddr(),
	}
	if p.PID != nil && *p.PID > 0 {
		out["pid"] = *p.PID
	}
	if strings.TrimSpace(p.StartedAt) != "" {
		out["started_at"] = p.StartedAt
	}
	if channel := statusMetadataString(p, "channel"); channel != "" {
		out["channel"] = channel
	}
	if workdir := statusMetadataString(p, "workdir"); workdir != "" {
		out["workdir"] = workdir
	}
	if strings.TrimSpace(cfg.StateFile) != "" {
		out["state_file"] = cfg.StateFile
	}
	if strings.TrimSpace(cfg.InteractionLogFile) != "" {
		out["interaction_log_file"] = cfg.InteractionLogFile
	}
	if strings.TrimSpace(logPath) != "" {
		out["log_file"] = logPath
	} else if v, ok := p.Metadata["log_file"].(string); ok && strings.TrimSpace(v) != "" {
		out["log_file"] = strings.TrimSpace(v)
	}
	if len(p.Metadata) > 0 {
		out["metadata"] = p.Metadata
	}
	return out
}

func statusMetadataString(p StatusPayload, key string) string {
	if len(p.Metadata) == 0 {
		return ""
	}
	v, _ := p.Metadata[key].(string)
	return strings.TrimSpace(v)
}

func effectiveStatusChannel(p StatusPayload, cfg config.AppConfig) string {
	channel := statusMetadataString(p, "channel")
	if channel != "" {
		return channel
	}
	return strings.TrimSpace(cfg.ChannelType)
}

func effectiveStatusWorkdir(p StatusPayload, cfg config.AppConfig) string {
	workdir := statusMetadataString(p, "workdir")
	if workdir != "" {
		return workdir
	}
	return strings.TrimSpace(cfg.Workdir)
}

func effectiveStatusLockFile(p StatusPayload, cfg config.AppConfig) string {
	_ = cfg
	return strings.TrimSpace(p.LockFile)
}

func renderPlainStatus(repoRoot string, p StatusPayload, cfg config.AppConfig, logPath string) string {
	var b strings.Builder
	if p.Running {
		pid := "unknown"
		if p.PID != nil {
			pid = fmt.Sprintf("%d", *p.PID)
		}
		started := p.StartedAt
		if strings.TrimSpace(started) == "" {
			started = "unknown"
		}
		fmt.Fprintf(&b, "RUNNING pid=%s started_at=%s lock=%s\n", pid, started, p.LockFile)
	} else {
		fmt.Fprintf(&b, "NOT_RUNNING lock=%s\n", p.LockFile)
	}

	effectiveLogPath := statusLogPath(repoRoot, p, logPath)
	if strings.TrimSpace(effectiveLogPath) != "" {
		fmt.Fprintf(&b, "log=%s\n", effectiveLogPath)
	}

	if p.Running {
		lines, err := readTailLines(effectiveLogPath, 8)
		if err != nil {
			fmt.Fprintf(&b, "recent_log=unavailable err=%v\n", err)
			return b.String()
		}
		trimmed := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				trimmed = append(trimmed, line)
			}
		}
		if len(trimmed) == 0 {
			b.WriteString("recent_log=empty\n")
			return b.String()
		}
		b.WriteString("recent_log:\n")
		for _, line := range trimmed {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	return b.String()
}

func statusLogPath(repoRoot string, p StatusPayload, explicitLogPath string) string {
	if v := strings.TrimSpace(explicitLogPath); v != "" {
		return v
	}
	if v, ok := p.Metadata["log_file"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return resolveLogPath(repoRoot, nil)
}

func printJSONActionError(action, code, message string) {
	printJSON(map[string]any{
		"ok":     false,
		"action": action,
		"status": "failed",
		"error": JSONError{
			Code:    code,
			Message: message,
		},
	})
}

func buildHealthPayload(repoRoot, action string, includePaths bool) HealthPayload {
	p := HealthPayload{
		OK:     false,
		Action: action,
		Status: "unhealthy",
		Items:  []HealthItem{},
	}
	add := func(key string, ok bool, detail, suggestion string) {
		p.Items = append(p.Items, HealthItem{
			Key:        key,
			OK:         ok,
			Detail:     detail,
			Suggestion: suggestion,
		})
	}

	envPath := filepath.Join(config.CAGHomeDir(), ".env")
	if _, err := os.Stat(envPath); err != nil {
		add("env", false, "~/.cag/.env missing", "run `cag config` first")
		return p
	}
	add("env", true, "~/.cag/.env loaded", "")

	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		add("config", false, err.Error(), "fix ~/.cag/.env values and re-run")
		return p
	}
	p.Channel = cfg.ChannelType

	if _, err := os.Stat(cfg.Workdir); err != nil {
		add("workdir", false, fmt.Sprintf("workdir not accessible: %s", cfg.Workdir), "use a valid repo root or set workdir per session")
	} else {
		add("workdir", true, "workdir ready", "")
	}

	acpCmd := strings.TrimSpace(cfg.ACPAgentCmd)
	acpBin := acpCmd
	if fields := strings.Fields(acpCmd); len(fields) > 0 {
		acpBin = fields[0]
	}
	if _, err := exec.LookPath(acpBin); err != nil {
		add("acp", false, fmt.Sprintf("acp command not found: %s", acpBin), "install codex and ensure ACP_AGENT_CMD is in PATH")
	} else {
		add("acp", true, fmt.Sprintf("acp command ready: %s", acpBin), "")
	}

	switch strings.ToLower(strings.TrimSpace(cfg.ChannelType)) {
	case "imessage":
		if _, err := exec.LookPath("imsg"); err != nil {
			add("imessage.binary", false, "imsg not found in PATH", "install imsg and relaunch terminal/app")
		} else {
			add("imessage.binary", true, "imsg ready", "")
		}
		if strings.TrimSpace(cfg.IMessageFetchCmd) == "" {
			add("imessage.fetch_cmd", false, "IMESSAGE_FETCH_CMD is empty", "set IMESSAGE_FETCH_CMD in ~/.cag/.env")
		} else {
			add("imessage.fetch_cmd", true, "IMESSAGE_FETCH_CMD configured", "")
		}
		if strings.TrimSpace(cfg.IMessageSendCmd) == "" {
			add("imessage.send_cmd", false, "IMESSAGE_SEND_CMD is empty", "set IMESSAGE_SEND_CMD in ~/.cag/.env")
		} else {
			add("imessage.send_cmd", true, "IMESSAGE_SEND_CMD configured", "")
		}
	case "dingtalk":
		streamKey := strings.TrimSpace(cfg.DingTalkAppKey) != ""
		streamSecret := strings.TrimSpace(cfg.DingTalkAppSecret) != ""
		add("dingtalk.stream_app_key", streamKey, "stream ingress requires DINGTALK_APP_KEY", "set DINGTALK_APP_KEY")
		add("dingtalk.stream_app_secret", streamSecret, "stream ingress requires DINGTALK_APP_SECRET", "set DINGTALK_APP_SECRET")
		dmPolicy := strings.ToLower(strings.TrimSpace(cfg.DingTalkDMPolicy))
		if dmPolicy == "" {
			dmPolicy = "allowlist"
		}
		switch dmPolicy {
		case "allowlist":
			envAllowlist := len(cfg.DingTalkAllowedFrom) > 0
			dbApproved := hasAllowedUserAccess(repoRoot, "dingtalk")
			switch {
			case envAllowlist:
				add("dingtalk.allowed_from", true, "direct-message allowlist configured in env", "")
			case dbApproved:
				add("dingtalk.allowed_from", true, "approved DingTalk users exist in gateway state", "")
			default:
				add("dingtalk.allowed_from", false, "direct-message allowlist is empty and no approved DingTalk users are stored yet", "set DINGTALK_ALLOWED_FROM, approve a user in GUI Access Requests, or use DINGTALK_DM_POLICY=allow_all")
			}
		case "allow_all":
			add("dingtalk.allowed_from", true, "direct-message policy allows all senders", "")
		default:
			add("dingtalk.dm_policy", false, fmt.Sprintf("invalid DINGTALK_DM_POLICY: %s", cfg.DingTalkDMPolicy), "use DINGTALK_DM_POLICY=allowlist or allow_all")
		}

		sendMode := strings.ToLower(strings.TrimSpace(cfg.DingTalkSendMode))
		switch sendMode {
		case "", "api":
			a := strings.TrimSpace(cfg.DingTalkAgentID) != ""
			add("dingtalk.send_agent_id", a, "api send requires DINGTALK_AGENT_ID", "set DINGTALK_AGENT_ID or switch DINGTALK_SEND_MODE=webhook")
		case "webhook":
			ok := strings.TrimSpace(cfg.DingTalkBotWebhook) != ""
			add("dingtalk.send_webhook", ok, "webhook send requires DINGTALK_BOT_WEBHOOK", "set DINGTALK_BOT_WEBHOOK")
		default:
			add("dingtalk.send_mode", false, fmt.Sprintf("invalid DINGTALK_SEND_MODE: %s", cfg.DingTalkSendMode), "use DINGTALK_SEND_MODE=api or webhook")
		}
		if warning := detectRecentDingTalkRuntimeWarning(repoRoot, cfg, 20*time.Minute); warning != "" {
			add("dingtalk.runtime_send", false, warning, "add current egress IP to DingTalk app whitelist, or use DINGTALK_SEND_MODE=webhook")
		}
	}

	if includePaths {
		addWritableCheck(&p.Items, "paths.lock_file", filepath.Dir(cfg.LockFile))
		addWritableCheck(&p.Items, "paths.state_file", filepath.Dir(cfg.StateFile))
		addWritableCheck(&p.Items, "paths.interaction_log", filepath.Dir(cfg.InteractionLogFile))
		addWritableCheck(&p.Items, "paths.report_dir", cfg.ReportDir)
		if strings.EqualFold(cfg.StorageBackend, "sqlite") {
			addWritableCheck(&p.Items, "paths.sqlite", filepath.Dir(cfg.StorageSQLitePath))
		}
	}

	p.OK = true
	for _, it := range p.Items {
		if !it.OK {
			p.OK = false
			break
		}
	}
	if p.OK {
		p.Status = "healthy"
	}
	return p
}

func addWritableCheck(items *[]HealthItem, key, dir string) {
	d := strings.TrimSpace(dir)
	if d == "" {
		*items = append(*items, HealthItem{
			Key:        key,
			OK:         false,
			Detail:     "empty path",
			Suggestion: "set path in .env",
		})
		return
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		*items = append(*items, HealthItem{
			Key:        key,
			OK:         false,
			Detail:     fmt.Sprintf("mkdir failed: %v", err),
			Suggestion: "check directory permissions",
		})
		return
	}
	f, err := os.CreateTemp(d, ".cag-write-check-*")
	if err != nil {
		*items = append(*items, HealthItem{
			Key:        key,
			OK:         false,
			Detail:     fmt.Sprintf("not writable: %v", err),
			Suggestion: "check directory permissions",
		})
		return
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	*items = append(*items, HealthItem{Key: key, OK: true, Detail: "writable: " + d})
}

func detectRecentDingTalkRuntimeWarning(repoRoot string, cfg config.AppConfig, window time.Duration) string {
	if !strings.EqualFold(strings.TrimSpace(cfg.ChannelType), "dingtalk") {
		return ""
	}
	if window <= 0 {
		window = 20 * time.Minute
	}
	lockStatus, err := lockfile.Inspect(cfg.LockFile)
	if err != nil || !lockStatus.Locked {
		return ""
	}
	for _, path := range dingtalkLogCandidates(repoRoot, cfg, lockStatus) {
		lines, err := readTailLines(path, 600)
		if err != nil || len(lines) == 0 {
			continue
		}
		if warning := detectRecentDingTalkRuntimeWarningFromLines(lines, time.Now().UTC(), window); warning != "" {
			return warning
		}
	}
	return ""
}

func detectRecentDingTalkRuntimeWarningFromLines(lines []string, now time.Time, window time.Duration) string {
	if len(lines) == 0 {
		return ""
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !isDingTalkRuntimeErrorLine(line) {
			continue
		}
		if ts, ok := parseBracketTimestamp(line); ok && now.Sub(ts) > window {
			continue
		}
		return shortLogText(line, 220)
	}
	return ""
}

func isDingTalkRuntimeErrorLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "dingtalk-stream api send failed") && strings.Contains(lower, "fallback=session_webhook") {
		return true
	}
	if strings.Contains(lower, "send startup greeting failed") &&
		(strings.Contains(lower, "errcode=88") || strings.Contains(lower, "subcode=60020")) {
		return true
	}
	return false
}

func parseBracketTimestamp(line string) (time.Time, bool) {
	raw := strings.TrimSpace(line)
	if !strings.HasPrefix(raw, "[") {
		return time.Time{}, false
	}
	end := strings.Index(raw, "]")
	if end <= 1 {
		return time.Time{}, false
	}
	tsRaw := strings.TrimSpace(raw[1:end])
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, tsRaw); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func dingtalkLogCandidates(repoRoot string, cfg config.AppConfig, lockStatus lockfile.Status) []string {
	addPath := func(out *[]string, seen map[string]struct{}, path string) {
		p := strings.TrimSpace(path)
		if p == "" {
			return
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, p)
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			p = abs
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		*out = append(*out, p)
	}

	out := make([]string, 0, 6)
	seen := map[string]struct{}{}
	if lockStatus.Metadata != nil {
		if v, ok := lockStatus.Metadata["log_file"].(string); ok {
			addPath(&out, seen, v)
		}
	}
	base := resolveLogPath(repoRoot, nil)
	addPath(&out, seen, base)
	if latest := latestRotatedLogPath(base); latest != "" {
		addPath(&out, seen, latest)
	}
	return out
}

func latestRotatedLogPath(base string) string {
	b := strings.TrimSpace(base)
	if b == "" {
		return ""
	}
	ext := filepath.Ext(b)
	stem := strings.TrimSuffix(filepath.Base(b), ext)
	if strings.TrimSpace(stem) == "" {
		return ""
	}
	glob := filepath.Join(filepath.Dir(b), stem+"_*"+ext)
	matches, err := filepath.Glob(glob)
	if err != nil || len(matches) == 0 {
		return ""
	}
	latest := ""
	var latestMod time.Time
	for _, candidate := range matches {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if latest == "" || info.ModTime().After(latestMod) {
			latest = candidate
			latestMod = info.ModTime()
		}
	}
	return latest
}

func readTailLines(path string, maxLines int) ([]string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil, fmt.Errorf("empty path")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if maxLines <= 0 {
		maxLines = 200
	}
	lines := make([]string, 0, maxLines)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if len(lines) < maxLines {
			lines = append(lines, line)
			continue
		}
		copy(lines, lines[1:])
		lines[maxLines-1] = line
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func resolveWorkdir(repoRoot string, args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(strings.TrimSpace(arg), "-") {
			continue
		}
		if strings.TrimSpace(arg) != "" {
			return arg
		}
	}
	if envWorkdir := strings.TrimSpace(os.Getenv("WORKDIR")); envWorkdir != "" {
		return envWorkdir
	}
	return repoRoot
}
func detectRepoRoot(cwd string) string {
	current := filepath.Clean(cwd)
	if filepath.Base(current) == "src" {
		parent := filepath.Clean(filepath.Join(current, ".."))
		if _, err := os.Stat(filepath.Join(parent, "Makefile")); err == nil {
			return parent
		}
	}
	if _, err := os.Stat(filepath.Join(current, ".env")); err == nil {
		return current
	}
	parent := filepath.Clean(filepath.Join(current, ".."))
	if _, err := os.Stat(filepath.Join(parent, ".env")); err == nil {
		return parent
	}
	return current
}

func expandUser(path string) string {
	if path == "~" {
		h, err := os.UserHomeDir()
		if err == nil {
			return h
		}
	}
	if strings.HasPrefix(path, "~/") {
		h, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(h, path[2:])
		}
	}
	return path
}

func resolveAgainstRepo(repoRoot, path string) (string, error) {
	p := expandUser(strings.TrimSpace(path))
	if !filepath.IsAbs(p) {
		p = filepath.Join(repoRoot, p)
	}
	return filepath.Abs(p)
}

func hasFlag(args []string, want string) bool {
	for _, arg := range args {
		if strings.TrimSpace(arg) == want {
			return true
		}
	}
	return false
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func resolveLogPath(repoRoot string, args []string) string {
	_ = repoRoot
	baseDir := filepath.Join(config.CAGHomeDir(), "gatewayd")
	if fv := strings.TrimSpace(flagValue(args, "--log-file")); fv != "" {
		if !filepath.IsAbs(fv) {
			fv = filepath.Join(baseDir, fv)
		}
		if abs, err := filepath.Abs(fv); err == nil {
			return abs
		}
		return fv
	}
	loadEnvDefaults(repoRoot)
	v := strings.TrimSpace(os.Getenv("GATEWAY_LOG_FILE"))
	if v == "" {
		v = filepath.Join(baseDir, "gatewayd.log")
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(baseDir, v)
	}
	if abs, err := filepath.Abs(v); err == nil {
		return abs
	}
	return v
}

func resolveFreshLogPath(repoRoot string, args []string) string {
	return resolveLogPath(repoRoot, args)
}

func runtimeLogOpenFlags(args []string) int {
	if strings.TrimSpace(flagValue(args, "--log-file")) != "" {
		return os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	return os.O_CREATE | os.O_WRONLY | os.O_APPEND
}

func rotatedLogPath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	ts := time.Now().UTC().Format("20060102_150405")
	if ext == "" {
		return base + "_" + ts
	}
	return base + "_" + ts + ext
}

func flagValue(args []string, key string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == key && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		prefix := key + "="
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
		}
	}
	return ""
}
