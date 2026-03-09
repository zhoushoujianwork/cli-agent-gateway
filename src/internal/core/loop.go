package core

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cli-agent-gateway/internal/storage"
)

type Loop struct {
	Channel             ChannelAdapter
	Agent               AgentAdapter
	Storage             storage.Backend
	RemoteUserID        string
	AllowedFrom         map[string]struct{}
	PendingUnknownUsers bool
	ProcessOnlyLatest   bool
	PollIntervalSec     int
	ReplyStyleEnabled   bool
	ReplyStylePrompt    string
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
			fmt.Fprintf(os.Stderr, "[WARN] reload state failed: %v\n", loadErr)
		}
		msgs, err := l.Channel.Fetch()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] fetch error: %v\n", err)
			time.Sleep(time.Duration(l.PollIntervalSec) * time.Second)
			continue
		}
		if len(msgs) > 0 {
			fmt.Fprintf(os.Stderr, "[INFO] fetch ok count=%d\n", len(msgs))
		}
		if l.ProcessOnlyLatest && len(msgs) > 1 {
			fmt.Fprintf(os.Stderr, "[INFO] process_only_latest enabled keep=1 drop=%d\n", len(msgs)-1)
			msgs = msgs[len(msgs)-1:]
		}
		for _, m := range msgs {
			if _, ok := processed[m.ID]; ok {
				fmt.Fprintf(os.Stderr, "[INFO] skip duplicate msg_id=%s sender=%s\n", m.ID, m.Sender)
				continue
			}
			allowed, accessStatus := l.accessDecision(st, m)
			st = l.upsertUserAccess(st, m, accessStatus)
			if !allowed {
				l.saveState(st)
				fmt.Fprintf(os.Stderr, "[INFO] skip unauthorized msg_id=%s sender=%s status=%s\n", m.ID, m.Sender, accessStatus)
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
					fmt.Fprintf(os.Stderr, "[WARN] send unauthorized notice failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				} else {
					fmt.Fprintf(os.Stderr, "[INFO] send unauthorized notice ok msg_id=%s to=%s status=%s\n", m.ID, m.Sender, accessStatus)
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "[INFO] inbound accepted msg_id=%s sender=%s channel=%s thread=%s text=%s\n", m.ID, m.Sender, nonEmpty(m.Channel, "command"), nonEmpty(m.ThreadID, "-"), shortText(m.Text, 80))
			now := time.Now().UTC().Format(time.RFC3339)
			l.appendInteraction(map[string]any{
				"kind":         "inbound_received",
				"msg_id":       m.ID,
				"sender":       m.Sender,
				"text":         m.Text,
				"time":         now,
				"user_profile": buildUserProfile(m),
			})

			cmd := strings.TrimSpace(m.Text)
			baseSessionKey := l.sessionKeyFor(m)
			sessionKey := baseSessionKey
			if deletedAt := strings.TrimSpace(st.SessionDeleted[baseSessionKey]); deletedAt != "" {
				sessionKey = deriveReopenedSessionKey(baseSessionKey, deletedAt)
			}
			if cmd == "/clear" || cmd == "/new" {
				delete(st.SessionMap, sessionKey)
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				l.saveState(st)
				fmt.Fprintf(os.Stderr, "[INFO] session reset msg_id=%s session_key=%s sender=%s\n", m.ID, sessionKey, m.Sender)
				if err := l.Channel.Send("会话已重置。", m.Sender, m.ID, ""); err != nil {
					fmt.Fprintf(os.Stderr, "[WARN] send clear reply failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				} else {
					fmt.Fprintf(os.Stderr, "[INFO] send clear reply ok msg_id=%s to=%s\n", m.ID, m.Sender)
				}
				continue
			}

			if err := l.Channel.Send("已收到，正在处理", m.Sender, "ack-"+m.ID, ""); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] send ack failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_ack_failed",
					"msg_id": m.ID,
					"to":     m.Sender,
					"error":  err.Error(),
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			} else {
				fmt.Fprintf(os.Stderr, "[INFO] send ack ok msg_id=%s to=%s\n", m.ID, m.Sender)
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
			var workdirErr error
			var resolvedWorkdir string
			st, resolvedWorkdir, workdirErr = l.ensureSessionWorkdir(st, sessionKey)
			if workdirErr != nil {
				errText := fmt.Sprintf("执行失败: workdir 初始化失败: %v", workdirErr)
				if err := l.Channel.Send(errText, m.Sender, m.ID, ""); err != nil {
					fmt.Fprintf(os.Stderr, "[WARN] send workdir error reply failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				}
				l.appendInteraction(map[string]any{
					"msg_id":       m.ID,
					"error":        errText,
					"ts":           time.Now().UTC().Format(time.RFC3339),
					"user_profile": buildUserProfile(m),
				})
				fmt.Fprintf(os.Stderr, "[WARN] execute skipped msg_id=%s reason=workdir_init_failed err=%v\n", m.ID, workdirErr)
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				l.saveState(st)
				continue
			}

			req := TaskRequest{
				TraceID:    traceID(m.ID),
				SessionKey: sessionKey,
				UserText:   userText,
				Sender:     m.Sender,
				Channel:    nonEmpty(m.Channel, "command"),
				ThreadID:   m.ThreadID,
				SessionID:  st.SessionMap[sessionKey],
				Metadata: mergeMetadata(m.Metadata, map[string]any{
					"received_ts": m.TS,
					"message_id":  m.ID,
					"workdir":     resolvedWorkdir,
				}),
			}
			fmt.Fprintf(os.Stderr, "[INFO] session resolved msg_id=%s session_key=%s session_id=%s workdir=%s\n", m.ID, sessionKey, req.SessionID, resolvedWorkdir)
			l.appendInteraction(map[string]any{
				"kind":        "trace",
				"stage":       "session_resolved",
				"msg_id":      m.ID,
				"session_key": sessionKey,
				"session_id":  req.SessionID,
				"workdir":     resolvedWorkdir,
				"ts":          now,
			})
			fmt.Fprintf(os.Stderr, "[INFO] execute start msg_id=%s session_key=%s session_id=%s sender=%s\n", m.ID, sessionKey, req.SessionID, m.Sender)
			l.appendInteraction(map[string]any{
				"kind":       "trace",
				"stage":      "execute_start",
				"msg_id":     m.ID,
				"session_id": req.SessionID,
				"trace_id":   req.TraceID,
				"ts":         time.Now().UTC().Format(time.RFC3339),
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
						fmt.Fprintf(os.Stderr, "[WARN] send progress failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
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
						fmt.Fprintf(os.Stderr, "[INFO] send progress ok msg_id=%s to=%s index=%d\n", m.ID, m.Sender, progressCount)
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
					fmt.Fprintf(os.Stderr, "[WARN] send error reply failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				} else {
					fmt.Fprintf(os.Stderr, "[INFO] send error reply ok msg_id=%s to=%s\n", m.ID, m.Sender)
				}
				l.appendInteraction(map[string]any{
					"msg_id":       m.ID,
					"error":        errText,
					"ts":           time.Now().UTC().Format(time.RFC3339),
					"user_profile": buildUserProfile(m),
				})
				fmt.Fprintf(os.Stderr, "[WARN] execute failed msg_id=%s err=%v\n", m.ID, execErr)
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				l.saveState(st)
				continue
			}
			fmt.Fprintf(os.Stderr, "[INFO] execute done msg_id=%s status=%s elapsed=%ds\n", m.ID, result.Status, result.ElapsedSec)
			l.logACPEvents(m.ID, result.RawEvents)

			if strings.TrimSpace(result.SessionID) != "" {
				st.SessionMap[sessionKey] = result.SessionID
			}
			meta := st.SessionMeta[sessionKey]
			meta.Workdir = resolvedWorkdir
			meta.Status = "ready"
			meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			st.SessionMeta[sessionKey] = meta
			reportPath := l.writeReport(m, req, result)
			fmt.Fprintf(os.Stderr, "[INFO] report written msg_id=%s path=%s\n", m.ID, nonEmpty(reportPath, "-"))
			finalText := formatFinal(result)
			if err := l.Channel.Send(finalText, m.Sender, m.ID, reportPath); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] send final failed msg_id=%s to=%s err=%v\n", m.ID, m.Sender, err)
				l.appendInteraction(map[string]any{
					"kind":   "trace",
					"stage":  "send_final_failed",
					"msg_id": m.ID,
					"to":     m.Sender,
					"error":  err.Error(),
					"ts":     time.Now().UTC().Format(time.RFC3339),
				})
			} else {
				fmt.Fprintf(os.Stderr, "[INFO] send final ok msg_id=%s to=%s\n", m.ID, m.Sender)
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
				"session_id":   result.SessionID,
				"result":       result.Summary,
				"status":       result.Status,
				"elapsed_sec":  result.ElapsedSec,
				"ts":           time.Now().UTC().Format(time.RFC3339),
				"user_profile": buildUserProfile(m),
			})

			processed[m.ID] = struct{}{}
			st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
			l.saveState(st)
			fmt.Fprintf(os.Stderr, "[INFO] persist done msg_id=%s processed_total=%d\n", m.ID, len(st.ProcessedIDs))
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
		fmt.Fprintf(os.Stderr, "[WARN] append interaction failed err=%v\n", err)
	}
}

func (l *Loop) saveState(st storage.StateData) {
	if err := l.Storage.SaveState(st); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] save state failed err=%v\n", err)
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

func (l *Loop) logACPEvents(msgID string, events []map[string]any) {
	if len(events) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "[INFO] acp events msg_id=%s count=%d\n", msgID, len(events))
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
		fmt.Fprintf(os.Stderr, "[INFO] acp event msg_id=%s idx=%d method=%s stage=%s text=%s\n", msgID, i+1, nonEmpty(method, "-"), stage, shortText(text, 120))
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
