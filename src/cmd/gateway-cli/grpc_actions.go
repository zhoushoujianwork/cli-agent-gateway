package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/core"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/infra/proclog"
	"cli-agent-gateway/internal/storage"
	"cli-agent-gateway/internal/utils/sessionctl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *gatewayControlServer) getRuntimeManager(root string) *sessionctl.RuntimeManager {
	root = strings.TrimSpace(root)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.managers == nil {
		s.managers = map[string]*sessionctl.RuntimeManager{}
	}
	manager := s.managers[root]
	if manager == nil {
		manager = sessionctl.NewRuntimeManager(root)
		s.managers[root] = manager
	}
	return manager
}

func (s *gatewayControlServer) Action(_ context.Context, req *gatewayv1.ActionRequest) (*gatewayv1.ActionResponse, error) {
	startedAt := time.Now()
	gatewaydLogAction("start", req, nil)
	payload, err := s.handleAction(s.repoRoot, req)
	if err != nil {
		gatewaydLogAction("error", req, map[string]any{
			"elapsed_ms": time.Since(startedAt).Milliseconds(),
			"error":      err.Error(),
		})
		return &gatewayv1.ActionResponse{
			Ok:     false,
			Error:  err.Error(),
			Action: strings.TrimSpace(req.GetAction()),
		}, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		gatewaydLogAction("error", req, map[string]any{
			"elapsed_ms": time.Since(startedAt).Milliseconds(),
			"error":      err.Error(),
		})
		return &gatewayv1.ActionResponse{
			Ok:     false,
			Error:  err.Error(),
			Action: strings.TrimSpace(req.GetAction()),
		}, nil
	}
	extra := map[string]any{
		"elapsed_ms": time.Since(startedAt).Milliseconds(),
		"ok":         payloadOK(payload),
	}
	if status := strings.TrimSpace(fmt.Sprint(payload["status"])); status != "" && status != "<nil>" {
		extra["status"] = status
	}
	if terminalReason := strings.TrimSpace(fmt.Sprint(payload["terminal_reason"])); terminalReason != "" && terminalReason != "<nil>" {
		extra["terminal_reason"] = terminalReason
	}
	if elapsedSec, ok := payload["elapsed_sec"]; ok {
		extra["elapsed_sec"] = elapsedSec
	}
	gatewaydLogAction("done", req, extra)
	return &gatewayv1.ActionResponse{
		Ok:          true,
		Action:      strings.TrimSpace(req.GetAction()),
		PayloadJson: string(raw),
	}, nil
}

func gatewaydLogAction(phase string, req *gatewayv1.ActionRequest, extra map[string]any) {
	fields := map[string]any{
		"event":  "action",
		"phase":  strings.TrimSpace(phase),
		"action": strings.TrimSpace(req.GetAction()),
	}
	if sessionKey := strings.TrimSpace(req.GetSessionKey()); sessionKey != "" {
		fields["session_key"] = sessionKey
	}
	if messageID := strings.TrimSpace(req.GetMessageId()); messageID != "" {
		fields["message_id"] = messageID
	}
	if source := strings.TrimSpace(nonEmpty(req.GetSource(), "session.send")); strings.TrimSpace(req.GetAction()) == "session.send" && source != "" {
		fields["source"] = source
	} else if source := strings.TrimSpace(req.GetSource()); source != "" {
		fields["source"] = source
	}
	if action := strings.TrimSpace(req.GetAction()); action == "session.send" {
		if preview := logTextPreview(req.GetText(), 96); preview != "" {
			fields["text_preview"] = preview
		}
	}
	for key, value := range extra {
		fields[key] = value
	}
	level := "INFO"
	if strings.EqualFold(strings.TrimSpace(phase), "error") {
		level = "ERROR"
	}
	proclog.Log(level, "gatewayd", fields)
}

func gatewaydLogTimeline(req *gatewayv1.ActionRequest, startedAt time.Time, step string, extra map[string]any) {
	fields := map[string]any{
		"event":      "action_timeline",
		"phase":      strings.TrimSpace(step),
		"action":     strings.TrimSpace(req.GetAction()),
		"elapsed_ms": time.Since(startedAt).Milliseconds(),
	}
	if sessionKey := strings.TrimSpace(req.GetSessionKey()); sessionKey != "" {
		fields["session_key"] = sessionKey
	}
	if messageID := strings.TrimSpace(req.GetMessageId()); messageID != "" {
		fields["message_id"] = messageID
	}
	if source := strings.TrimSpace(nonEmpty(req.GetSource(), "session.send")); strings.TrimSpace(req.GetAction()) == "session.send" && source != "" {
		fields["source"] = source
	} else if source := strings.TrimSpace(req.GetSource()); source != "" {
		fields["source"] = source
	}
	for key, value := range extra {
		fields[key] = value
	}
	proclog.Info("gatewayd", fields)
}

func gatewaydLogACPEvents(req *gatewayv1.ActionRequest, startedAt time.Time, events []map[string]any) {
	if len(events) == 0 {
		return
	}
	gatewaydLogTimeline(req, startedAt, "acp_events", map[string]any{"count": len(events)})
	for i, ev := range events {
		method := strings.TrimSpace(fmt.Sprint(ev["method"]))
		stage := "-"
		text := ""
		payloadJSON := ""
		if params, ok := ev["params"].(map[string]any); ok {
			if update, ok := params["update"].(map[string]any); ok {
				if su := strings.TrimSpace(fmt.Sprint(update["sessionUpdate"])); su != "" {
					stage = su
				}
				text = firstNonEmptyString(
					strings.TrimSpace(fmt.Sprint(update["title"])),
					strings.TrimSpace(fmt.Sprint(update["text"])),
					strings.TrimSpace(fmt.Sprint(update["summary"])),
					strings.TrimSpace(fmt.Sprint(update["message"])),
				)
				if text == "" {
					if content, ok := update["content"].(map[string]any); ok {
						text = strings.TrimSpace(fmt.Sprint(content["text"]))
					}
				}
			}
			if stage == "-" {
				if innerMethod := strings.TrimSpace(fmt.Sprint(params["method"])); innerMethod != "" {
					stage = innerMethod
				}
			}
			payloadJSON = truncateLogJSON(params, 16384)
		}
		extra := map[string]any{
			"index":        i + 1,
			"method":       nonEmpty(method, "-"),
			"stage":        stage,
			"payload_json": payloadJSON,
		}
		if strings.TrimSpace(text) != "" {
			extra["text_preview"] = logTextPreview(text, 200)
		}
		gatewaydLogTimeline(req, startedAt, "acp_event", extra)
	}
}

func truncateLogJSON(v any, limit int) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return truncateLogText(fmt.Sprint(v), limit)
	}
	return truncateLogText(string(raw), limit)
}

func truncateLogText(raw string, limit int) string {
	text := strings.TrimSpace(raw)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	suffix := "...(truncated)"
	if limit <= len(suffix) {
		return text[:limit]
	}
	return text[:limit-len(suffix)] + suffix
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func logTextPreview(raw string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "..."
}

func payloadOK(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if ok, present := payload["ok"].(bool); present {
		return ok
	}
	return true
}

func (s *gatewayControlServer) handleAction(root string, req *gatewayv1.ActionRequest) (map[string]any, error) {
	cfg, err := config.Load(root, "")
	if err != nil {
		return nil, err
	}
	store, err := sessionctl.OpenStore(cfg)
	if err != nil {
		return nil, err
	}
	manager := s.getRuntimeManager(root)
	action := strings.TrimSpace(req.GetAction())
	switch action {
	case "session.create":
		return handleSessionCreate(store, req)
	case "session.list":
		return handleSessionList(store, req)
	case "session.show":
		return handleSessionShow(store, req)
	case "session.bind":
		return handleBindingCreate(store, req)
	case "session.unbind":
		return handleBindingDelete(store, req)
	case "session.attach":
		return handleSessionAttach(store, manager, cfg, req)
	case "session.detach":
		return handleSessionDetach(store, manager, req)
	case "session.send":
		return handleSessionSend(store, manager, cfg, req)
	case "session.messages":
		return handleSessionMessages(cfg, store, req)
	case "session.clear":
		return handleSessionClear(store, manager, cfg, req)
	case "session.delete":
		return handleSessionDelete(store, manager, req)
	case "channel.list":
		return handleChannelList(store, cfg)
	case "channel.enable":
		return handleChannelSetEnabled(store, cfg, req, true)
	case "channel.disable":
		return handleChannelSetEnabled(store, cfg, req, false)
	case "channel.inbox":
		return handleChannelInbox(store, req)
	case "channel.show":
		return handleChannelShow(store, req)
	case "binding.create":
		return handleBindingCreate(store, req)
	case "binding.delete":
		return handleBindingDelete(store, req)
	case "binding.list":
		return handleBindingList(store, req)
	case "binding.show":
		return handleBindingShow(store, req)
	case "runtime.status":
		return handleRuntimeStatus(store, manager, root)
	case "runtime.ps":
		return handleRuntimePS(store, manager, req)
	case "runtime.restart":
		return handleRuntimeRestart(store, manager, cfg, req)
	case "runtime.logs":
		return handleRuntimeLogs(root), nil
	default:
		return nil, fmt.Errorf("unsupported action: %s", action)
	}
}

func handleSessionCreate(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	key := sessionctl.NormalizeSessionKey(req.GetSessionKey())
	if key == "" {
		return nil, fmt.Errorf("session key required")
	}
	workdir := strings.TrimSpace(req.GetWorkdir())
	if strings.TrimSpace(workdir) == "" {
		return nil, fmt.Errorf("workdir required")
	}
	if !filepath.IsAbs(workdir) {
		if abs, err := filepath.Abs(workdir); err == nil {
			workdir = abs
		}
	}
	info, err := os.Stat(workdir)
	if err != nil {
		return nil, fmt.Errorf("invalid workdir: %s", workdir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("invalid workdir (not a directory): %s", workdir)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[key]
		if rec.Key == "" {
			rec.Key = key
			rec.CreatedAt = now
		}
		rec.Workdir = workdir
		rec.Status = "detached"
		rec.ArchivedAt = ""
		rec.UpdatedAt = now
		st.Sessions[key] = rec
		if st.RuntimeIndex[key].SessionKey == "" {
			st.RuntimeIndex[key] = storage.RuntimeRecord{
				SessionKey: key,
				Attached:   false,
				Status:     "detached",
				UpdatedAt:  now,
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"action":      "session.create",
		"session_key": key,
		"workdir":     workdir,
		"status":      "detached",
		"updated_at":  now,
	}, nil
}

func handleSessionList(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(st.Sessions))
	for _, rec := range st.Sessions {
		if strings.TrimSpace(rec.Key) == "" {
			continue
		}
		if !req.GetIncludeArchived() && strings.EqualFold(strings.TrimSpace(rec.Status), "archived") {
			continue
		}
		rt := st.RuntimeIndex[rec.Key]
		items = append(items, map[string]any{
			"session_key": rec.Key,
			"workdir":     rec.Workdir,
			"status":      rec.Status,
			"created_at":  rec.CreatedAt,
			"updated_at":  rec.UpdatedAt,
			"archived_at": rec.ArchivedAt,
			"attached":    rt.Attached,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(items[i]["updated_at"])) > strings.TrimSpace(fmt.Sprint(items[j]["updated_at"]))
	})
	return map[string]any{"ok": true, "action": "session.list", "items": items}, nil
}

func handleSessionShow(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, session, err := loadSessionByKey(store, req.GetSessionKey(), true)
	if err != nil {
		return nil, err
	}
	bindings := make([]map[string]any, 0)
	for _, binding := range st.Bindings {
		if binding.SessionKey != session.Key {
			continue
		}
		bindings = append(bindings, bindingToMap(binding))
	}
	sort.Slice(bindings, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(bindings[i]["conversation_key"])) < strings.TrimSpace(fmt.Sprint(bindings[j]["conversation_key"]))
	})
	rt := st.RuntimeIndex[session.Key]
	return map[string]any{
		"ok":          true,
		"action":      "session.show",
		"session_key": session.Key,
		"workdir":     session.Workdir,
		"status":      session.Status,
		"created_at":  session.CreatedAt,
		"updated_at":  session.UpdatedAt,
		"archived_at": session.ArchivedAt,
		"runtime": map[string]any{
			"attached":          rt.Attached,
			"status":            rt.Status,
			"updated_at":        rt.UpdatedAt,
			"last_attached_at":  rt.LastAttachedAt,
			"last_detached_at":  rt.LastDetachedAt,
			"last_recovered_at": rt.LastRecoveredAt,
		},
		"bindings": bindings,
	}, nil
}

func handleSessionAttach(store *sessionctl.Store, manager *sessionctl.RuntimeManager, cfg config.AppConfig, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), false)
	if err != nil {
		return nil, err
	}
	runtimeRecord, err := manager.Attach(cfg, session, nonEmpty(req.GetSource(), "session.attach"), nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		rec.Status = "attached"
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		runtimeRecord.UpdatedAt = now
		st.RuntimeIndex[session.Key] = runtimeRecord
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"action":      "session.attach",
		"session_key": session.Key,
		"status":      "attached",
		"attached":    true,
		"updated_at":  now,
	}, nil
}

func handleSessionDetach(store *sessionctl.Store, manager *sessionctl.RuntimeManager, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), true)
	if err != nil {
		return nil, err
	}
	if err := manager.Detach(session.Key); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		if !strings.EqualFold(strings.TrimSpace(rec.Status), "archived") {
			rec.Status = "detached"
		}
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		st.RuntimeIndex[session.Key] = storage.RuntimeRecord{
			SessionKey:      session.Key,
			Attached:        false,
			Status:          "detached",
			UpdatedAt:       now,
			LastDetachedAt:  now,
			LastAttachedAt:  st.RuntimeIndex[session.Key].LastAttachedAt,
			LastRecoveredAt: st.RuntimeIndex[session.Key].LastRecoveredAt,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"action":      "session.detach",
		"session_key": session.Key,
		"attached":    false,
		"status":      "detached",
		"updated_at":  now,
	}, nil
}

func handleSessionSend(store *sessionctl.Store, manager *sessionctl.RuntimeManager, cfg config.AppConfig, req *gatewayv1.ActionRequest) (map[string]any, error) {
	startedAt := time.Now()
	gatewaydLogTimeline(req, startedAt, "request_accepted", nil)
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), false)
	if err != nil {
		return nil, err
	}
	gatewaydLogTimeline(req, startedAt, "session_loaded", map[string]any{
		"workdir": session.Workdir,
	})
	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return nil, fmt.Errorf("text required")
	}
	msgID := strings.TrimSpace(req.GetMessageId())
	if msgID == "" {
		msgID = fmt.Sprintf("session-%d", time.Now().UnixMilli())
	}
	source := nonEmpty(req.GetSource(), "session.send")
	if err := sessionctl.AppendSessionMessageRecord(cfg, session.Key, "user-"+msgID, "user", text, "sent", source, msgID); err != nil {
		return nil, err
	}
	gatewaydLogTimeline(req, startedAt, "user_message_persisted", map[string]any{
		"text_preview": logTextPreview(text, 96),
	})
	_ = sessionctl.AppendSessionTraceRecord(cfg, session.Key, msgID, "execute_start", source)
	gatewaydLogTimeline(req, startedAt, "runtime_execute_started", nil)
	taskReq := sessionctl.BuildRuntimeTaskRequest(session, source, text, source, msgID, map[string]any{
		"message_id": msgID,
	})
	eventIndex := 0
	taskReq.EventSink = func(event core.TaskEvent) {
		eventIndex++
		_ = sessionctl.AppendStructuredSessionTraceRecord(cfg, session.Key, msgID, event)
		extra := map[string]any{
			"index":  eventIndex,
			"method": nonEmpty(event.Method, "-"),
			"stage":  nonEmpty(event.Stage, "-"),
		}
		if event.Status != "" {
			extra["status"] = event.Status
		}
		if event.Title != "" {
			extra["title"] = event.Title
		}
		if event.Detail != "" {
			extra["detail"] = logTextPreview(event.Detail, 200)
		}
		if event.PayloadPreview != "" {
			extra["payload_json"] = event.PayloadPreview
		}
		gatewaydLogTimeline(req, startedAt, "acp_event", extra)
	}
	result, runtimeRecord, execErr := manager.Send(cfg, session, taskReq)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	status := "attached"
	if execErr != nil {
		status = "error"
		gatewaydLogTimeline(req, startedAt, "runtime_execute_failed", map[string]any{
			"error": execErr.Error(),
		})
		_ = sessionctl.AppendSessionMessageRecord(cfg, session.Key, "assistant-"+msgID, "system", execErr.Error(), "error", source, msgID)
		_ = sessionctl.AppendSessionTraceRecord(cfg, session.Key, msgID, "execute_error", execErr.Error())
	} else {
		gatewaydLogTimeline(req, startedAt, "runtime_execute_finished", map[string]any{
			"status":          result.Status,
			"terminal_reason": nonEmpty(result.TerminalReason, result.Status),
			"elapsed_sec":     result.ElapsedSec,
		})
		assistantText := strings.TrimSpace(result.OutputText)
		if assistantText == "" {
			assistantText = strings.TrimSpace(result.Summary)
		}
		_ = sessionctl.AppendSessionMessageRecord(cfg, session.Key, "assistant-"+msgID, "assistant", assistantText, nonEmpty(result.Status, "ok"), source, msgID)
		_ = sessionctl.AppendSessionTraceRecord(cfg, session.Key, msgID, "execute_done", nonEmpty(result.TerminalReason, result.Status))
	}
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		rec.Status = "attached"
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		runtimeRecord.UpdatedAt = now
		runtimeRecord.Status = status
		st.RuntimeIndex[session.Key] = runtimeRecord
		return nil
	}); err != nil {
		return nil, err
	}
	gatewaydLogTimeline(req, startedAt, "state_persisted", map[string]any{
		"runtime_status": status,
	})
	payload := map[string]any{
		"ok":              execErr == nil,
		"action":          "session.send",
		"session_key":     session.Key,
		"message_id":      msgID,
		"status":          result.Status,
		"summary":         result.Summary,
		"raw_output":      result.OutputText,
		"elapsed_sec":     result.ElapsedSec,
		"terminal_reason": result.TerminalReason,
	}
	if execErr != nil {
		payload["error"] = execErr.Error()
		return payload, nil
	}
	return payload, nil
}

func handleSessionMessages(cfg config.AppConfig, store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), true)
	if err != nil {
		return nil, err
	}
	msgs, timeline, err := sessionctl.CollectSessionMessages(cfg, session.Key)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"action":      "session.messages",
		"session_key": session.Key,
		"messages":    msgs,
		"timeline":    timeline,
	}, nil
}

func handleSessionClear(store *sessionctl.Store, manager *sessionctl.RuntimeManager, cfg config.AppConfig, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), false)
	if err != nil {
		return nil, err
	}
	runtimeRecord, err := manager.Clear(cfg, session, nonEmpty(req.GetSource(), "session.clear"))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		rec.Status = "attached"
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		runtimeRecord.UpdatedAt = now
		st.RuntimeIndex[session.Key] = runtimeRecord
		return nil
	}); err != nil {
		return nil, err
	}
	_ = sessionctl.AppendSessionTraceRecord(cfg, session.Key, "clear-"+session.Key, "session_cleared", "live context reset")
	return map[string]any{
		"ok":          true,
		"action":      "session.clear",
		"session_key": session.Key,
		"status":      "attached",
		"updated_at":  now,
	}, nil
}

func handleSessionDelete(store *sessionctl.Store, manager *sessionctl.RuntimeManager, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), true)
	if err != nil {
		return nil, err
	}
	if err := manager.Detach(session.Key); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		rec.Status = "archived"
		rec.ArchivedAt = now
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		for key, binding := range st.Bindings {
			if binding.SessionKey == session.Key {
				delete(st.Bindings, key)
			}
		}
		st.RuntimeIndex[session.Key] = storage.RuntimeRecord{
			SessionKey:      session.Key,
			Attached:        false,
			Status:          "archived",
			UpdatedAt:       now,
			LastDetachedAt:  now,
			LastAttachedAt:  st.RuntimeIndex[session.Key].LastAttachedAt,
			LastRecoveredAt: st.RuntimeIndex[session.Key].LastRecoveredAt,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":          true,
		"action":      "session.delete",
		"session_key": session.Key,
		"status":      "archived",
		"archived_at": now,
	}, nil
}

func handleChannelList(store *sessionctl.Store, cfg config.AppConfig) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(knownChannels()))
	for _, channel := range knownChannels() {
		configured := channelConfigured(cfg, channel)
		items = append(items, map[string]any{
			"channel":    channel,
			"configured": configured,
			"enabled":    channelEnabled(st, cfg, channel),
			"manageable": channelManageable(channel),
		})
	}
	return map[string]any{
		"ok":     true,
		"action": "channel.list",
		"items":  items,
	}, nil
}

func handleChannelSetEnabled(store *sessionctl.Store, cfg config.AppConfig, req *gatewayv1.ActionRequest, enabled bool) (map[string]any, error) {
	channel := strings.ToLower(strings.TrimSpace(req.GetChannel()))
	if channel == "" {
		return nil, fmt.Errorf("channel required")
	}
	if !channelManageable(channel) {
		return nil, fmt.Errorf("channel does not support enable/disable: %s", channel)
	}
	if !channelConfigured(cfg, channel) {
		return nil, fmt.Errorf("channel not configured: %s", channel)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		st.ChannelStates[channel] = storage.ChannelStateRecord{
			Channel:   channel,
			Enabled:   enabled,
			UpdatedAt: now,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"action":     requestedAction(req, "channel.update"),
		"channel":    channel,
		"configured": true,
		"enabled":    enabled,
		"updated_at": now,
	}, nil
}

func handleChannelInbox(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	channelFilter := strings.ToLower(strings.TrimSpace(req.GetChannel()))
	items := make([]map[string]any, 0, len(st.Unassigned))
	for _, rec := range st.Unassigned {
		if channelFilter != "" && strings.ToLower(strings.TrimSpace(rec.Channel)) != channelFilter {
			continue
		}
		items = append(items, conversationToMap(rec))
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(items[i]["updated_at"])) > strings.TrimSpace(fmt.Sprint(items[j]["updated_at"]))
	})
	return map[string]any{"ok": true, "action": "channel.inbox", "items": items}, nil
}

func handleChannelShow(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, key, err := loadConversationByRequest(store, req)
	if err != nil {
		return nil, err
	}
	rec, ok := st.Unassigned[key]
	if ok {
		return map[string]any{
			"ok":                true,
			"action":            "channel.show",
			"conversation":      conversationToMap(rec),
			"binding_present":   false,
			"bound_session_key": "",
		}, nil
	}
	binding, ok := st.Bindings[key]
	if !ok {
		return nil, fmt.Errorf("conversation not found: %s", key)
	}
	return map[string]any{
		"ok":                true,
		"action":            "channel.show",
		"binding_present":   true,
		"bound_session_key": binding.SessionKey,
		"conversation": map[string]any{
			"conversation_key": binding.ConversationKey,
			"channel":          binding.Channel,
			"conversation_id":  binding.ConversationID,
			"thread_id":        binding.ThreadID,
			"updated_at":       binding.UpdatedAt,
		},
	}, nil
}

func handleBindingCreate(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), false)
	if err != nil {
		return nil, err
	}
	key := sessionctl.CanonicalConversationKey(req.GetChannel(), req.GetConversationId(), req.GetThreadId())
	if key == "" {
		return nil, fmt.Errorf("channel and conversation id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		st.Bindings[key] = storage.BindingRecord{
			ConversationKey: key,
			Channel:         strings.ToLower(strings.TrimSpace(req.GetChannel())),
			ConversationID:  strings.TrimSpace(req.GetConversationId()),
			ThreadID:        strings.TrimSpace(req.GetThreadId()),
			SessionKey:      session.Key,
			UpdatedAt:       now,
		}
		delete(st.Unassigned, key)
		rec := st.Sessions[session.Key]
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":               true,
		"action":           requestedAction(req, "binding.create"),
		"conversation_key": key,
		"session_key":      session.Key,
		"updated_at":       now,
	}, nil
}

func handleBindingDelete(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	key := sessionctl.CanonicalConversationKey(req.GetChannel(), req.GetConversationId(), req.GetThreadId())
	if key == "" {
		return nil, fmt.Errorf("channel and conversation id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var deleted storage.BindingRecord
	if _, err := store.Mutate(func(st *storage.StateData) error {
		binding, ok := st.Bindings[key]
		if !ok {
			return fmt.Errorf("binding not found: %s", key)
		}
		sessionKey := sessionctl.NormalizeSessionKey(req.GetSessionKey())
		if sessionKey != "" && binding.SessionKey != sessionKey {
			return fmt.Errorf("binding belongs to session %s, not %s", binding.SessionKey, sessionKey)
		}
		deleted = binding
		if rec, ok := st.Unassigned[key]; ok {
			rec.UpdatedAt = now
			st.Unassigned[key] = rec
		} else {
			st.Unassigned[key] = storage.ConversationRecord{
				ConversationKey: binding.ConversationKey,
				Channel:         binding.Channel,
				ConversationID:  binding.ConversationID,
				ThreadID:        binding.ThreadID,
				LastSeenAt:      now,
				UpdatedAt:       now,
			}
		}
		session := st.Sessions[binding.SessionKey]
		if strings.TrimSpace(session.Key) != "" {
			session.UpdatedAt = now
			st.Sessions[binding.SessionKey] = session
		}
		delete(st.Bindings, key)
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":               true,
		"action":           requestedAction(req, "binding.delete"),
		"conversation_key": key,
		"session_key":      deleted.SessionKey,
		"updated_at":       now,
	}, nil
}

func handleBindingList(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(st.Bindings))
	sessionFilter := sessionctl.NormalizeSessionKey(req.GetSessionKey())
	channelFilter := strings.ToLower(strings.TrimSpace(req.GetChannel()))
	for _, binding := range st.Bindings {
		if sessionFilter != "" && binding.SessionKey != sessionFilter {
			continue
		}
		if channelFilter != "" && strings.ToLower(strings.TrimSpace(binding.Channel)) != channelFilter {
			continue
		}
		items = append(items, bindingToMap(binding))
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(items[i]["conversation_key"])) < strings.TrimSpace(fmt.Sprint(items[j]["conversation_key"]))
	})
	return map[string]any{"ok": true, "action": "binding.list", "items": items}, nil
}

func handleBindingShow(store *sessionctl.Store, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, key, err := loadConversationByRequest(store, req)
	if err != nil {
		return nil, err
	}
	binding, ok := st.Bindings[key]
	if !ok {
		return nil, fmt.Errorf("binding not found: %s", key)
	}
	return map[string]any{
		"ok":      true,
		"action":  "binding.show",
		"binding": bindingToMap(binding),
	}, nil
}

func handleRuntimeStatus(store *sessionctl.Store, manager *sessionctl.RuntimeManager, root string) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		return nil, err
	}
	statusPayload, err := getStatusPayload(root)
	if err != nil {
		statusPayload = StatusPayload{
			Running:  false,
			LockFile: strings.TrimSpace(cfg.LockFile),
			Metadata: map[string]any{
				"channel":  strings.TrimSpace(cfg.ChannelType),
				"workdir":  strings.TrimSpace(cfg.Workdir),
				"log_file": strings.TrimSpace(resolveLogPath(root, nil)),
			},
		}
	}
	attached := 0
	for _, rt := range st.RuntimeIndex {
		if rt.Attached {
			attached++
		}
	}
	payload := statusJSON("runtime.status", statusPayload, cfg, sessionctl.ResolveRuntimeLogPath(root))
	payload["attached_count"] = attached
	payload["runtime_count"] = len(manager.Snapshot())
	payload["session_count"] = len(st.Sessions)
	payload["binding_count"] = len(st.Bindings)
	payload["unassigned_count"] = len(st.Unassigned)
	return payload, nil
}

func handleRuntimePS(store *sessionctl.Store, manager *sessionctl.RuntimeManager, req *gatewayv1.ActionRequest) (map[string]any, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	sessionFilter := sessionctl.NormalizeSessionKey(req.GetSessionKey())
	items := make([]map[string]any, 0)
	for _, rt := range st.RuntimeIndex {
		if sessionFilter != "" && rt.SessionKey != sessionFilter {
			continue
		}
		if !rt.Attached && !req.GetIncludeArchived() {
			continue
		}
		items = append(items, map[string]any{
			"session_key":       rt.SessionKey,
			"attached":          rt.Attached,
			"status":            rt.Status,
			"updated_at":        rt.UpdatedAt,
			"last_attached_at":  rt.LastAttachedAt,
			"last_detached_at":  rt.LastDetachedAt,
			"last_recovered_at": rt.LastRecoveredAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.TrimSpace(fmt.Sprint(items[i]["session_key"])) < strings.TrimSpace(fmt.Sprint(items[j]["session_key"]))
	})
	return map[string]any{
		"ok":     true,
		"action": "runtime.ps",
		"items":  items,
	}, nil
}

func handleRuntimeRestart(store *sessionctl.Store, manager *sessionctl.RuntimeManager, cfg config.AppConfig, req *gatewayv1.ActionRequest) (map[string]any, error) {
	_, session, err := loadSessionByKey(store, req.GetSessionKey(), false)
	if err != nil {
		return nil, err
	}
	if err := manager.Detach(session.Key); err != nil {
		return nil, err
	}
	runtimeRecord, err := manager.Attach(cfg, session, nonEmpty(req.GetSource(), "runtime.restart"), map[string]any{
		"restart": true,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.Mutate(func(st *storage.StateData) error {
		rec := st.Sessions[session.Key]
		rec.Status = "attached"
		rec.UpdatedAt = now
		st.Sessions[session.Key] = rec
		runtimeRecord.UpdatedAt = now
		runtimeRecord.Status = "attached"
		st.RuntimeIndex[session.Key] = runtimeRecord
		return nil
	}); err != nil {
		return nil, err
	}
	_ = sessionctl.AppendSessionTraceRecord(cfg, session.Key, "restart-"+session.Key, "runtime_restarted", "runtime detached and attached")
	return map[string]any{
		"ok":          true,
		"action":      "runtime.restart",
		"session_key": session.Key,
		"status":      "attached",
		"attached":    true,
		"updated_at":  now,
	}, nil
}

func handleRuntimeLogs(root string) map[string]any {
	return map[string]any{
		"ok":       true,
		"action":   "runtime.logs",
		"log_file": sessionctl.ResolveRuntimeLogPath(root),
	}
}

func knownChannels() []string {
	return []string{"gui", "command", "dingtalk", "imessage"}
}

func channelManageable(channel string) bool {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "command", "dingtalk", "imessage":
		return true
	default:
		return false
	}
}

func channelConfigured(cfg config.AppConfig, channel string) bool {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "gui" {
		return true
	}
	return channel != "" && strings.EqualFold(strings.TrimSpace(cfg.ChannelType), channel)
}

func channelEnabled(st storage.StateData, cfg config.AppConfig, channel string) bool {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if !channelConfigured(cfg, channel) {
		return channel == "gui"
	}
	if !channelManageable(channel) {
		return true
	}
	rec, ok := st.ChannelStates[channel]
	if !ok {
		return true
	}
	return rec.Enabled
}

func requestedAction(req *gatewayv1.ActionRequest, fallback string) string {
	if req == nil {
		return fallback
	}
	if action := strings.TrimSpace(req.GetAction()); action != "" {
		return action
	}
	return fallback
}

func loadSessionByKey(store *sessionctl.Store, key string, allowArchived bool) (storage.StateData, storage.SessionRecord, error) {
	st, err := store.Load()
	if err != nil {
		return storage.StateData{}, storage.SessionRecord{}, err
	}
	key = sessionctl.NormalizeSessionKey(key)
	if key == "" {
		return storage.StateData{}, storage.SessionRecord{}, fmt.Errorf("session key required")
	}
	rec := st.Sessions[key]
	if strings.TrimSpace(rec.Key) == "" {
		return storage.StateData{}, storage.SessionRecord{}, fmt.Errorf("session not found: %s", key)
	}
	if !allowArchived && strings.EqualFold(strings.TrimSpace(rec.Status), "archived") {
		return storage.StateData{}, storage.SessionRecord{}, fmt.Errorf("session archived: %s", key)
	}
	return st, rec, nil
}

func loadConversationByRequest(store *sessionctl.Store, req *gatewayv1.ActionRequest) (storage.StateData, string, error) {
	st, err := store.Load()
	if err != nil {
		return storage.StateData{}, "", err
	}
	key := sessionctl.CanonicalConversationKey(req.GetChannel(), req.GetConversationId(), req.GetThreadId())
	if key == "" {
		return storage.StateData{}, "", fmt.Errorf("channel and conversation id required")
	}
	return st, key, nil
}

func conversationToMap(rec storage.ConversationRecord) map[string]any {
	return map[string]any{
		"conversation_key":   rec.ConversationKey,
		"channel":            rec.Channel,
		"conversation_id":    rec.ConversationID,
		"thread_id":          rec.ThreadID,
		"conversation_title": rec.ConversationTitle,
		"last_message_id":    rec.LastMessageID,
		"last_text":          rec.LastText,
		"last_sender":        rec.LastSender,
		"last_seen_at":       rec.LastSeenAt,
		"updated_at":         rec.UpdatedAt,
		"metadata":           rec.Metadata,
	}
}

func bindingToMap(binding storage.BindingRecord) map[string]any {
	return map[string]any{
		"conversation_key": binding.ConversationKey,
		"channel":          binding.Channel,
		"conversation_id":  binding.ConversationID,
		"thread_id":        binding.ThreadID,
		"session_key":      binding.SessionKey,
		"updated_at":       binding.UpdatedAt,
	}
}

func tryActionViaGRPC(repoRoot string, req *gatewayv1.ActionRequest) (*gatewayv1.ActionResponse, error) {
	cli, conn, err := grpcGatewayClient(repoRoot)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), sendViaSessionGRPCTimeout(repoRoot))
	defer cancel()
	resp, err := cli.Action(ctx, req)
	if err == nil && !shouldRefreshGatewaydForActionResponse(resp) {
		return resp, nil
	}
	if err != nil && !shouldRefreshGatewaydForAction(err) {
		return nil, err
	}
	if _, shutdownErr := shutdownManagedGatewayd(repoRoot); shutdownErr != nil {
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	if ensureErr := ensureGatewaydRunning(repoRoot); ensureErr != nil {
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	cli, conn, dialErr := grpcGatewayClient(repoRoot)
	if dialErr != nil {
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	defer conn.Close()
	ctx, cancel = context.WithTimeout(context.Background(), sendViaSessionGRPCTimeout(repoRoot))
	defer cancel()
	return cli.Action(ctx, req)
}

func shouldRefreshGatewaydForAction(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	if st.Code() != codes.Unimplemented {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(st.Message()))
	return strings.Contains(msg, "unknown method action") || strings.Contains(msg, "/action")
}

func shouldRefreshGatewaydForActionResponse(resp *gatewayv1.ActionResponse) bool {
	if resp == nil || resp.GetOk() {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(resp.GetError()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "unsupported action:")
}

func decodeActionPayload(resp *gatewayv1.ActionResponse) (map[string]any, error) {
	if resp == nil {
		return nil, fmt.Errorf("empty response")
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("%s", strings.TrimSpace(resp.GetError()))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.GetPayloadJson()), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func runActionLocal(repoRoot string, req *gatewayv1.ActionRequest) (map[string]any, error) {
	server := &gatewayControlServer{
		repoRoot: repoRoot,
		managers: map[string]*sessionctl.RuntimeManager{},
	}
	return server.handleAction(repoRoot, req)
}
