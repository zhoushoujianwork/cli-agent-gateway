package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
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
	root := strings.TrimSpace(req.GetRepoRoot())
	if root == "" {
		root = s.repoRoot
	}
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
	root := strings.TrimSpace(req.GetRepoRoot())
	if root == "" {
		root = s.repoRoot
	}
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
	cli, conn, err := grpcGatewayClient()
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

func grpcGatewayClient() (gatewayv1.GatewayControlClient, *grpc.ClientConn, error) {
	if grpcDisabled() {
		return nil, nil, fmt.Errorf("grpc disabled")
	}
	addr := gatewaydAddr()
	ctx, cancel := context.WithTimeout(context.Background(), 280*time.Millisecond)
	defer cancel()
	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, err
	}
	return gatewayv1.NewGatewayControlClient(conn), conn, nil
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
