package main

import (
	"testing"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestShouldRefreshGatewaydForAction(t *testing.T) {
	if !shouldRefreshGatewaydForAction(status.Error(codes.Unimplemented, "unknown method Action for service gateway.v1.GatewayControl")) {
		t.Fatalf("expected unimplemented action RPC to trigger gatewayd refresh")
	}
	if shouldRefreshGatewaydForAction(status.Error(codes.Unavailable, "transport is closing")) {
		t.Fatalf("did not expect unavailable error to trigger gatewayd refresh")
	}
	if shouldRefreshGatewaydForAction(status.Error(codes.Unimplemented, "unknown method Status for service gateway.v1.GatewayControl")) {
		t.Fatalf("did not expect unrelated unimplemented RPC to trigger gatewayd refresh")
	}
}

func TestShouldRefreshGatewaydForRestart(t *testing.T) {
	if !shouldRefreshGatewaydForRestart("fork/exec /var/folders/.../go-build123/exe/gateway-cli: no such file or directory") {
		t.Fatalf("expected stale gatewayd executable error to trigger refresh")
	}
	if shouldRefreshGatewaydForRestart("rpc error: code = Unavailable desc = connection closed") {
		t.Fatalf("did not expect unrelated restart error to trigger refresh")
	}
}

func TestShouldRefreshGatewaydForActionResponse(t *testing.T) {
	if !shouldRefreshGatewaydForActionResponse(&gatewayv1.ActionResponse{
		Ok:    false,
		Error: "unsupported action: channel.disable",
	}) {
		t.Fatalf("expected unsupported action response to trigger gatewayd refresh")
	}
	if shouldRefreshGatewaydForActionResponse(&gatewayv1.ActionResponse{
		Ok:    false,
		Error: "session not found: foo",
	}) {
		t.Fatalf("did not expect regular action failure to trigger gatewayd refresh")
	}
	if shouldRefreshGatewaydForActionResponse(&gatewayv1.ActionResponse{
		Ok: true,
	}) {
		t.Fatalf("did not expect ok response to trigger gatewayd refresh")
	}
}
