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
		if l.ProcessOnlyLatest && len(msgs) > 1 {
			msgs = msgs[len(msgs)-1:]
		}
		for _, m := range msgs {
			if _, ok := processed[m.ID]; ok {
				continue
			}
			if !l.allowed(m.Sender) {
				continue
			}

			cmd := strings.TrimSpace(m.Text)
			sessionKey := l.sessionKeyFor(m)
			if cmd == "/clear" || cmd == "/new" {
				delete(st.SessionMap, sessionKey)
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				_ = l.Storage.SaveState(st)
				_ = l.Channel.Send("会话已重置。", m.Sender, m.ID, "")
				continue
			}

			_ = l.Channel.Send("已收到，正在处理", m.Sender, "ack-"+m.ID, "")

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
				Metadata: map[string]any{
					"received_ts": m.TS,
					"message_id":  m.ID,
					"workdir":     mustGetwd(),
				},
			}

			result, execErr := l.Agent.Execute(req)
			if execErr != nil {
				errText := fmt.Sprintf("执行失败: %v", execErr)
				_ = l.Channel.Send(errText, m.Sender, m.ID, "")
				_ = l.Storage.AppendInteraction(map[string]any{"msg_id": m.ID, "error": errText, "ts": time.Now().UTC().Format(time.RFC3339)})
				processed[m.ID] = struct{}{}
				st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
				_ = l.Storage.SaveState(st)
				continue
			}

			if strings.TrimSpace(result.SessionID) != "" {
				st.SessionMap[sessionKey] = result.SessionID
			}
			reportPath := l.writeReport(m, req, result)
			finalText := formatFinal(result)
			_ = l.Channel.Send(finalText, m.Sender, m.ID, reportPath)
			_ = l.Storage.AppendInteraction(map[string]any{
				"msg_id":      m.ID,
				"sender":      m.Sender,
				"text":        m.Text,
				"trace_id":    req.TraceID,
				"session_id":  result.SessionID,
				"result":      result.Summary,
				"status":      result.Status,
				"elapsed_sec": result.ElapsedSec,
				"ts":          time.Now().UTC().Format(time.RFC3339),
			})

			processed[m.ID] = struct{}{}
			st.ProcessedIDs = append(st.ProcessedIDs, m.ID)
			_ = l.Storage.SaveState(st)
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
