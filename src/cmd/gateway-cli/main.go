package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	OK        bool   `json:"ok"`
	Channel   string `json:"channel"`
	To        string `json:"to"`
	MessageID string `json:"message_id"`
	Error     string `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	cmd := strings.ToLower(strings.TrimSpace(os.Args[1]))
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve cwd: %v\n", err)
		os.Exit(1)
	}
	repoRoot := detectRepoRoot(cwd)
	args := os.Args[2:]

	switch cmd {
	case "run":
		os.Exit(runGoMain(repoRoot, args))
	case "start":
		os.Exit(runStart(repoRoot, args))
	case "stop":
		os.Exit(runStop(repoRoot, args))
	case "restart":
		os.Exit(runRestart(repoRoot, args))
	case "config":
		os.Exit(runGoConfig(repoRoot, args))
	case "status":
		os.Exit(runStatus(repoRoot, args))
	case "health":
		os.Exit(runHealth(repoRoot, args))
	case "doctor":
		os.Exit(runDoctor(repoRoot, args))
	case "send":
		os.Exit(runSend(repoRoot, args))
	case "actions":
		printActions(os.Stdout)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s\n", cmd)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printActions(out *os.File) {
	fmt.Fprintln(out, "run")
	fmt.Fprintln(out, "start")
	fmt.Fprintln(out, "stop")
	fmt.Fprintln(out, "restart")
	fmt.Fprintln(out, "config")
	fmt.Fprintln(out, "status")
	fmt.Fprintln(out, "health")
	fmt.Fprintln(out, "doctor")
	fmt.Fprintln(out, "send")
	fmt.Fprintln(out, "actions")
	fmt.Fprintln(out, "help")
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage: cag <action> [options]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Actions:")
	fmt.Fprintln(out, "  run                 Start gateway runtime in foreground")
	fmt.Fprintln(out, "  start               Start gateway runtime in background (dashboard-friendly)")
	fmt.Fprintln(out, "  stop                Stop running gateway process by lock owner pid")
	fmt.Fprintln(out, "  restart             Stop then start")
	fmt.Fprintln(out, "  start --log-file    Optional server log path for background runtime")
	fmt.Fprintln(out, "  config [workdir]    Generate/update .env using Go-native defaults")
	fmt.Fprintln(out, "  status [--json]     Check single-instance lock status")
	fmt.Fprintln(out, "  health [--json]     Validate runtime prerequisites for selected channel")
	fmt.Fprintln(out, "  doctor [--json]     Extended diagnostics (health + path writability)")
	fmt.Fprintln(out, "  send [opts]         Send a message via selected channel")
	fmt.Fprintln(out, "  actions             Print supported action names")
	fmt.Fprintln(out, "  help                Show this message")
}

func runGoMain(repoRoot string, args []string) int {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		fmt.Fprintln(os.Stderr, "run does not accept workdir arg; configure CODEX_WORKDIR in .env (use `cag config`)")
		return 2
	}
	envPath := filepath.Join(repoRoot, ".env")
	if _, err := os.Stat(envPath); err != nil {
		panic(fmt.Sprintf(".env not found: %s", envPath))
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
	_ = lock.WriteMetadata(map[string]any{
		"workdir":    cfg.Workdir,
		"lock_file":  cfg.LockFile,
		"channel":    cfg.ChannelType,
		"started_at": time.Now().UTC().Format(time.RFC3339),
	})

	fmt.Printf("[%s] startup channel=%s workdir=%s\n", time.Now().UTC().Format(time.RFC3339), cfg.ChannelType, cfg.Workdir)
	fmt.Printf("[%s] startup acp_cmd=%s permission_policy=%s\n", time.Now().UTC().Format(time.RFC3339), cfg.ACPAgentCmd, cfg.PermissionPolicy)

	channel := buildChannelAdapter(cfg)
	agent := acp.NewAdapter(
		cfg.ACPAgentCmd,
		cfg.Workdir,
		cfg.PermissionPolicy,
		cfg.TimeoutSec,
		cfg.InitializeTimeoutSec,
		cfg.SessionNewTimeoutSec,
		cfg.SessionNewRetries,
		cfg.SessionNewBackoffSec,
	)
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
		Channel:           channel,
		Agent:             agent,
		Storage:           store,
		RemoteUserID:      cfg.RemoteUserID,
		AllowedFrom:       cfg.AllowedFrom,
		ProcessOnlyLatest: cfg.ProcessOnlyLatest,
		PollIntervalSec:   cfg.PollIntervalSec,
		ReplyStyleEnabled: cfg.ReplyStyleEnabled,
		ReplyStylePrompt:  cfg.ReplyStylePrompt,
	}
	if err := loop.RunForever(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway loop failed: %v\n", err)
		return 1
	}
	return 0
}

func runGoConfig(repoRoot string, args []string) int {
	workdir := resolveWorkdir(repoRoot, args)
	path, err := config.WriteDefaultEnv(repoRoot, workdir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write .env failed: %v\n", err)
		return 1
	}
	fmt.Printf("configured env file: %s\n", path)
	return 0
}

func runStatus(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
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
	if payload.Running {
		pid := "unknown"
		if payload.PID != nil {
			pid = fmt.Sprintf("%d", *payload.PID)
		}
		started := payload.StartedAt
		if strings.TrimSpace(started) == "" {
			started = "unknown"
		}
		fmt.Printf("RUNNING pid=%s started_at=%s lock=%s\n", pid, started, payload.LockFile)
		return 0
	}
	fmt.Printf("NOT_RUNNING lock=%s\n", payload.LockFile)
	return 0
}

func runStart(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	envPath := filepath.Join(repoRoot, ".env")
	if _, err := os.Stat(envPath); err != nil {
		if jsonOut {
			printJSONActionError("start", "env_missing", ".env not found")
			return 1
		}
		fmt.Fprintf(os.Stderr, ".env not found: %s\n", envPath)
		return 1
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

	logPath := resolveLogPath(repoRoot, args)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		if jsonOut {
			printJSONActionError("start", "log_prepare_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "create log dir failed: %v\n", err)
		return 1
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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

	time.Sleep(800 * time.Millisecond)
	payload, err := getStatusPayload(repoRoot)
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
	cfg, _ := config.Load(repoRoot, "")
	quiet := hasFlag(args, "--quiet")
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

	logPath := resolveLogPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		printJSONActionError("restart", "log_prepare_failed", err.Error())
		return 1
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
	configureDetachedProcess(p)
	if err := p.Start(); err != nil {
		printJSONActionError("restart", "start_process_failed", err.Error())
		return 1
	}
	_ = p.Process.Release()
	time.Sleep(800 * time.Millisecond)

	after, err := getStatusPayload(repoRoot)
	if err != nil {
		printJSONActionError("restart", "status_after_restart_failed", err.Error())
		return 1
	}
	if !after.Running {
		printJSONActionError("restart", "restart_not_running", "restart requested but process is not running yet")
		return 1
	}
	printJSON(statusJSON("restart", after, cfg, logPath))
	return 0
}

func runHealth(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
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
	envPath := filepath.Join(repoRoot, ".env")
	if _, err := os.Stat(envPath); err != nil {
		panic(fmt.Sprintf(".env not found: %s", envPath))
	}

	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	text := fs.String("text", "", "message text")
	to := fs.String("to", "", "target receiver/user")
	channelOverride := fs.String("channel", "", "channel override: command|dingtalk|imessage")
	messageID := fs.String("message-id", "", "message id")
	reportFile := fs.String("report-file", "", "report file path")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(*text) == "" {
		fmt.Fprintln(os.Stderr, "send requires --text")
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
	if target == "" {
		fmt.Fprintln(os.Stderr, "send requires --to (or DINGTALK_DEFAULT_TO_USER for dingtalk)")
		return 2
	}

	msgID := strings.TrimSpace(*messageID)
	if msgID == "" {
		msgID = fmt.Sprintf("manual-%d", time.Now().UnixMilli())
	}

	sendErr := channel.Send(strings.TrimSpace(*text), target, msgID, strings.TrimSpace(*reportFile))
	if *jsonOut {
		payload := SendPayload{
			OK:        sendErr == nil,
			Channel:   cfg.ChannelType,
			To:        target,
			MessageID: msgID,
		}
		if sendErr != nil {
			payload.Error = sendErr.Error()
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
	fmt.Printf("sent channel=%s to=%s message_id=%s\n", cfg.ChannelType, target, msgID)
	return 0
}

func buildChannelAdapter(cfg config.AppConfig) core.ChannelAdapter {
	switch strings.ToLower(strings.TrimSpace(cfg.ChannelType)) {
	case "dingtalk":
		return dingtalk.NewAdapter(dingtalk.Options{
			RepoRoot:              cfg.RepoRoot,
			QueueFile:             cfg.DingTalkQueueFile,
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

func getStatusPayload(repoRoot string) (StatusPayload, error) {
	_ = envfile.LoadDotEnvSetDefault(filepath.Join(repoRoot, ".env"))
	lockPath := strings.TrimSpace(os.Getenv("LOCK_FILE"))
	if lockPath == "" {
		lockPath = filepath.Join(repoRoot, ".cli_agent_gateway.lock")
	}
	resolvedLock, err := resolveAgainstRepo(repoRoot, lockPath)
	if err != nil {
		return StatusPayload{}, err
	}
	st, err := lockfile.Inspect(resolvedLock)
	if err != nil {
		return StatusPayload{}, err
	}
	payload := StatusPayload{Running: st.Locked, LockFile: resolvedLock, Metadata: st.Metadata}
	if st.OwnerPID != nil {
		pid := *st.OwnerPID
		payload.PID = &pid
	}
	if st.OwnerStartedAt != nil {
		payload.StartedAt = *st.OwnerStartedAt
	}
	return payload, nil
}

func statusJSON(action string, p StatusPayload, cfg config.AppConfig, logPath string) map[string]any {
	status := "stopped"
	if p.Running {
		status = "running"
	}
	lockFile := p.LockFile
	if strings.TrimSpace(cfg.LockFile) != "" {
		lockFile = cfg.LockFile
	}
	out := map[string]any{
		"ok":        true,
		"action":    action,
		"status":    status,
		"running":   p.Running,
		"lock_file": lockFile,
	}
	if p.PID != nil && *p.PID > 0 {
		out["pid"] = *p.PID
	}
	if strings.TrimSpace(p.StartedAt) != "" {
		out["started_at"] = p.StartedAt
	}
	if strings.TrimSpace(cfg.ChannelType) != "" {
		out["channel"] = cfg.ChannelType
	}
	if strings.TrimSpace(cfg.Workdir) != "" {
		out["workdir"] = cfg.Workdir
	}
	if strings.TrimSpace(cfg.StateFile) != "" {
		out["state_file"] = cfg.StateFile
	}
	if strings.TrimSpace(cfg.InteractionLogFile) != "" {
		out["interaction_log_file"] = cfg.InteractionLogFile
	}
	if strings.TrimSpace(logPath) != "" {
		out["log_file"] = logPath
	}
	if len(p.Metadata) > 0 {
		out["metadata"] = p.Metadata
	}
	return out
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

	envPath := filepath.Join(repoRoot, ".env")
	if _, err := os.Stat(envPath); err != nil {
		add("env", false, ".env missing", "run `cag config <workdir>` first")
		return p
	}
	add("env", true, ".env loaded", "")

	cfg, err := config.Load(repoRoot, "")
	if err != nil {
		add("config", false, err.Error(), "fix .env values and re-run")
		return p
	}
	p.Channel = cfg.ChannelType

	if _, err := os.Stat(cfg.Workdir); err != nil {
		add("workdir", false, fmt.Sprintf("workdir not accessible: %s", cfg.Workdir), "set CODEX_WORKDIR to an existing directory")
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
			add("imessage.fetch_cmd", false, "IMESSAGE_FETCH_CMD is empty", "set IMESSAGE_FETCH_CMD in .env")
		} else {
			add("imessage.fetch_cmd", true, "IMESSAGE_FETCH_CMD configured", "")
		}
		if strings.TrimSpace(cfg.IMessageSendCmd) == "" {
			add("imessage.send_cmd", false, "IMESSAGE_SEND_CMD is empty", "set IMESSAGE_SEND_CMD in .env")
		} else {
			add("imessage.send_cmd", true, "IMESSAGE_SEND_CMD configured", "")
		}
	case "dingtalk":
		mode := strings.ToLower(strings.TrimSpace(cfg.DingTalkSendMode))
		if mode == "webhook" {
			ok := strings.TrimSpace(cfg.DingTalkBotWebhook) != ""
			add("dingtalk.webhook", ok, "requires DINGTALK_BOT_WEBHOOK", "set DINGTALK_BOT_WEBHOOK for webhook mode")
		} else {
			k := strings.TrimSpace(cfg.DingTalkAppKey) != ""
			s := strings.TrimSpace(cfg.DingTalkAppSecret) != ""
			a := strings.TrimSpace(cfg.DingTalkAgentID) != ""
			add("dingtalk.app_key", k, "requires DINGTALK_APP_KEY", "set DINGTALK_APP_KEY")
			add("dingtalk.app_secret", s, "requires DINGTALK_APP_SECRET", "set DINGTALK_APP_SECRET")
			add("dingtalk.agent_id", a, "requires DINGTALK_AGENT_ID", "set DINGTALK_AGENT_ID")
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
	if fv := strings.TrimSpace(flagValue(args, "--log-file")); fv != "" {
		if !filepath.IsAbs(fv) {
			fv = filepath.Join(repoRoot, fv)
		}
		if abs, err := filepath.Abs(fv); err == nil {
			return abs
		}
		return fv
	}
	_ = envfile.LoadDotEnvSetDefault(filepath.Join(repoRoot, ".env"))
	v := strings.TrimSpace(os.Getenv("GATEWAY_LOG_FILE"))
	if v == "" {
		v = filepath.Join(repoRoot, ".agent_gateway.log")
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(repoRoot, v)
	}
	if abs, err := filepath.Abs(v); err == nil {
		return abs
	}
	return v
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
