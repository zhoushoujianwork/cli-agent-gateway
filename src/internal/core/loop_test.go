package core

import (
	"path/filepath"
	"testing"

	"cli-agent-gateway/internal/storage"
)

func TestAccessDecisionPendingUnknownUsers(t *testing.T) {
	loop := &Loop{
		AllowedFrom:         map[string]struct{}{},
		PendingUnknownUsers: true,
	}
	allowed, status := loop.accessDecision(storage.StateData{}, InboundMessage{
		Channel: "dingtalk",
		Sender:  "user-1",
	})
	if allowed {
		t.Fatalf("expected pending user to be blocked")
	}
	if status != "pending" {
		t.Fatalf("expected pending status, got %q", status)
	}
}

func TestAccessDecisionAllowAllUnknownUsers(t *testing.T) {
	loop := &Loop{
		AllowedFrom:         map[string]struct{}{},
		PendingUnknownUsers: false,
	}
	allowed, status := loop.accessDecision(storage.StateData{}, InboundMessage{
		Channel: "dingtalk",
		Sender:  "user-1",
	})
	if !allowed {
		t.Fatalf("expected allow_all flow to accept user")
	}
	if status != "allowed" {
		t.Fatalf("expected allowed status, got %q", status)
	}
}

func TestAccessDecisionStoredUserStatusOverridesEnv(t *testing.T) {
	st := storage.StateData{
		UserAccess: map[string]storage.UserAccessRecord{
			"dingtalk|user-1": {
				Channel: "dingtalk",
				UserID:  "user-1",
				Status:  "blocked",
			},
			"dingtalk|user-2": {
				Channel: "dingtalk",
				UserID:  "user-2",
				Status:  "allowed",
			},
		},
	}
	loop := &Loop{
		AllowedFrom: map[string]struct{}{
			"user-1": {},
		},
		PendingUnknownUsers: true,
	}

	if allowed, status := loop.accessDecision(st, InboundMessage{Channel: "dingtalk", Sender: "user-1"}); allowed || status != "blocked" {
		t.Fatalf("expected blocked stored status to win, got allowed=%v status=%q", allowed, status)
	}
	if allowed, status := loop.accessDecision(st, InboundMessage{Channel: "dingtalk", Sender: "user-2"}); !allowed || status != "allowed" {
		t.Fatalf("expected allowed stored status to win, got allowed=%v status=%q", allowed, status)
	}
}

func TestEnsureSessionWorkdirUsesDefaultWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	loop := &Loop{}
	st := storage.StateData{
		SessionMeta: map[string]storage.SessionMetaRecord{},
	}
	next, workdir, err := loop.ensureSessionWorkdir(st, "sess-a")
	if err != nil {
		t.Fatalf("ensureSessionWorkdir failed: %v", err)
	}
	want := filepath.Join(home, ".cag", "workspace", "default")
	if filepath.Clean(workdir) != filepath.Clean(want) {
		t.Fatalf("expected default workdir=%s got=%s", want, workdir)
	}
	meta := next.SessionMeta["sess-a"]
	if filepath.Clean(meta.Workdir) != filepath.Clean(want) {
		t.Fatalf("expected session meta workdir=%s got=%s", want, meta.Workdir)
	}
}

func TestEnsureSessionWorkdirKeepsExistingValue(t *testing.T) {
	loop := &Loop{}
	custom := filepath.Join(t.TempDir(), "custom-workdir")
	st := storage.StateData{
		SessionMeta: map[string]storage.SessionMetaRecord{
			"sess-b": {
				Workdir: custom,
				Status:  "ready",
			},
		},
	}
	next, workdir, err := loop.ensureSessionWorkdir(st, "sess-b")
	if err != nil {
		t.Fatalf("ensureSessionWorkdir failed: %v", err)
	}
	if filepath.Clean(workdir) != filepath.Clean(custom) {
		t.Fatalf("expected existing workdir=%s got=%s", custom, workdir)
	}
	if filepath.Clean(next.SessionMeta["sess-b"].Workdir) != filepath.Clean(custom) {
		t.Fatalf("expected stored workdir=%s got=%s", custom, next.SessionMeta["sess-b"].Workdir)
	}
}
