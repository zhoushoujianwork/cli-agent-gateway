package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"cli-agent-gateway/internal/config"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultGatewaydAddr = "127.0.0.1:58473"

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

func tryStatusViaGRPC(repoRoot string) (*gatewayv1.StatusResponse, error) {
	cli, conn, err := grpcGatewayClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	return cli.Status(ctx, &gatewayv1.StatusRequest{RepoRoot: repoRoot})
}

func trySessionsViaGRPC(repoRoot string, limit int) (*gatewayv1.SessionsResponse, error) {
	cli, conn, err := grpcGatewayClientAutoStart()
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
	cli, conn, err := grpcGatewayClientAutoStart()
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
	cli, conn, err := grpcGatewayClientAutoStart()
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
	cli, conn, err := grpcGatewayClientAutoStart()
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
	cli, conn, err := grpcGatewayClientAutoStart()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	return cli.DeleteAllSessions(ctx, &gatewayv1.EmptyRepoRequest{RepoRoot: repoRoot})
}

func grpcGatewayClient() (gatewayv1.GatewayControlClient, *grpc.ClientConn, error) {
	return grpcGatewayClientWithOptions(false)
}

func grpcGatewayClientAutoStart() (gatewayv1.GatewayControlClient, *grpc.ClientConn, error) {
	return grpcGatewayClientWithOptions(true)
}

func grpcGatewayClientWithOptions(autoStart bool) (gatewayv1.GatewayControlClient, *grpc.ClientConn, error) {
	if grpcDisabled() {
		return nil, nil, fmt.Errorf("grpc disabled")
	}
	addr := gatewaydAddr()
	conn, err := dialGateway(addr, 280*time.Millisecond)
	if err == nil {
		return gatewayv1.NewGatewayControlClient(conn), conn, nil
	}
	if !autoStart {
		return nil, nil, err
	}
	startErr := startGatewaydDetached(addr)
	if startErr != nil {
		return nil, nil, fmt.Errorf("dial gatewayd failed: %v; auto-start failed: %v", err, startErr)
	}
	conn, err = dialGateway(addr, 1800*time.Millisecond)
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

func startGatewaydDetached(addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p := exec.Command(exe, "gatewayd", "--listen", addr)
	p.Stdout = io.Discard
	p.Stderr = io.Discard
	configureDetachedProcess(p)
	if err := p.Start(); err != nil {
		return err
	}
	_ = p.Process.Release()
	time.Sleep(250 * time.Millisecond)
	return nil
}
