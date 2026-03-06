package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cli-agent-gateway/internal/config"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultGatewaydAddr = "127.0.0.1:58473"
const defaultGatewaydLogFile = "logs/.gatewayd.log"
const gatewaydStateFileName = ".cli_agent_gatewayd.json"

type gatewaydState struct {
	PID       int    `json:"pid"`
	Listen    string `json:"listen"`
	StartedAt string `json:"started_at"`
	RepoRoot  string `json:"repo_root"`
}

type gatewayControlServer struct {
	gatewayv1.UnimplementedGatewayControlServer
	repoRoot string
}

func (s *gatewayControlServer) Status(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	payload, err := getStatusPayload(root)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	out := &gatewayv1.StatusResponse{
		Ok:                 true,
		Running:            payload.Running,
		LockFile:           payload.LockFile,
		StartedAt:          strings.TrimSpace(payload.StartedAt),
		Channel:            strings.TrimSpace(cfg.ChannelType),
		InteractionLogFile: strings.TrimSpace(cfg.InteractionLogFile),
		StateFile:          strings.TrimSpace(cfg.StateFile),
		LogFile:            strings.TrimSpace(resolveLogPath(root, nil)),
		Workdir:            strings.TrimSpace(cfg.Workdir),
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
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.SessionsResponse{Ok: false, Error: err.Error()}, nil
	}
	items, err := collectSessions(cfg)
	if err != nil {
		return &gatewayv1.SessionsResponse{Ok: false, Error: err.Error()}, nil
	}
	limit := int(req.GetLimit())
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := &gatewayv1.SessionsResponse{
		Ok:    true,
		Items: make([]*gatewayv1.SessionItem, 0, len(items)),
	}
	for _, it := range items {
		out.Items = append(out.Items, &gatewayv1.SessionItem{
			SessionKey:  it.SessionKey,
			SessionId:   it.SessionID,
			Channel:     it.Channel,
			Sender:      it.Sender,
			SenderName:  it.SenderName,
			ThreadId:    it.ThreadID,
			LastMessage: it.LastMessage,
			LastTime:    it.LastTime,
			Latest:      it.Latest,
		})
	}
	return out, nil
}

func (s *gatewayControlServer) Start(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	args := []string{"start", "--json"}
	if logFile := strings.TrimSpace(req.GetLogFile()); logFile != "" {
		args = append(args, "--log-file", logFile)
	}
	node, err := runLocalJSONAction(root, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Stop(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	args := []string{"stop", "--json"}
	if req.GetQuiet() {
		args = append(args, "--quiet")
	}
	node, err := runLocalJSONAction(root, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Restart(_ context.Context, req *gatewayv1.StatusRequest) (*gatewayv1.StatusResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	args := []string{"restart", "--json"}
	if logFile := strings.TrimSpace(req.GetLogFile()); logFile != "" {
		args = append(args, "--log-file", logFile)
	}
	node, err := runLocalJSONAction(root, args...)
	if err != nil {
		return &gatewayv1.StatusResponse{Ok: false, Error: err.Error()}, nil
	}
	return statusResponseFromNode(node), nil
}

func (s *gatewayControlServer) Health(_ context.Context, req *gatewayv1.HealthCheckRequest) (*gatewayv1.HealthCheckResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	args := []string{"health", "--json"}
	if req.GetIncludePaths() {
		args[0] = "doctor"
	}
	node, err := runLocalJSONAction(root, args...)
	if err != nil {
		return &gatewayv1.HealthCheckResponse{Ok: false, Error: err.Error(), Action: "health", Status: "failed"}, nil
	}
	return healthResponseFromNode(node, "health"), nil
}

func (s *gatewayControlServer) Doctor(_ context.Context, req *gatewayv1.HealthCheckRequest) (*gatewayv1.HealthCheckResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	node, err := runLocalJSONAction(root, "doctor", "--json")
	if err != nil {
		return &gatewayv1.HealthCheckResponse{Ok: false, Error: err.Error(), Action: "doctor", Status: "failed"}, nil
	}
	return healthResponseFromNode(node, "doctor"), nil
}

func (s *gatewayControlServer) SendToSession(_ context.Context, req *gatewayv1.SendToSessionRequest) (*gatewayv1.SendToSessionResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.SendToSessionResponse{Ok: false, Error: err.Error()}, nil
	}
	key := normalizeSessionKey(req.GetSessionKey())
	if key == "" {
		return &gatewayv1.SendToSessionResponse{Ok: false, Error: "session key required"}, nil
	}
	msgID := strings.TrimSpace(req.GetMessageId())
	if msgID == "" {
		msgID = fmt.Sprintf("manual-%d", time.Now().UnixMilli())
	}
	mt := strings.TrimSpace(req.GetMsgType())
	if mt == "" {
		mt = "text"
	}
	source := strings.TrimSpace(req.GetSource())
	if source == "" {
		source = "text"
	}
	payload, execErr := sendViaSessionKey(
		cfg,
		key,
		strings.TrimSpace(req.GetText()),
		mt,
		source,
		msgID,
		req.GetDryRun(),
	)
	resp := &gatewayv1.SendToSessionResponse{
		Ok:         payload.OK,
		Error:      payload.Error,
		Channel:    payload.Channel,
		To:         payload.To,
		MessageId:  payload.MessageID,
		MsgType:    payload.MsgType,
		DryRun:     payload.DryRun,
		Source:     payload.Source,
		SessionKey: payload.SessionKey,
		SessionId:  payload.SessionID,
		Result:     payload.Result,
		ElapsedSec: int32(payload.ElapsedSec),
	}
	if execErr != nil {
		resp.Ok = false
		if resp.Error == "" {
			resp.Error = execErr.Error()
		}
	}
	return resp, nil
}

func (s *gatewayControlServer) SessionMessages(_ context.Context, req *gatewayv1.SessionMessagesRequest) (*gatewayv1.SessionMessagesResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.SessionMessagesResponse{Ok: false, Error: err.Error()}, nil
	}
	key := normalizeSessionKey(req.GetSessionKey())
	if key == "" {
		return &gatewayv1.SessionMessagesResponse{Ok: false, Error: "session key required"}, nil
	}
	msgs, timeline, err := collectSessionMessages(cfg, key)
	if err != nil {
		return &gatewayv1.SessionMessagesResponse{Ok: false, Error: err.Error()}, nil
	}
	resp := &gatewayv1.SessionMessagesResponse{
		Ok:       true,
		Messages: make([]*gatewayv1.ChatMessage, 0, len(msgs)),
		Timeline: make([]*gatewayv1.TimelineEntry, 0, len(timeline)),
	}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, &gatewayv1.ChatMessage{
			Id:           m.ID,
			SourceMsgId:  m.SourceMsgID,
			Role:         m.Role,
			Text:         m.Text,
			Time:         m.Time,
			Status:       m.Status,
			StatusDetail: m.StatusDetail,
		})
	}
	for _, t := range timeline {
		entry := &gatewayv1.TimelineEntry{
			MsgId:  t.MsgID,
			Events: make([]*gatewayv1.ProcessEvent, 0, len(t.Events)),
		}
		for _, ev := range t.Events {
			entry.Events = append(entry.Events, &gatewayv1.ProcessEvent{
				Id:     ev.ID,
				Time:   ev.Time,
				Title:  ev.Title,
				Detail: ev.Detail,
			})
		}
		resp.Timeline = append(resp.Timeline, entry)
	}
	return resp, nil
}

func (s *gatewayControlServer) ClearSession(_ context.Context, req *gatewayv1.SessionKeyRequest) (*gatewayv1.SessionMutationResponse, error) {
	return s.mutateSession(req.GetRepoRoot(), req.GetSessionKey(), clearSessionMapping)
}

func (s *gatewayControlServer) DeleteSession(_ context.Context, req *gatewayv1.SessionKeyRequest) (*gatewayv1.SessionMutationResponse, error) {
	return s.mutateSession(req.GetRepoRoot(), req.GetSessionKey(), clearSessionMapping)
}

func (s *gatewayControlServer) DeleteAllSessions(_ context.Context, req *gatewayv1.EmptyRepoRequest) (*gatewayv1.SessionMutationResponse, error) {
	root := resolveReqRoot(s.repoRoot, req.GetRepoRoot())
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error()}, nil
	}
	if err := deleteAllSessionMappings(cfg); err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error()}, nil
	}
	return &gatewayv1.SessionMutationResponse{Ok: true}, nil
}

func (s *gatewayControlServer) mutateSession(repoRoot, sessionKey string, fn func(config.AppConfig, string) error) (*gatewayv1.SessionMutationResponse, error) {
	root := resolveReqRoot(s.repoRoot, repoRoot)
	cfg, err := config.Load(root, "")
	if err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error()}, nil
	}
	key := normalizeSessionKey(sessionKey)
	if key == "" {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: "session key required"}, nil
	}
	if err := fn(cfg, key); err != nil {
		return &gatewayv1.SessionMutationResponse{Ok: false, Error: err.Error(), SessionKey: key}, nil
	}
	return &gatewayv1.SessionMutationResponse{Ok: true, SessionKey: key}, nil
}

func runLocalJSONAction(repoRoot string, args ...string) (map[string]any, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CAG_GRPC_DISABLE=1")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
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
	addr := strings.TrimSpace(*listen)
	if addr == "" {
		addr = defaultGatewaydAddr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gatewayd listen failed: %v\n", err)
		return 1
	}
	defer ln.Close()

	srv := grpc.NewServer()
	gatewayv1.RegisterGatewayControlServer(srv, &gatewayControlServer{repoRoot: repoRoot})
	fmt.Printf("gatewayd listening=%s\n", addr)
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "gatewayd serve failed: %v\n", err)
		return 1
	}
	return 0
}

func runGatewaydUp(repoRoot string, args []string) int {
	jsonOut := hasFlag(args, "--json")
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
	if state, err := loadGatewaydState(repoRoot); err == nil {
		if state.PID > 0 && processAlive(state.PID) && strings.TrimSpace(state.Listen) == addr {
			return nil
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
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "gatewayd", "--listen", addr)
	cmd.Dir = repoRoot
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "CAG_GRPC_DISABLE=1")
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
		RepoRoot:  repoRoot,
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
	return filepath.Join(repoRoot, gatewaydStateFileName)
}

func loadGatewaydState(repoRoot string) (gatewaydState, error) {
	raw, err := os.ReadFile(gatewaydStatePath(repoRoot))
	if err != nil {
		return gatewaydState{}, err
	}
	var state gatewaydState
	if err := json.Unmarshal(raw, &state); err != nil {
		return gatewaydState{}, err
	}
	return state, nil
}

func saveGatewaydState(repoRoot string, state gatewaydState) error {
	path := gatewaydStatePath(repoRoot)
	tmp := path + ".tmp"
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeGatewaydState(repoRoot string) error {
	path := gatewaydStatePath(repoRoot)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func resolveGatewaydLogPath(repoRoot string) string {
	if v := strings.TrimSpace(os.Getenv("GATEWAYD_LOG_FILE")); v != "" {
		if filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(repoRoot, v)
	}
	return filepath.Join(repoRoot, defaultGatewaydLogFile)
}

func tryStatusViaGRPC(repoRoot string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	return cli.Status(ctx, &gatewayv1.StatusRequest{RepoRoot: repoRoot})
}

func tryStartViaGRPC(repoRoot, logFile string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	return cli.Start(ctx, &gatewayv1.StatusRequest{
		RepoRoot: repoRoot,
		LogFile:  strings.TrimSpace(logFile),
	})
}

func tryStopViaGRPC(repoRoot string, quiet bool) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	return cli.Stop(ctx, &gatewayv1.StatusRequest{
		RepoRoot: repoRoot,
		Quiet:    quiet,
	})
}

func tryRestartViaGRPC(repoRoot, logFile string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1800*time.Millisecond)
	defer cancel()
	return cli.Restart(ctx, &gatewayv1.StatusRequest{
		RepoRoot: repoRoot,
		LogFile:  strings.TrimSpace(logFile),
	})
}

func tryHealthViaGRPC(repoRoot string, includePaths bool) (*gatewayv1.HealthCheckResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Health(ctx, &gatewayv1.HealthCheckRequest{
		RepoRoot:     repoRoot,
		IncludePaths: includePaths,
	})
}

func tryDoctorViaGRPC(repoRoot string, includePaths bool) (*gatewayv1.HealthCheckResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Doctor(ctx, &gatewayv1.HealthCheckRequest{
		RepoRoot:     repoRoot,
		IncludePaths: includePaths,
	})
}

func trySessionsViaGRPC(repoRoot string, limit int) (*gatewayv1.SessionsResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.Sessions(ctx, &gatewayv1.SessionsRequest{
		RepoRoot: repoRoot,
		Limit:    int32(limit),
	})
}

func trySendToSessionViaGRPC(repoRoot, sessionKey, text, messageID, msgType string, dryRun bool, source string) (*gatewayv1.SendToSessionResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), sendViaSessionGRPCTimeout())
	defer cancel()
	return cli.SendToSession(ctx, &gatewayv1.SendToSessionRequest{
		RepoRoot:   repoRoot,
		SessionKey: sessionKey,
		Text:       text,
		MessageId:  messageID,
		MsgType:    msgType,
		DryRun:     dryRun,
		Source:     source,
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
		RepoRoot:   repoRoot,
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
		RepoRoot:   repoRoot,
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
	return cli.DeleteAllSessions(ctx, &gatewayv1.EmptyRepoRequest{RepoRoot: repoRoot})
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
	if v := strings.TrimSpace(os.Getenv("GATEWAYD_ADDR")); v != "" {
		return v
	}
	return defaultGatewaydAddr
}

func grpcDisabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("CAG_GRPC_DISABLE")))
	return raw == "1" || raw == "true" || raw == "yes"
}

func resolveReqRoot(defaultRoot, reqRoot string) string {
	root := strings.TrimSpace(reqRoot)
	if root != "" {
		return root
	}
	return defaultRoot
}

func sendViaSessionGRPCTimeout() time.Duration {
	timeoutSec := 120
	raw := strings.TrimSpace(os.Getenv("AGENT_TIMEOUT_SEC"))
	if raw != "" {
		if n, err := fmt.Sscanf(raw, "%d", &timeoutSec); err == nil && n == 1 && timeoutSec > 0 {
			// parsed
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
