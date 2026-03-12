package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cli-agent-gateway/internal/config"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/infra/envfile"
	"cli-agent-gateway/internal/infra/proclog"
	"cli-agent-gateway/internal/utils/sessionctl"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultGatewaydAddr = "127.0.0.1:58473"
const defaultGatewaydLogFile = "logs/gatewayd.log"
const gatewaydStateFileName = ".cli_agent_gatewayd.json"

var gatewayAddrEnvOnce sync.Once

type gatewaydState struct {
	PID       int    `json:"pid"`
	Listen    string `json:"listen"`
	StartedAt string `json:"started_at"`
}

type gatewayControlServer struct {
	gatewayv1.UnimplementedGatewayControlServer
	repoRoot string
	mu       sync.Mutex
	managers map[string]*sessionctl.RuntimeManager
}

func (s *gatewayControlServer) Status(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	payload, err := getStatusPayload(s.repoRoot)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	cfg, err := config.Load(s.repoRoot, "")
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	logFile := strings.TrimSpace(resolveLogPath(s.repoRoot, nil))
	if v, ok := payload.Metadata["log_file"].(string); ok && strings.TrimSpace(v) != "" {
		logFile = strings.TrimSpace(v)
	}
	out := &gatewayv1.StatusResponse{
		Ok:                 true,
		Running:            payload.Running,
		LockFile:           effectiveStatusLockFile(payload, cfg),
		StartedAt:          strings.TrimSpace(payload.StartedAt),
		Channel:            effectiveStatusChannel(payload, cfg),
		InteractionLogFile: strings.TrimSpace(cfg.InteractionLogFile),
		StateFile:          strings.TrimSpace(cfg.StateFile),
		LogFile:            logFile,
		Workdir:            effectiveStatusWorkdir(payload, cfg),
		Status:             "stopped",
	}
	if payload.Running {
		out.Status = "running"
	}
	if payload.PID != nil {
		out.HasPid = true
		out.Pid = int64(*payload.PID)
	}
	return out, nil
}

func (s *gatewayControlServer) Sessions(_ context.Context, req *gatewayv1.SessionsRequest) (*gatewayv1.SessionsResponse, error) {
	return &gatewayv1.SessionsResponse{Ok: false, Error: "deprecated RPC: use action RPC session.list"}, nil
}

func (s *gatewayControlServer) SessionNew(_ context.Context, req *gatewayv1.SessionNewRequest) (*gatewayv1.SessionNewResponse, error) {
	return &gatewayv1.SessionNewResponse{Ok: false, Error: "deprecated RPC: use action RPC session.create"}, nil
}

func (s *gatewayControlServer) Start(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	args := []string{"start", "--json"}
	if logFile := strings.TrimSpace(req.GetLogFile()); logFile != "" {
		args = append(args, "--log-file", logFile)
	}
	node, err := runLocalJSONAction(s.repoRoot, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Stop(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	args := []string{"stop", "--json"}
	if req.GetQuiet() {
		args = append(args, "--quiet")
	}
	node, err := runLocalJSONAction(s.repoRoot, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Restart(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	args := []string{"restart", "--json"}
	if logFile := strings.TrimSpace(req.GetLogFile()); logFile != "" {
		args = append(args, "--log-file", logFile)
	}
	node, err := runLocalJSONAction(s.repoRoot, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Health(_ context.Context, req *gatewayv1.HealthCheckRequest) (*gatewayv1.HealthCheckResponse, error) {
	args := []string{"health", "--json"}
	if req.GetIncludePaths() {
		args[0] = "doctor"
	}
	node, err := runLocalJSONAction(s.repoRoot, args...)
	if err != nil {
		return &gatewayv1.HealthCheckResponse{Ok: false, Error: err.Error(), Action: "health", Status: "failed"}, nil
	}
	return healthResponseFromNode(node, "health"), nil
}

func (s *gatewayControlServer) Doctor(_ context.Context, req *gatewayv1.HealthCheckRequest) (*gatewayv1.HealthCheckResponse, error) {
	node, err := runLocalJSONAction(s.repoRoot, "doctor", "--json")
	if err != nil {
		return &gatewayv1.HealthCheckResponse{Ok: false, Error: err.Error(), Action: "doctor", Status: "failed"}, nil
	}
	return healthResponseFromNode(node, "doctor"), nil
}

func (s *gatewayControlServer) SendToSession(_ context.Context, req *gatewayv1.SendToSessionRequest) (*gatewayv1.SendToSessionResponse, error) {
	return &gatewayv1.SendToSessionResponse{Ok: false, Error: "deprecated RPC: use action RPC session.send"}, nil
}

func (s *gatewayControlServer) SessionMessages(_ context.Context, req *gatewayv1.SessionMessagesRequest) (*gatewayv1.SessionMessagesResponse, error) {
	return &gatewayv1.SessionMessagesResponse{Ok: false, Error: "deprecated RPC: use action RPC session.messages"}, nil
}

func (s *gatewayControlServer) ClearSession(_ context.Context, req *gatewayv1.SessionKeyRequest) (*gatewayv1.SessionMutationResponse, error) {
	return &gatewayv1.SessionMutationResponse{Ok: false, Error: "deprecated RPC: use action RPC session.clear"}, nil
}

func (s *gatewayControlServer) DeleteSession(_ context.Context, req *gatewayv1.SessionKeyRequest) (*gatewayv1.SessionMutationResponse, error) {
	return &gatewayv1.SessionMutationResponse{Ok: false, Error: "deprecated RPC: use action RPC session.delete"}, nil
}

func (s *gatewayControlServer) DeleteAllSessions(_ context.Context, req *gatewayv1.EmptyRepoRequest) (*gatewayv1.SessionMutationResponse, error) {
	return &gatewayv1.SessionMutationResponse{Ok: false, Error: "deprecated RPC: no bulk-delete in grouped commands"}, nil
}

func (s *gatewayControlServer) mutateSession(repoRoot, sessionKey string, fn func(config.AppConfig, string) error) (*gatewayv1.SessionMutationResponse, error) {
	_ = repoRoot
	cfg, err := config.Load(s.repoRoot, "")
	if err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error()}, nil
	}
	key := sessionctl.NormalizeSessionKey(sessionKey)
	if key == "" {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: "session key required"}, nil
	}
	if err := fn(cfg, key); err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error(), SessionKey: key}, nil
	}
	return &gatewayv1.SessionMutationResponse{Ok: true, SessionKey: key}, nil
}

func runLocalJSONAction(repoRoot string, args ...string) (map[string]any, error) {
	cmd, err := newSelfCommand(repoRoot, args...)
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if shouldRetryWithGoRun(runErr) {
		cmd = exec.Command("go", append([]string{"run", "./cmd/gateway-cli"}, args...)...)
		cmd.Dir = repoRoot
		cmd.Env = managedChildEnv("CAG_GRPC_DISABLE=1")
		stdout.Reset()
		stderr.Reset()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr = cmd.Run()
	}
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		raw = strings.TrimSpace(stderr.String())
	}
	if raw == "" {
		if runErr != nil {
			return nil, runErr
		}
		return nil, fmt.Errorf("empty json output")
	}
	var node map[string]any
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return node, nil
}

func newSelfCommand(repoRoot string, args ...string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	name, argv := selfCommandSpec(exe, args...)
	if err != nil && name == "" {
		return nil, err
	}
	cmd := exec.Command(name, argv...)
	cmd.Dir = repoRoot
	cmd.Env = managedChildEnv("CAG_GRPC_DISABLE=1")
	return cmd, nil
}

func selfCommandSpec(exe string, args ...string) (string, []string) {
	if strings.TrimSpace(exe) != "" {
		if _, statErr := os.Stat(exe); statErr == nil {
			return exe, args
		}
	}
	return "go", append([]string{"run", "./cmd/gateway-cli"}, args...)
}

func shouldRetryWithGoRun(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "fork/exec") && strings.Contains(msg, "no such file or directory")
}

func statusResponseFromNode(node map[string]any) *gatewayv1.StatusResponse {
	resp := &gatewayv1.StatusResponse{
		Ok:                 boolFromNode(node, "ok"),
		Error:              errorMessageFromNode(node),
		Running:            boolFromNode(node, "running"),
		StartedAt:          stringFromNode(node, "started_at"),
		LockFile:           stringFromNode(node, "lock_file"),
		Channel:            stringFromNode(node, "channel"),
		Workdir:            stringFromNode(node, "workdir"),
		LogFile:            stringFromNode(node, "log_file"),
		InteractionLogFile: stringFromNode(node, "interaction_log_file"),
		StateFile:          stringFromNode(node, "state_file"),
		Status:             stringFromNode(node, "status"),
	}
	if pid, ok := int64FromNode(node, "pid"); ok && pid > 0 {
		resp.HasPid = true
		resp.Pid = pid
	}
	if resp.Status == "" {
		if resp.Running {
			resp.Status = "running"
		} else {
			resp.Status = "stopped"
		}
	}
	return resp
}

func healthResponseFromNode(node map[string]any, fallbackAction string) *gatewayv1.HealthCheckResponse {
	resp := &gatewayv1.HealthCheckResponse{
		Ok:      boolFromNode(node, "ok"),
		Error:   errorMessageFromNode(node),
		Action:  nonEmpty(stringFromNode(node, "action"), fallbackAction),
		Status:  stringFromNode(node, "status"),
		Channel: stringFromNode(node, "channel"),
		Items:   []*gatewayv1.HealthCheckItem{},
	}
	rawItems, _ := node["items"].([]any)
	for _, it := range rawItems {
		itemNode, _ := it.(map[string]any)
		if len(itemNode) == 0 {
			continue
		}
		resp.Items = append(resp.Items, &gatewayv1.HealthCheckItem{
			Key:        stringFromNode(itemNode, "key"),
			Ok:         boolFromNode(itemNode, "ok"),
			Detail:     stringFromNode(itemNode, "detail"),
			Suggestion: stringFromNode(itemNode, "suggestion"),
		})
	}
	if resp.Status == "" {
		if resp.Ok {
			resp.Status = "healthy"
		} else {
			resp.Status = "failed"
		}
	}
	return resp
}

func boolFromNode(node map[string]any, key string) bool {
	v, ok := node[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func stringFromNode(node map[string]any, key string) string {
	v, ok := node[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func int64FromNode(node map[string]any, key string) (int64, bool) {
	v, ok := node[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func errorMessageFromNode(node map[string]any) string {
	errNode, ok := node["error"]
	if !ok || errNode == nil {
		return ""
	}
	if m, ok := errNode.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			return strings.TrimSpace(msg)
		}
		if msg, ok := m["error"].(string); ok {
			return strings.TrimSpace(msg)
		}
	}
	return strings.TrimSpace(fmt.Sprint(errNode))
}

func runGatewayd(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("gatewayd", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", gatewaydAddr(), "gRPC listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	proclog.Configure()
	if err := ensureACPCommandAvailable(repoRoot); err != nil {
		proclog.Error("gatewayd", map[string]any{
			"event": "lifecycle",
			"phase": "preflight_failed",
			"err":   err.Error(),
		})
		return 1
	}
	addr := strings.TrimSpace(*listen)
	if addr == "" {
		addr = defaultGatewaydAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		proclog.Error("gatewayd", map[string]any{
			"event": "lifecycle",
			"phase": "listen_failed",
			"addr":  addr,
			"err":   err.Error(),
		})
		return 1
	}
	defer ln.Close()

	srv := grpc.NewServer()
	gatewayv1.RegisterGatewayControlServer(srv, &gatewayControlServer{repoRoot: repoRoot, managers: map[string]*sessionctl.RuntimeManager{}})
	proclog.Info("gatewayd", map[string]any{
		"event": "lifecycle",
		"phase": "listening",
		"addr":  addr,
	})
	if err := srv.Serve(ln); err != nil {
		proclog.Error("gatewayd", map[string]any{
			"event": "lifecycle",
			"phase": "serve_failed",
			"addr":  addr,
			"err":   err.Error(),
		})
		return 1
	}
	return 0
}

func runGatewaydStatus(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("gatewayd-status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload := inspectGatewaydStatus(repoRoot)
	if *jsonOut {
		printJSON(payload)
		return 0
	}
	fmt.Println(mustJSON(payload))
	return 0
}

func runGatewaydUp(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	if err := ensureACPCommandAvailable(repoRoot); err != nil {
		if jsonOut {
			printJSONActionError("gatewayd-up", "acp_preflight_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "gatewayd-up failed: %v\n", err)
		return 1
	}
	if err := ensureGatewaydRunning(repoRoot); err != nil {
		if jsonOut {
			printJSONActionError("gatewayd-up", "gatewayd_up_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "gatewayd-up failed: %v\n", err)
		return 1
	}
	state, _ := loadGatewaydState(repoRoot)
	if jsonOut {
		payload := map[string]any{
			"ok":     true,
			"action": "gatewayd-up",
			"listen": gatewaydAddr(),
		}
		if state.PID > 0 {
			payload["pid"] = state.PID
		}
		if strings.TrimSpace(state.StartedAt) != "" {
			payload["started_at"] = strings.TrimSpace(state.StartedAt)
		}
		printJSON(payload)
		return 0
	}
	if state.PID > 0 {
		fmt.Printf("gatewayd ready listen=%s pid=%d\n", gatewaydAddr(), state.PID)
	} else {
		fmt.Printf("gatewayd ready listen=%s\n", gatewaydAddr())
	}
	return 0
}

func inspectGatewaydStatus(repoRoot string) map[string]any {
	addr := gatewaydAddr()
	logFile := resolveGatewaydLogPath(repoRoot)
	stateFile := gatewaydStatePath(repoRoot)
	state, stateErr := loadGatewaydState(repoRoot)
	statePresent := stateErr == nil
	pidAlive := state.PID > 0 && processAlive(state.PID)
	reachable := false
	if conn, err := dialGateway(addr, 200*time.Millisecond); err == nil {
		reachable = true
		_ = conn.Close()
	}
	status := "stopped"
	running := false
	switch {
	case reachable:
		status = "running"
		running = true
	case pidAlive:
		status = "degraded"
		running = true
	}
	payload := map[string]any{
		"ok":           true,
		"action":       "gatewayd-status",
		"status":       status,
		"running":      running,
		"reachable":    reachable,
		"listen":       addr,
		"gateway_addr": addr,
		"log_file":     logFile,
		"state_file":   stateFile,
		"managed":      statePresent,
	}
	if statePresent {
		if state.PID > 0 {
			payload["pid"] = state.PID
			payload["pid_alive"] = pidAlive
		}
		if strings.TrimSpace(state.StartedAt) != "" {
			payload["started_at"] = strings.TrimSpace(state.StartedAt)
		}
		if strings.TrimSpace(state.Listen) != "" {
			payload["listen"] = strings.TrimSpace(state.Listen)
			payload["gateway_addr"] = strings.TrimSpace(state.Listen)
		}
	}
	return payload
}

func runGatewaydDown(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
	stopped, err := shutdownManagedGatewayd(repoRoot)
	if err != nil {
		if jsonOut {
			printJSONActionError("gatewayd-down", "gatewayd_down_failed", err.Error())
			return 1
		}
		fmt.Fprintf(os.Stderr, "gatewayd-down failed: %v\n", err)
		return 1
	}
	if jsonOut {
		printJSON(map[string]any{
			"ok":      true,
			"action":  "gatewayd-down",
			"listen":  gatewaydAddr(),
			"stopped": stopped,
		})
		return 0
	}
	if stopped {
		fmt.Printf("gatewayd stopped listen=%s\n", gatewaydAddr())
	} else {
		fmt.Printf("gatewayd not managed or already stopped listen=%s\n", gatewaydAddr())
	}
	return 0
}

func ensureGatewaydRunning(repoRoot string) error {
	if grpcDisabled() {
		return fmt.Errorf("grpc disabled by CAG_GRPC_DISABLE")
	}
	addr := gatewaydAddr()
	if conn, err := dialGateway(addr, 250*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil
	}
	if err := startManagedGatewayd(repoRoot, addr); err != nil {
		// Another process may have already started gatewayd. Fall through to readiness probe.
	}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := dialGateway(addr, 350*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(120 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("gatewayd did not become ready in time")
	}
	return lastErr
}

func startManagedGatewayd(repoRoot, addr string) error {
	if err := ensureACPCommandAvailable(repoRoot); err != nil {
		return err
	}
	if state, err := loadGatewaydState(repoRoot); err == nil {
		if state.PID > 0 && processAlive(state.PID) && strings.TrimSpace(state.Listen) == addr {
			if conn, err := dialGateway(addr, 250*time.Millisecond); err == nil {
				_ = conn.Close()
				return nil
			}
		}
		_ = removeGatewaydState(repoRoot)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := resolveGatewaydLogPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "gatewayd", "--listen", addr)
	cmd.Dir = managedGatewaydWorkdir()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = managedChildEnv("CAG_GRPC_DISABLE=1")
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	if err := saveGatewaydState(repoRoot, gatewaydState{
		PID:       pid,
		Listen:    addr,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return err
	}
	return nil
}

func shutdownManagedGatewayd(repoRoot string) (bool, error) {
	state, err := loadGatewaydState(repoRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if state.PID <= 0 {
		_ = removeGatewaydState(repoRoot)
		return false, nil
	}
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		_ = removeGatewaydState(repoRoot)
		return false, nil
	}
	if processAlive(state.PID) {
		if err := signalTerminate(proc); err != nil {
			return false, err
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(state.PID) {
				break
			}
			time.Sleep(120 * time.Millisecond)
		}
		if processAlive(state.PID) {
			_ = signalKill(proc)
			time.Sleep(200 * time.Millisecond)
		}
	}
	_ = removeGatewaydState(repoRoot)
	return true, nil
}

func gatewaydStatePath(repoRoot string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.TempDir()
	}
	return gatewaydStatePathForHome(home, repoRoot)
}

func gatewaydStatePathForHome(home, repoRoot string) string {
	_ = repoRoot
	return filepath.Join(home, ".cag", "gatewayd", gatewaydStateFileName)
}

func loadGatewaydState(repoRoot string) (gatewaydState, error) {
	path := gatewaydStatePath(repoRoot)
	raw, err := os.ReadFile(path)
	if err == nil {
		var state gatewaydState
		if err := json.Unmarshal(raw, &state); err != nil {
			return gatewaydState{}, err
		}
		return state, nil
	}
	if !os.IsNotExist(err) {
		return gatewaydState{}, err
	}
	if migrated, migrateErr := migrateLegacyGatewaydState(repoRoot); migrateErr != nil {
		return gatewaydState{}, migrateErr
	} else if migrated {
		raw, err = os.ReadFile(path)
		if err != nil {
			return gatewaydState{}, err
		}
		var state gatewaydState
		if err := json.Unmarshal(raw, &state); err != nil {
			return gatewaydState{}, err
		}
		return state, nil
	}
	return gatewaydState{}, os.ErrNotExist
}

func saveGatewaydState(repoRoot string, state gatewaydState) error {
	path := gatewaydStatePath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

func removeGatewaydState(repoRoot string) error {
	path := gatewaydStatePath(repoRoot)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func migrateLegacyGatewaydState(repoRoot string) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.TempDir()
	}
	legacyPath := legacyGatewaydStatePathForHome(home, repoRoot)
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var state gatewaydState
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, err
	}
	if err := saveGatewaydState(repoRoot, state); err != nil {
		return false, err
	}
	_ = os.Remove(legacyPath)
	_ = os.Remove(filepath.Dir(legacyPath))
	return true, nil
}

func legacyGatewaydStatePathForHome(home, repoRoot string) string {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	base := sanitizeStateToken(filepath.Base(root))
	if base == "" || base == "." {
		base = "repo"
	}
	sum := sha1.Sum([]byte(root))
	repoKey := fmt.Sprintf("%s-%x", base, sum[:8])
	return filepath.Join(home, ".cag", "gatewayd", repoKey, gatewaydStateFileName)
}

func sanitizeStateToken(v string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(v) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := strings.Trim(strings.TrimSpace(b.String()), "-")
	return out
}

func resolveGatewaydLogPath(repoRoot string) string {
	_ = repoRoot
	baseDir := filepath.Join(config.CAGHomeDir(), "gatewayd")
	if v := strings.TrimSpace(os.Getenv("GATEWAYD_LOG_FILE")); v != "" {
		if filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(baseDir, v)
	}
	return filepath.Join(baseDir, "gatewayd.log")
}

func managedGatewaydWorkdir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return os.TempDir()
}

func tryStatusViaGRPC(repoRoot string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	return cli.Status(ctx, &gatewayv1.StatusRequest{})
}

func tryStartViaGRPC(repoRoot, logFile string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	return cli.Start(ctx, &gatewayv1.StatusRequest{LogFile: strings.TrimSpace(logFile)})
}

func tryStopViaGRPC(repoRoot string, quiet bool) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	return cli.Stop(ctx, &gatewayv1.StatusRequest{Quiet: quiet})
}

func tryRestartViaGRPC(repoRoot, logFile string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	return cli.Restart(ctx, &gatewayv1.StatusRequest{LogFile: strings.TrimSpace(logFile)})
}

func tryHealthViaGRPC(repoRoot string, includePaths bool) (*gatewayv1.HealthCheckResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Health(ctx, &gatewayv1.HealthCheckRequest{IncludePaths: includePaths})
}

func tryDoctorViaGRPC(repoRoot string, includePaths bool) (*gatewayv1.HealthCheckResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Doctor(ctx, &gatewayv1.HealthCheckRequest{IncludePaths: includePaths})
}

func trySessionsViaGRPC(repoRoot string, limit int) (*gatewayv1.SessionsResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Sessions(ctx, &gatewayv1.SessionsRequest{Limit: int32(limit)})
}

func trySendToSessionViaGRPC(repoRoot, sessionKey, text, messageID, msgType string, dryRun bool, source, workdir string) (*gatewayv1.SendToSessionResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), sendViaSessionGRPCTimeout(repoRoot))
	defer cancel()
	return cli.SendToSession(ctx, &gatewayv1.SendToSessionRequest{
		SessionKey: sessionKey,
		Text:       text,
		MessageId:  messageID,
		MsgType:    msgType,
		DryRun:     dryRun,
		Source:     source,
		Workdir:    workdir,
	})
}

func trySessionNewViaGRPC(repoRoot, sessionKey, workdir string) (*gatewayv1.SessionNewResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.SessionNew(ctx, &gatewayv1.SessionNewRequest{
		SessionKey: sessionKey,
		Workdir:    workdir,
	})
}

func trySessionMessagesViaGRPC(repoRoot, sessionKey string) (*gatewayv1.SessionMessagesResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.SessionMessages(ctx, &gatewayv1.SessionMessagesRequest{
		SessionKey: sessionKey,
	})
}

func tryClearSessionViaGRPC(repoRoot, sessionKey string) (*gatewayv1.SessionMutationResponse, error) {
	return trySessionMutationViaGRPC(repoRoot, sessionKey, "clear")
}

func tryDeleteSessionViaGRPC(repoRoot, sessionKey string) (*gatewayv1.SessionMutationResponse, error) {
	return trySessionMutationViaGRPC(repoRoot, sessionKey, "delete")
}

func trySessionMutationViaGRPC(repoRoot, sessionKey, mode string) (*gatewayv1.SessionMutationResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	req := &gatewayv1.SessionKeyRequest{
		SessionKey: sessionKey,
	}
	if mode == "delete" {
		return cli.DeleteSession(ctx, req)
	}
	return cli.ClearSession(ctx, req)
}

func tryDeleteAllSessionsViaGRPC(repoRoot string) (*gatewayv1.SessionMutationResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.DeleteAllSessions(ctx, &gatewayv1.EmptyRepoRequest{})
}

func grpcGatewayClient(repoRoot string) (gatewayv1.GatewayControlClient, *grpc.ClientConn, error) {
	if grpcDisabled() {
		return nil, nil, fmt.Errorf("grpc disabled")
	}
	addr := gatewaydAddr()
	conn, err := dialGateway(addr, 800*time.Millisecond)
	if err != nil {
		if ensureErr := ensureGatewaydRunning(repoRoot); ensureErr != nil {
			return nil, nil, ensureErr
		}
		conn, err = dialGateway(addr, 1200*time.Millisecond)
		if err != nil {
			return nil, nil, err
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return gatewayv1.NewGatewayControlClient(conn), conn, nil
}

func dialGateway(addr string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
}

func gatewaydAddr() string {
	loadGatewayAddrEnvDefaults()
	if v := strings.TrimSpace(os.Getenv("GATEWAYD_ADDR")); v != "" {
		return v
	}
	return defaultGatewaydAddr
}

func loadGatewayAddrEnvDefaults() {
	gatewayAddrEnvOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return
		}
		_ = envfile.LoadDotEnvSetDefault(filepath.Join(home, ".cag", ".env"))
	})
}

func grpcDisabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("CAG_GRPC_DISABLE")))
	return raw == "1" || raw == "true" || raw == "yes"
}

func sendViaSessionGRPCTimeout(repoRoot string) time.Duration {
	timeoutSec := 120
	if cfg, err := config.Load(repoRoot, ""); err == nil && cfg.TimeoutSec > 0 {
		timeoutSec = cfg.TimeoutSec
	} else {
		raw := strings.TrimSpace(os.Getenv("AGENT_TIMEOUT_SEC"))
		if raw != "" {
			if n, err := fmt.Sscanf(raw, "%d", &timeoutSec); err == nil && n == 1 && timeoutSec > 0 {
				// parsed
			}
		}
	}
	if timeoutSec < 30 {
		timeoutSec = 30
	}
	return time.Duration(timeoutSec+30) * time.Second
}

func formatGatewayUnavailable(err error) string {
	return fmt.Sprintf("gatewayd unreachable at %s: %v (run: cag gatewayd --listen %s)", gatewaydAddr(), err, gatewaydAddr())
}
