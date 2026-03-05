package core

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"cli-agent-gateway/internal/storage"
)

type Loop struct {
	Channel           ChannelAdapter
	Agent             AgentAdapter
	Storage           storage.Backend
	RemoteUserID      string
	AllowedFrom       map[string]struct{}
	ProcessOnlyLatest bool
	PollIntervalSec   int
	ReplyStyleEnabled bool
	ReplyStylePrompt  string
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
			if !l.allowed(m.Sender) {
				fmt.Fprintf(os.Stderr, "[INFO] skip unauthorized msg_id=%s sender=%s\n", m.ID, m.Sender)
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
			sessionKey := l.sessionKeyFor(m)
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
					"workdir":     mustGetwd(),
				}),
			}
			fmt.Fprintf(os.Stderr, "[INFO] session resolved msg_id=%s session_key=%s session_id=%s\n", m.ID, sessionKey, req.SessionID)
			l.appendInteraction(map[string]any{
				"kind":        "trace",
				"stage":       "session_resolved",
				"msg_id":      m.ID,
				"session_key": sessionKey,
				"session_id":  req.SessionID,
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

func (l *Loop) allowed(sender string) bool {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return false
	}
	if len(l.AllowedFrom) == 0 {
		if strings.TrimSpace(l.RemoteUserID) == "" {
			return true
		}
		return sender == strings.TrimSpace(l.RemoteUserID)
	}
	_, ok := l.AllowedFrom[sender]
	return ok
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

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
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

func shortText(s string, n int) string {
	t := strings.TrimSpace(s)
	if n <= 0 || len(t) <= n {
		return t
	}
	return t[:n-3] + "..."
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
