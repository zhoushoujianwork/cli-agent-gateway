package core

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cli-agent-gateway/internal/infra/proclog"
	"cli-agent-gateway/internal/storage"
)

type Loop struct {
	Channel             ChannelAdapter
	ChannelName         string
	Agent               AgentAdapter
	Storage             storage.Backend
	RemoteUserID        string
	AllowedFrom         map[string]struct{}
	PendingUnknownUsers bool
	ProcessOnlyLatest   bool
	PollIntervalSec     int
	ReplyStyleEnabled   bool
	ReplyStylePrompt    string

	channelStateSeen bool
	channelEnabled   bool
	fetchErrorActive bool
	lastFetchError   string
}

func loopLogInfo(event string, fields map[string]any) {
	node := map[string]any{
		"event": strings.TrimSpace(event),
	}
	for key, value := range fields {
		node[key] = value
	}
	proclog.Info("cli", node)
}

func loopLogWarn(event string, fields map[string]any) {
	node := map[string]any{
		"event": strings.TrimSpace(event),
	}
	for key, value := range fields {
		node[key] = value
	}
	proclog.Warn("cli", node)
}

func (l *Loop) RunForever() error {
	st, err := l.Storage.LoadState()
	if err != nil {
		return err
	}
	processed := map[string]struct{}{}
	for _, id := range st.ProcessedIDs {
		processed[id] = struct{}{}
	}
	for {
		if latest, loadErr := l.Storage.LoadState(); loadErr == nil {
			st = latest
			processed = map[string]struct{}{}
			for _, id := range st.ProcessedIDs {
				processed[id] = struct{}{}
			}
		} else {
			loopLogWarn("state_reload_failed", map[string]any{"err": loadErr.Error()})
		}
		if enabled := l.channelIngressEnabled(st); !enabled {
			if event, ok := l.channelStateTransitionEvent(false); ok {
				loopLogInfo(event, map[string]any{"channel": nonEmpty(l.ChannelName, "command")})
			}
			time.Sleep(time.Duration(l.PollIntervalSec) * time.Second)
			continue
		} else {
			if event, ok := l.channelStateTransitionEvent(true); ok {
				loopLogInfo(event, map[string]any{"channel": nonEmpty(l.ChannelName, "command")})
			}
		}
		msgs, err := l.Channel.Fetch()
		if err != nil {
			if l.shouldLogFetchFailure(err) {
				loopLogWarn("fetch_failed", map[string]any{"err": err.Error()})
			}
			time.Sleep(time.Duration(l.PollIntervalSec) * time.Second)
			continue
		}
		l.noteFetchSuccess()
		if len(msgs) > 0 {
			loopLogInfo("fetch_ok", map[string]any{"count": len(msgs)})
		}
		if l.ProcessOnlyLatest && len(msgs) > 1 {
			loopLogInfo("process_only_latest", map[string]any{"keep": 1, "drop": len(msgs) - 1})
			msgs = msgs[len(msgs)-1:]
		}
		for _, m := range msgs {
			if _, ok := processed[m.ID]; ok {
				loopLogInfo("skip_duplicate", map[string]any{"msg_id": m.ID, "sender": m.Sender})
				continue
			}
			allowed, accessStatus := l.accessDecision(st, m)
			st = l.upsertUserAccess(st, m, accessStatus)
			if !allowed {
				l.saveState(st)
				loopLogInfo("skip_unauthorized", map[string]any{"msg_id": m.ID, "sender": m.Sender, "status": accessStatus})
				l.appendInteraction(map[string]any{
					"kind":         "unauthorized_inbound",
					"msg_id":       m.ID,
					"sender":       m.Sender,
					"text":         m.Text,
					"time":         time.Now().UTC().Format(time.RFC3339),
					"user_profile": buildUserProfile(m),
					"status":       accessStatus,
				})
				if err := l.Channel.Send(l.unauthorizedNotice(m, accessStatus), m.Sender, "unauthorized-"+m.ID, ""); err != nil {
					loopLogWarn("send_unauthorized_notice_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error()})
				} else {
					loopLogInfo("send_unauthorized_notice_ok", map[string]any{"msg_id": m.ID, "to": m.Sender, "status": accessStatus})
				}
				continue
			}
			loopLogInfo("inbound_accepted", map[string]any{
				"msg_id":  m.ID,
				"sender":  m.Sender,
				"channel": nonEmpty(m.Channel, "command"),
				"thread":  nonEmpty(m.ThreadID, "-"),
				"text":    shortText(m.Text, 80),
			})
			now := time.Now().UTC().Format(time.RFC3339)
			l.appendInteraction(map[string]any{
				"kind":         "inbound_received",
				"msg_id":       m.ID,
				"sender":       m.Sender,
				"text":         m.Text,
				"time":         now,
				"user_profile": buildUserProfile(m),
			})

			sessionKey, convKey := l.boundSessionForInbound(st, m)
			if strings.TrimSpace(sessionKey) == "" {
				st = l.recordUnassignedConversation(st, m, convKey)
				l.saveState(st)
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				if err := l.Channel.Send("当前对话尚未绑定到本地 session，请先在 GUI/CLI 完成绑定。", m.Sender, "unbound-"+m.ID, ""); err != nil {
					loopLogWarn("send_unbound_notice_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error()})
				} else {
					loopLogInfo("send_unbound_notice_ok", map[string]any{"msg_id": m.ID, "conversation": convKey})
				}
				l.appendInteraction(map[string]any{
					"kind":             "channel_unassigned",
					"msg_id":           m.ID,
					"conversation_key": convKey,
					"text":             m.Text,
					"sender":           m.Sender,
					"ts":               time.Now().UTC().Format(time.RFC3339),
				})
				continue
			}

			if err := l.Channel.Send("已收到，正在处理", m.Sender, "ack-"+m.ID, ""); err != nil {
				loopLogWarn("send_ack_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error()})
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_ack_failed",
					"msg_id": m.ID,
					"to":     m.Sender,
					"error":  err.Error(),
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			} else {
				loopLogInfo("send_ack_ok", map[string]any{"msg_id": m.ID, "to": m.Sender})
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_ack_ok",
					"msg_id": m.ID,
					"to":     m.Sender,
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			}

			userText := strings.TrimSpace(m.Text)
			if l.ReplyStyleEnabled && strings.TrimSpace(l.ReplyStylePrompt) != "" {
				userText = l.ReplyStylePrompt + "\n\n用户请求：\n" + userText
			}
			req := TaskRequest{
				TraceID:    traceID(m.ID),
				SessionKey: sessionKey,
				UserText:   userText,
				Sender:     m.Sender,
				Channel:    nonEmpty(m.Channel, "command"),
				ThreadID:   m.ThreadID,
				Metadata: mergeMetadata(m.Metadata, map[string]any{
					"received_ts":      m.TS,
					"message_id":       m.ID,
					"conversation_key": convKey,
				}),
			}
			loopLogInfo("session_resolved", map[string]any{"msg_id": m.ID, "session_key": sessionKey, "conversation": convKey})
			l.appendInteraction(map[string]any{
				"kind":             "trace",
				"stage":            "session_resolved",
				"msg_id":           m.ID,
				"session_key":      sessionKey,
				"conversation_key": convKey,
				"ts":               now,
			})
			loopLogInfo("execute_start", map[string]any{"msg_id": m.ID, "session_key": sessionKey, "sender": m.Sender})
			l.appendInteraction(map[string]any{
				"kind":     "trace",
				"stage":    "execute_start",
				"msg_id":   m.ID,
				"trace_id": req.TraceID,
				"ts":       time.Now().UTC().Format(time.RFC3339),
			})
			type execResult struct {
				result TaskResult
				err    error
			}
			execCh := make(chan execResult, 1)
			go func() {
				r, e := l.Agent.Execute(req)
				execCh <- execResult{result: r, err: e}
			}()
			var (
				result  TaskResult
				execErr error
			)
			progressEvery := progressIntervalSec()
			var ticker *time.Ticker
			var tickC <-chan time.Time
			if progressEvery > 0 {
				ticker = time.NewTicker(time.Duration(progressEvery) * time.Second)
				tickC = ticker.C
			}
			progressCount := 0
			for {
				select {
				case out := <-execCh:
					result = out.result
					execErr = out.err
					goto EXEC_DONE
				case <-tickC:
					progressCount++
					progressText := fmt.Sprintf("处理中，已等待 %ds", progressCount*progressEvery)
					progressID := fmt.Sprintf("progress-%s-%d", m.ID, progressCount)
					if err := l.Channel.Send(progressText, m.Sender, progressID, ""); err != nil {
						loopLogWarn("send_progress_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error(), "index": progressCount})
						l.appendInteraction(map[string]any{
							"kind":   "trace",
							"stage":  "send_progress_failed",
							"msg_id": m.ID,
							"to":     m.Sender,
							"error":  err.Error(),
							"index":  progressCount,
							"ts":     time.Now().UTC().Format(time.RFC3339),
						})
					} else {
						loopLogInfo("send_progress_ok", map[string]any{"msg_id": m.ID, "to": m.Sender, "index": progressCount})
						l.appendInteraction(map[string]any{
							"kind":   "trace",
							"stage":  "send_progress_ok",
							"msg_id": m.ID,
							"to":     m.Sender,
							"index":  progressCount,
							"ts":     time.Now().UTC().Format(time.RFC3339),
						})
					}
				}
			}
		EXEC_DONE:
			if ticker != nil {
				ticker.Stop()
			}
			if execErr != nil {
				errText := fmt.Sprintf("执行失败: %v", execErr)
				if err := l.Channel.Send(errText, m.Sender, m.ID, ""); err != nil {
					loopLogWarn("send_error_reply_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error()})
				} else {
					loopLogInfo("send_error_reply_ok", map[string]any{"msg_id": m.ID, "to": m.Sender})
				}
				l.appendInteraction(map[string]any{
					"msg_id":       m.ID,
					"error":        errText,
					"ts":           time.Now().UTC().Format(time.RFC3339),
					"user_profile": buildUserProfile(m),
				})
				loopLogWarn("execute_failed", map[string]any{"msg_id": m.ID, "err": execErr.Error()})
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				l.saveState(st)
				continue
			}
			loopLogInfo("execute_done", map[string]any{"msg_id": m.ID, "status": result.Status, "elapsed_sec": result.ElapsedSec})
			l.logACPEvents(m.ID, result.RawEvents)

			reportPath := l.writeReport(m, req, result)
			loopLogInfo("report_written", map[string]any{"msg_id": m.ID, "path": nonEmpty(reportPath, "-")})
			finalText := formatFinal(result)
			if err := l.Channel.Send(finalText, m.Sender, m.ID, reportPath); err != nil {
				loopLogWarn("send_final_failed", map[string]any{"msg_id": m.ID, "to": m.Sender, "err": err.Error()})
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_final_failed",
					"msg_id": m.ID,
					"to":     m.Sender,
					"error":  err.Error(),
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			} else {
				loopLogInfo("send_final_ok", map[string]any{"msg_id": m.ID, "to": m.Sender})
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_final_ok",
					"msg_id": m.ID,
					"to":     m.Sender,
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			}
			l.appendInteraction(map[string]any{
				"msg_id":       m.ID,
				"sender":       m.Sender,
				"text":         m.Text,
				"trace_id":     req.TraceID,
				"result":       result.Summary,
				"status":       result.Status,
				"elapsed_sec":  result.ElapsedSec,
				"ts":           time.Now().UTC().Format(time.RFC3339),
				"user_profile": buildUserProfile(m),
			})

			processed[m.ID] = struct{}{}
			st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
			l.saveState(st)
			loopLogInfo("persist_done", map[string]any{"msg_id": m.ID, "processed_total": len(st.ProcessedIDs)})
		}
		time.Sleep(time.Duration(l.PollIntervalSec) * time.Second)
	}
}

func (l *Loop) accessDecision(st storage.StateData, m InboundMessage) (bool, string) {
	sender := strings.TrimSpace(m.Sender)
	if sender == "" {
		return false, "pending"
	}
	key := userAccessKey(nonEmpty(m.Channel, "command"), sender)
	if rec, ok := st.UserAccess[key]; ok {
		switch strings.ToLower(strings.TrimSpace(rec.Status)) {
		case "allowed":
			return true, "allowed"
		case "blocked":
			return false, "blocked"
		}
	}
	if len(l.AllowedFrom) == 0 {
		if strings.TrimSpace(l.RemoteUserID) == "" {
			if l.PendingUnknownUsers {
				return false, "pending"
			}
			return true, "allowed"
		}
		if sender == strings.TrimSpace(l.RemoteUserID) {
			return true, "allowed"
		}
		return false, "pending"
	}
	if _, ok := l.AllowedFrom[sender]; ok {
		return true, "allowed"
	}
	return false, "pending"
}

func (l *Loop) boundSessionForInbound(st storage.StateData, m InboundMessage) (string, string) {
	channel := strings.ToLower(strings.TrimSpace(nonEmpty(m.Channel, "command")))
	conversationID := strings.TrimSpace(anyString(m.Metadata["conversation_id"]))
	if conversationID == "" {
		conversationID = strings.TrimSpace(m.Sender)
	}
	threadID := strings.TrimSpace(anyString(m.Metadata["thread_id"]))
	if threadID == "" {
		threadID = parseThreadID(conversationID, strings.TrimSpace(m.ThreadID))
	}
	key := canonicalConversationKey(channel, conversationID, threadID)
	if key == "" {
		return "", ""
	}
	binding, ok := st.Bindings[key]
	if !ok {
		return "", key
	}
	return strings.TrimSpace(binding.SessionKey), key
}

func (l *Loop) recordUnassignedConversation(st storage.StateData, m InboundMessage, conversationKey string) storage.StateData {
	if st.Unassigned == nil {
		st.Unassigned = map[string]storage.ConversationRecord{}
	}
	channel := strings.ToLower(strings.TrimSpace(nonEmpty(m.Channel, "command")))
	conversationID := strings.TrimSpace(anyString(m.Metadata["conversation_id"]))
	if conversationID == "" {
		conversationID = strings.TrimSpace(m.Sender)
	}
	threadID := strings.TrimSpace(anyString(m.Metadata["thread_id"]))
	if threadID == "" {
		threadID = parseThreadID(conversationID, strings.TrimSpace(m.ThreadID))
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	st.Unassigned[conversationKey] = storage.ConversationRecord{
		ConversationKey:   conversationKey,
		Channel:           channel,
		ConversationID:    conversationID,
		ThreadID:          threadID,
		ConversationTitle: strings.TrimSpace(anyString(m.Metadata["conversation_title"])),
		LastMessageID:     strings.TrimSpace(m.ID),
		LastText:          strings.TrimSpace(m.Text),
		LastSender:        strings.TrimSpace(m.Sender),
		LastSeenAt:        nonEmpty(strings.TrimSpace(m.TS), now),
		UpdatedAt:         now,
		Metadata:          buildUserProfile(m),
	}
	return st
}

func (l *Loop) sessionKeyFor(m InboundMessage) string {
	thread := strings.TrimSpace(m.ThreadID)
	if thread == "" {
		thread = "-"
	}
	sig := nonEmpty(m.Channel, "command") + "|" + m.Sender + "|" + thread
	h := sha1.Sum([]byte(sig))
	return "sess_" + hex.EncodeToString(h[:])[:24]
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
	h := sha1.Sum([]byte(base + "|" + cutoff))
	return base + "_r" + hex.EncodeToString(h[:])[:8]
}

func (l *Loop) writeReport(msg InboundMessage, req TaskRequest, result TaskResult) string {
	node := map[string]any{
		"message": msg,
		"request": req,
		"result":  result,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	}
	path, err := l.Storage.WriteReport(node, msg.ID)
	if err != nil {
		return ""
	}
	return path
}

func formatFinal(result TaskResult) string {
	status := "✅ 完成"
	if result.Status == "error" {
		status = "❌ 失败"
	}
	if result.Status == "timeout" {
		status = "⏱️ 超时"
	}
	return fmt.Sprintf("[%s]\n耗时: %ds\n\n%s", status, result.ElapsedSec, strings.TrimSpace(result.Summary))
}

func traceID(msgID string) string {
	if strings.TrimSpace(msgID) == "" {
		msgID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	if len(msgID) <= 8 {
		return msgID
	}
	return msgID[:8]
}

func nonEmpty(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func (l *Loop) appendInteraction(node map[string]any) {
	if err := l.Storage.AppendInteraction(node); err != nil {
		loopLogWarn("append_interaction_failed", map[string]any{"err": err.Error()})
	}
}

func (l *Loop) saveState(st storage.StateData) {
	if err := l.Storage.SaveState(st); err != nil {
		loopLogWarn("save_state_failed", map[string]any{"err": err.Error()})
	}
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func buildUserProfile(m InboundMessage) map[string]any {
	profile := map[string]any{
		"channel":     nonEmpty(m.Channel, "command"),
		"sender":      m.Sender,
		"thread_id":   m.ThreadID,
		"sender_name": m.Sender,
	}
	if name := strings.TrimSpace(anyString(m.Metadata["sender_name"])); name != "" {
		profile["sender_name"] = name
	}
	for _, key := range []string{
		"sender_staff_id",
		"sender_id",
		"sender_corp_id",
		"is_admin",
		"conversation_id",
		"conversation_title",
		"chat_type",
		"is_group",
		"is_at_bot",
		"sender_profile",
		"sender_province",
	} {
		if v, ok := m.Metadata[key]; ok {
			profile[key] = v
		}
	}
	return profile
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func canonicalConversationKey(channel, conversationID, threadID string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	conversationID = strings.TrimSpace(conversationID)
	threadID = strings.TrimSpace(threadID)
	if channel == "" || conversationID == "" {
		return ""
	}
	if threadID == "" {
		return channel + "/" + conversationID
	}
	return channel + "/" + conversationID + "/" + threadID
}

func parseThreadID(conversationID, threadID string) string {
	conversationID = strings.TrimSpace(conversationID)
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || threadID == conversationID {
		return ""
	}
	if strings.HasPrefix(threadID, conversationID+":") {
		return strings.TrimPrefix(threadID, conversationID+":")
	}
	return threadID
}

func userAccessKey(channel, userID string) string {
	return strings.TrimSpace(nonEmpty(channel, "command")) + "|" + strings.TrimSpace(userID)
}

func userSenderName(m InboundMessage) string {
	if name := strings.TrimSpace(anyString(m.Metadata["sender_name"])); name != "" {
		return name
	}
	return strings.TrimSpace(m.Sender)
}

func userAccessMetadata(m InboundMessage) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"sender_staff_id",
		"sender_id",
		"sender_corp_id",
		"conversation_id",
		"conversation_title",
		"chat_type",
		"is_group",
		"is_at_bot",
		"sender_profile",
	} {
		if v, ok := m.Metadata[key]; ok {
			out[key] = v
		}
	}
	return out
}

func (l *Loop) upsertUserAccess(st storage.StateData, m InboundMessage, status string) storage.StateData {
	if st.UserAccess == nil {
		st.UserAccess = map[string]storage.UserAccessRecord{}
	}
	channel := strings.TrimSpace(nonEmpty(m.Channel, "command"))
	userID := strings.TrimSpace(m.Sender)
	if userID == "" {
		return st
	}
	key := userAccessKey(channel, userID)
	now := time.Now().UTC().Format(time.RFC3339)
	rec := st.UserAccess[key]
	if strings.TrimSpace(rec.Channel) == "" {
		rec.Channel = channel
	}
	if strings.TrimSpace(rec.UserID) == "" {
		rec.UserID = userID
	}
	if strings.TrimSpace(rec.FirstSeenAt) == "" {
		rec.FirstSeenAt = now
	}
	if strings.TrimSpace(rec.Status) == "" || strings.EqualFold(strings.TrimSpace(rec.Status), "pending") || strings.EqualFold(status, "blocked") {
		rec.Status = status
	}
	rec.SenderName = userSenderName(m)
	rec.LastSeenAt = now
	rec.LastMessageID = strings.TrimSpace(m.ID)
	rec.LastText = shortText(m.Text, 120)
	rec.ThreadID = strings.TrimSpace(m.ThreadID)
	rec.ConversationID = strings.TrimSpace(anyString(m.Metadata["conversation_id"]))
	rec.ConversationTitle = strings.TrimSpace(anyString(m.Metadata["conversation_title"]))
	rec.Source = "gateway"
	rec.Metadata = userAccessMetadata(m)
	st.UserAccess[key] = rec
	return st
}

func (l *Loop) unauthorizedNotice(m InboundMessage, status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked":
		return "当前账号已被网关拒绝访问，请联系管理员在 GUI 的 Access Requests 中调整状态。"
	default:
		return "当前账号尚未通过网关授权，消息已记录。请提醒管理员在 GUI 的 Access Requests 中允许后再重试。"
	}
}

func shortText(s string, n int) string {
	t := strings.TrimSpace(s)
	if n <= 0 || len(t) <= n {
		return t
	}
	return t[:n-3] + "..."
}

func (l *Loop) ensureSessionWorkdir(st storage.StateData, sessionKey string) (storage.StateData, string, error) {
	if st.SessionMeta == nil {
		st.SessionMeta = map[string]storage.SessionMetaRecord{}
	}
	meta := st.SessionMeta[sessionKey]
	workdir := strings.TrimSpace(meta.Workdir)
	if workdir == "" {
		defaultWorkdir, err := defaultSessionWorkdir()
		if err != nil {
			return st, "", err
		}
		workdir = defaultWorkdir
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return st, "", fmt.Errorf("create session workdir failed: %w", err)
	}
	info, err := os.Stat(workdir)
	if err != nil {
		return st, "", fmt.Errorf("stat session workdir failed: %w", err)
	}
	if !info.IsDir() {
		return st, "", fmt.Errorf("invalid session workdir (not a directory): %s", workdir)
	}
	meta.Workdir = workdir
	if strings.TrimSpace(meta.Status) == "" {
		meta.Status = "ready"
	}
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	st.SessionMeta[sessionKey] = meta
	return st, workdir, nil
}

func defaultSessionWorkdir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir failed: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("resolve home dir failed: empty")
	}
	path := filepath.Join(home, ".cag", "workspace", "default")
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve default workdir failed: %w", err)
	}
	return resolved, nil
}

func progressIntervalSec() int {
	raw := strings.TrimSpace(os.Getenv("CAG_PROGRESS_TICK_SEC"))
	if raw == "" {
		return 20
	}
	n := 0
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 20
	}
	if n <= 0 {
		return 0
	}
	return n
}

func (l *Loop) channelIngressEnabled(st storage.StateData) bool {
	channel := strings.ToLower(strings.TrimSpace(nonEmpty(l.ChannelName, "command")))
	if st.ChannelStates == nil {
		return true
	}
	rec, ok := st.ChannelStates[channel]
	if !ok {
		return true
	}
	return rec.Enabled
}

func (l *Loop) channelStateTransitionEvent(enabled bool) (string, bool) {
	if !l.channelStateSeen {
		l.channelStateSeen = true
		l.channelEnabled = enabled
		if enabled {
			return "", false
		}
		return "channel_disabled", true
	}
	if l.channelEnabled == enabled {
		return "", false
	}
	l.channelEnabled = enabled
	if enabled {
		return "channel_enabled", true
	}
	return "channel_disabled", true
}

func (l *Loop) shouldLogFetchFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(err.Error())
	if !l.fetchErrorActive || l.lastFetchError != msg {
		l.fetchErrorActive = true
		l.lastFetchError = msg
		return true
	}
	return false
}

func (l *Loop) noteFetchSuccess() {
	l.fetchErrorActive = false
	l.lastFetchError = ""
}

func (l *Loop) logACPEvents(msgID string, events []map[string]any) {
	if len(events) == 0 {
		return
	}
	loopLogInfo("acp_events", map[string]any{"msg_id": msgID, "count": len(events)})
	for i, ev := range events {
		method := strings.TrimSpace(anyString(ev["method"]))
		stage := "-"
		text := ""
		if params, ok := ev["params"].(map[string]any); ok {
			if update, ok := params["update"].(map[string]any); ok {
				if su := strings.TrimSpace(anyString(update["sessionUpdate"])); su != "" {
					stage = su
				}
				if c, ok := update["content"].(map[string]any); ok {
					text = strings.TrimSpace(anyString(c["text"]))
				}
				if text == "" {
					text = strings.TrimSpace(anyString(update["text"]))
				}
				if text == "" {
					text = strings.TrimSpace(anyString(update["summary"]))
				}
			}
			if stage == "-" {
				if m := strings.TrimSpace(anyString(params["method"])); m != "" {
					stage = m
				}
			}
		}
		loopLogInfo("acp_event", map[string]any{
			"msg_id": msgID,
			"index":  i + 1,
			"method": nonEmpty(method, "-"),
			"stage":  stage,
			"text":   shortText(text, 120),
		})
		l.appendInteraction(map[string]any{
			"kind":   "trace",
			"stage":  "acp_event",
			"msg_id": msgID,
			"index":  i + 1,
			"method": method,
			"event":  stage,
			"text":   shortText(text, 200),
			"ts":     time.Now().UTC().Format(time.RFC3339),
		})
	}
}
