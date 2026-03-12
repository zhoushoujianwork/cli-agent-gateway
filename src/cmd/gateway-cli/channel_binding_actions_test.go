package main

import (
	"path/filepath"
	"testing"
	"time"

	"cli-agent-gateway/internal/config"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/storage"
	"cli-agent-gateway/internal/utils/sessionctl"
)

func TestHandleChannelSetEnabledAndList(t *testing.T) {
	store, cfg := newTestStore(t, "dingtalk")

	payload, err := handleChannelList(store, cfg)
	if err != nil {
		t.Fatalf("channel list failed: %v", err)
	}
	if !lookupChannelItem(t, payload, "dingtalk")["enabled"].(bool) {
		t.Fatalf("expected configured channel to default enabled")
	}

	if _, err := handleChannelSetEnabled(store, cfg, &gatewayv1.ActionRequest{
		Action:  "channel.disable",
		Channel: "dingtalk",
	}, false); err != nil {
		t.Fatalf("channel disable failed: %v", err)
	}

	payload, err = handleChannelList(store, cfg)
	if err != nil {
		t.Fatalf("channel list after disable failed: %v", err)
	}
	dingtalk := lookupChannelItem(t, payload, "dingtalk")
	if dingtalk["enabled"].(bool) {
		t.Fatalf("expected dingtalk channel disabled")
	}
	if !dingtalk["configured"].(bool) {
		t.Fatalf("expected dingtalk channel configured")
	}

	if _, err := handleChannelSetEnabled(store, cfg, &gatewayv1.ActionRequest{
		Action:  "channel.disable",
		Channel: "gui",
	}, false); err == nil {
		t.Fatalf("expected gui disable to fail")
	}

	if _, err := handleChannelSetEnabled(store, cfg, &gatewayv1.ActionRequest{
		Action:  "channel.enable",
		Channel: "dingtalk",
	}, true); err != nil {
		t.Fatalf("channel enable failed: %v", err)
	}

	payload, err = handleChannelList(store, cfg)
	if err != nil {
		t.Fatalf("channel list after enable failed: %v", err)
	}
	if !lookupChannelItem(t, payload, "dingtalk")["enabled"].(bool) {
		t.Fatalf("expected dingtalk channel enabled")
	}
}

func TestHandleBindingCreateAndDeleteViaSessionActions(t *testing.T) {
	store, _ := newTestStore(t, "dingtalk")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		st.Sessions["sess-a"] = storage.SessionRecord{
			Key:       "sess-a",
			Workdir:   t.TempDir(),
			Status:    "detached",
			CreatedAt: now,
			UpdatedAt: now,
		}
		st.Sessions["sess-b"] = storage.SessionRecord{
			Key:       "sess-b",
			Workdir:   t.TempDir(),
			Status:    "detached",
			CreatedAt: now,
			UpdatedAt: now,
		}
		st.Unassigned["dingtalk/conv-1"] = storage.ConversationRecord{
			ConversationKey: "dingtalk/conv-1",
			Channel:         "dingtalk",
			ConversationID:  "conv-1",
			UpdatedAt:       now,
		}
		return nil
	}); err != nil {
		t.Fatalf("seed state failed: %v", err)
	}

	payload, err := handleBindingCreate(store, &gatewayv1.ActionRequest{
		Action:         "session.bind",
		SessionKey:     "sess-a",
		Channel:        "dingtalk",
		ConversationId: "conv-1",
	})
	if err != nil {
		t.Fatalf("session bind failed: %v", err)
	}
	if payload["action"] != "session.bind" {
		t.Fatalf("expected action=session.bind, got %v", payload["action"])
	}

	st, err := store.Load()
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if binding := st.Bindings["dingtalk/conv-1"]; binding.SessionKey != "sess-a" {
		t.Fatalf("expected binding to sess-a, got %+v", binding)
	}
	if _, ok := st.Unassigned["dingtalk/conv-1"]; ok {
		t.Fatalf("expected unassigned conversation removed after bind")
	}

	if _, err := handleBindingDelete(store, &gatewayv1.ActionRequest{
		Action:         "session.unbind",
		SessionKey:     "sess-b",
		Channel:        "dingtalk",
		ConversationId: "conv-1",
	}); err == nil {
		t.Fatalf("expected unbind with wrong session to fail")
	}

	payload, err = handleBindingDelete(store, &gatewayv1.ActionRequest{
		Action:         "session.unbind",
		SessionKey:     "sess-a",
		Channel:        "dingtalk",
		ConversationId: "conv-1",
	})
	if err != nil {
		t.Fatalf("session unbind failed: %v", err)
	}
	if payload["action"] != "session.unbind" {
		t.Fatalf("expected action=session.unbind, got %v", payload["action"])
	}
	if payload["session_key"] != "sess-a" {
		t.Fatalf("expected session_key=sess-a, got %v", payload["session_key"])
	}

	st, err = store.Load()
	if err != nil {
		t.Fatalf("load state after unbind failed: %v", err)
	}
	if _, ok := st.Bindings["dingtalk/conv-1"]; ok {
		t.Fatalf("expected binding removed after unbind")
	}
	if _, ok := st.Unassigned["dingtalk/conv-1"]; !ok {
		t.Fatalf("expected conversation returned to unassigned after unbind")
	}
}

func newTestStore(t *testing.T, channel string) (*sessionctl.Store, config.AppConfig) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.AppConfig{
		ChannelType:        channel,
		StorageBackend:     "localfile",
		StateFile:          filepath.Join(dir, "state.json"),
		InteractionLogFile: filepath.Join(dir, "interactions.jsonl"),
		ReportDir:          filepath.Join(dir, "reports"),
		StorageSQLitePath:  filepath.Join(dir, "state.sqlite"),
	}
	store, err := sessionctl.OpenStore(cfg)
	if err != nil {
		t.Fatalf("open store failed: %v", err)
	}
	return store, cfg
}

func lookupChannelItem(t *testing.T, payload map[string]any, channel string) map[string]any {
	t.Helper()
	items, ok := payload["items"].([]map[string]any)
	if ok {
		for _, item := range items {
			if item["channel"] == channel {
				return item
			}
		}
	}
	rawItems, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("items missing or invalid: %#v", payload["items"])
	}
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["channel"] == channel {
			return item
		}
	}
	t.Fatalf("channel %s not found in payload", channel)
	return nil
}
