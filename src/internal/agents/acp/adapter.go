package acp

import (
	"fmt"
	"os"
	"strings"
	"time"

	"cli-agent-gateway/internal/core"
	"cli-agent-gateway/internal/infra/proclog"
)

type Adapter struct {
	client               *JsonRPCClient
	permissionPolicy     string
	timeoutSec           int
	initializeTimeoutSec int
	sessionNewTimeoutSec int
	sessionNewRetries    int
	sessionNewBackoffSec float64
	initialized          bool
	debug                bool
}

func NewAdapter(command, cwd, permissionPolicy string, timeoutSec, initializeTimeoutSec, sessionNewTimeoutSec, sessionNewRetries int, sessionNewBackoffSec float64) *Adapter {
	return &Adapter{
		client:               NewJSONRPCClient(command, cwd),
		permissionPolicy:     permissionPolicy,
		timeoutSec:           timeoutSec,
		initializeTimeoutSec: initializeTimeoutSec,
		sessionNewTimeoutSec: sessionNewTimeoutSec,
		sessionNewRetries:    sessionNewRetries,
		sessionNewBackoffSec: sessionNewBackoffSec,
		debug:                envBoolDefaultTrue("CAG_GO_DEBUG"),
	}
}

func (a *Adapter) Close() error {
	return a.client.Close()
}

func (a *Adapter) Execute(req core.TaskRequest) (core.TaskResult, error) {
	if err := a.ensureReady(); err != nil {
		return core.TaskResult{}, wrapACPError("initialize", err)
	}

	sessionID, err := a.createSession(req)
	if err != nil {
		return core.TaskResult{}, wrapACPError("session/new", err)
	}
	return a.runPrompt(req, sessionID)
}

func (a *Adapter) runPrompt(req core.TaskRequest, sessionID string) (core.TaskResult, error) {
	start := time.Now()
	deadline := time.Now().Add(time.Duration(a.timeoutSec) * time.Second)
	summary := ""
	status := "timeout"
	output := ""
	terminalReason := "timeout"
	rawEvents := make([]map[string]any, 0)
	lastHeartbeat := time.Now()
	lastEventAt := time.Now()
	sawChunk := false
	lastContentAt := time.Time{}
	softIdleSec := softTerminalIdleSec()
	softIdle := time.Duration(softIdleSec) * time.Second
	terminalDrain := terminalDrainWindow()
	terminalSeen := false

	promptID, err := a.client.StartRequest("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": req.UserText,
		}},
		"metadata": req.Metadata,
	})
	if err != nil {
		return core.TaskResult{}, wrapACPError("session/prompt", err)
	}
	a.debugf("prompt start request_id=%d session_id=%s", promptID, sessionID)

	for time.Now().Before(deadline) {
		if terminalSeen && time.Since(lastEventAt) >= terminalDrain {
			if strings.TrimSpace(summary) == "" {
				summary = strings.TrimSpace(output)
			}
			if strings.TrimSpace(summary) == "" {
				summary = "任务已处理完成。"
			}
			return core.TaskResult{
				TraceID:        req.TraceID,
				Status:         status,
				Summary:        summary,
				TerminalReason: terminalReason,
				ElapsedSec:     int(time.Since(start).Seconds()),
				OutputText:     output,
				RawEvents:      rawEvents,
			}, nil
		}

		if !terminalSeen && softIdleSec > 0 && sawChunk && !lastContentAt.IsZero() && time.Since(lastContentAt) >= softIdle {
			a.debugf("soft terminal request_id=%d session_id=%s reason=idle_after_chunk idle=%ds", promptID, sessionID, softIdleSec)
			rawEvents = append(rawEvents, map[string]any{
				"method": "session/soft_terminal",
				"params": map[string]any{
					"reason":   "idle_after_chunk",
					"idle_sec": softIdleSec,
				},
			})
			if strings.TrimSpace(summary) == "" {
				summary = strings.TrimSpace(output)
			}
			if strings.TrimSpace(summary) == "" {
				summary = "任务已处理完成。"
			}
			return core.TaskResult{
				TraceID:        req.TraceID,
				Status:         "ok",
				Summary:        summary,
				TerminalReason: "idle_after_chunk",
				ElapsedSec:     int(time.Since(start).Seconds()),
				OutputText:     output,
				RawEvents:      rawEvents,
			}, nil
		}

		if time.Since(lastHeartbeat) >= 5*time.Second {
			elapsed := int(time.Since(start).Seconds())
			remaining := int(time.Until(deadline).Seconds())
			if remaining < 0 {
				remaining = 0
			}
			a.debugf("execute waiting request_id=%d session_id=%s elapsed=%ds remaining=%ds", promptID, sessionID, elapsed, remaining)
			lastHeartbeat = time.Now()
		}

		resp, err := a.client.PollResponse(promptID, 100*time.Millisecond)
		if err != nil {
			return core.TaskResult{}, wrapACPError("session/prompt", err)
		}
		if resp != nil {
			lastEventAt = time.Now()
			rawEvents = append(rawEvents, map[string]any{
				"method": "session/prompt.response",
				"params": map[string]any{
					"id":     resp.ID,
					"error":  resp.Error,
					"result": resp.Result,
				},
			})
			a.debugf("prompt response id=%d error=%v", resp.ID, resp.Error)
			if resp.Error != nil {
				return core.TaskResult{}, newProtocolError("session/prompt", fmt.Sprintf("jsonrpc error: %v", resp.Error))
			}
			if result, ok := resp.Result.(map[string]any); ok {
				text := extractText(result)
				rawText, hasRawText := extractRawText(result)
				if text != "" || hasRawText {
					if isChunkUpdate(sessionUpdateType(result)) {
						output = appendRawChunk(output, rawText)
						summary = appendSummaryChunk(summary, text, rawText, hasRawText)
						sawChunk = true
					} else {
						if text != "" {
							summary = text
						}
						output = rawText
					}
					lastContentAt = time.Now()
				}
				if isTerminal(result) {
					status = statusFrom(result)
					terminalReason = terminalReasonFrom(result)
					terminalSeen = true
				}
			}
		}

		serverReq := a.client.PopRequest(50 * time.Millisecond)
		if serverReq != nil {
			lastEventAt = time.Now()
			rawEvents = append(rawEvents, map[string]any{
				"method": "session/server_request",
				"params": map[string]any{
					"id":     serverReq.ID,
					"method": serverReq.Method,
					"params": serverReq.Params,
				},
			})
			a.debugf("server request method=%s id=%d", serverReq.Method, serverReq.ID)
			if strings.Contains(strings.ToLower(serverReq.Method), "request_permission") {
				decision := "allow"
				if strings.EqualFold(a.permissionPolicy, "auto_deny") {
					decision = "deny"
				}
				_ = a.client.SendResponse(serverReq.ID, map[string]any{"decision": decision, "reason": "policy:" + a.permissionPolicy}, nil)
			} else {
				_ = a.client.SendResponse(serverReq.ID, nil, map[string]any{"code": -32601, "message": "unsupported method"})
			}
		}

		n := a.client.PopNotification(50 * time.Millisecond)
		if n != nil {
			lastEventAt = time.Now()
			a.debugf("notification method=%s params=%v", n.Method, n.Params)
			rawEvents = append(rawEvents, map[string]any{"method": n.Method, "params": n.Params})
			text := extractText(n.Params)
			rawText, hasRawText := extractRawText(n.Params)
			if text != "" || hasRawText {
				if isChunkUpdate(sessionUpdateType(n.Params)) {
					output = appendRawChunk(output, rawText)
					summary = appendSummaryChunk(summary, text, rawText, hasRawText)
					sawChunk = true
				} else {
					if text != "" {
						summary = text
					}
					output = rawText
				}
				lastContentAt = time.Now()
			}
			if isTerminal(n.Params) {
				status = statusFrom(n.Params)
				terminalReason = terminalReasonFrom(n.Params)
				terminalSeen = true
			}
		}
	}

	if summary == "" {
		summary = "任务超时，未收到终态事件。"
	}
	return core.TaskResult{
		TraceID:        req.TraceID,
		Status:         status,
		Summary:        summary,
		TerminalReason: terminalReason,
		ElapsedSec:     int(time.Since(start).Seconds()),
		OutputText:     output,
		RawEvents:      rawEvents,
	}, nil
}

func (a *Adapter) debugf(format string, args ...any) {
	if !a.debug {
		return
	}
	proclog.Info("acp", map[string]any{
		"event":   "debug",
		"message": fmt.Sprintf(format, args...),
	})
}

func (a *Adapter) ensureReady() error {
	a.debugf("ensureReady start")
	if err := a.client.Start(); err != nil {
		return wrapACPError("process/start", err)
	}
	if a.initialized {
		a.debugf("ensureReady already initialized")
		return nil
	}
	a.debugf("initialize request")
	_, err := a.client.SendRequest("initialize", map[string]any{
		"protocolVersion":    "0.2",
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "cli-agent-gateway-go", "version": "0.2.0"},
	}, time.Duration(a.initializeTimeoutSec)*time.Second)
	if err != nil {
		return wrapACPError("initialize", err)
	}
	a.initialized = true
	a.debugf("initialize ok")
	return nil
}

func (a *Adapter) createSession(req core.TaskRequest) (string, error) {
	a.debugf("createSession start key=%s", req.SessionKey)
	payload := map[string]any{
		"cwd":        req.Metadata["workdir"],
		"mcpServers": []any{},
		"session": map[string]any{
			"metadata": map[string]any{
				"channel":   req.Channel,
				"sender":    req.Sender,
				"thread_id": req.ThreadID,
			},
		},
	}
	attempts := a.sessionNewRetries + 1
	for i := 0; i < attempts; i++ {
		a.debugf("session/new attempt=%d", i+1)
		result, err := a.client.SendRequest("session/new", payload, time.Duration(a.sessionNewTimeoutSec)*time.Second)
		if err == nil {
			if m, ok := result.(map[string]any); ok {
				if sid, ok := m["sessionId"].(string); ok && strings.TrimSpace(sid) != "" {
					return sid, nil
				}
				if sid, ok := m["session_id"].(string); ok && strings.TrimSpace(sid) != "" {
					return sid, nil
				}
				if sid, ok := m["id"].(string); ok && strings.TrimSpace(sid) != "" {
					return sid, nil
				}
			}
			return "", newProtocolError("session/new", "response missing session id")
		}
		if i == attempts-1 {
			return "", wrapACPError("session/new", err)
		}
		if a.sessionNewBackoffSec > 0 {
			time.Sleep(time.Duration(a.sessionNewBackoffSec*float64(time.Second)) * time.Duration(1<<i))
		}
	}
	return "", newProtocolError("session/new", "exhausted retries without session id")
}

func extractText(payload map[string]any) string {
	if u, ok := payload["update"].(map[string]any); ok {
		if c, ok := u["content"].(map[string]any); ok {
			if t, ok := c["text"].(string); ok && strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		}
		for _, key := range []string{"summary", "message", "text", "output"} {
			if t, ok := u[key].(string); ok && strings.TrimSpace(t) != "" {
				return strings.TrimSpace(t)
			}
		}
	}
	for _, key := range []string{"summary", "message", "text", "output", "content"} {
		if t, ok := payload[key].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

func extractRawText(payload map[string]any) (string, bool) {
	if u, ok := payload["update"].(map[string]any); ok {
		if c, ok := u["content"].(map[string]any); ok {
			if t, ok := c["text"].(string); ok {
				return t, true
			}
		}
		for _, key := range []string{"summary", "message", "text", "output"} {
			if t, ok := u[key].(string); ok {
				return t, true
			}
		}
	}
	for _, key := range []string{"summary", "message", "text", "output", "content"} {
		if t, ok := payload[key].(string); ok {
			return t, true
		}
	}
	return "", false
}

func isTerminal(payload map[string]any) bool {
	if stopReasonFromPayload(payload) != "" {
		return true
	}
	if u, ok := payload["update"].(map[string]any); ok {
		su := strings.ToLower(strings.TrimSpace(anyString(u["sessionUpdate"])))
		if su == "turn_complete" || su == "agent_turn_complete" || su == "task_complete" || su == "completed" {
			return true
		}
	}
	st := strings.ToLower(strings.TrimSpace(anyString(payload["status"])))
	if st == "" {
		st = strings.ToLower(strings.TrimSpace(anyString(payload["state"])))
	}
	if st == "completed" || st == "done" || st == "success" || st == "failed" || st == "error" || st == "cancelled" {
		return true
	}
	if b, ok := payload["is_terminal"].(bool); ok {
		return b
	}
	return false
}

func statusFrom(payload map[string]any) string {
	if stop := stopReasonFromPayload(payload); stop != "" {
		switch stop {
		case "end_turn", "completed", "done", "success":
			return "ok"
		case "failed", "error":
			return "error"
		case "cancelled":
			return "cancelled"
		default:
			return "ok"
		}
	}
	st := strings.ToLower(strings.TrimSpace(anyString(payload["status"])))
	if st == "" {
		st = strings.ToLower(strings.TrimSpace(anyString(payload["state"])))
	}
	switch st {
	case "completed", "done", "success":
		return "ok"
	case "failed", "error":
		return "error"
	case "cancelled":
		return "cancelled"
	default:
		return "ok"
	}
}

func terminalReasonFrom(payload map[string]any) string {
	if stop := stopReasonFromPayload(payload); stop != "" {
		switch stop {
		case "end_turn", "completed", "done", "success":
			return "completed"
		default:
			return stop
		}
	}
	if u, ok := payload["update"].(map[string]any); ok {
		su := strings.ToLower(strings.TrimSpace(anyString(u["sessionUpdate"])))
		switch su {
		case "turn_complete", "agent_turn_complete", "task_complete", "completed":
			return "completed"
		case "":
			// continue below
		default:
			return su
		}
	}
	st := strings.ToLower(strings.TrimSpace(anyString(payload["status"])))
	if st == "" {
		st = strings.ToLower(strings.TrimSpace(anyString(payload["state"])))
	}
	switch st {
	case "completed", "done", "success":
		return "completed"
	case "failed", "error":
		return "error"
	case "cancelled":
		return "cancelled"
	case "timeout":
		return "timeout"
	case "":
		if b, ok := payload["is_terminal"].(bool); ok && b {
			return "terminal"
		}
		return ""
	default:
		return st
	}
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func envBoolDefaultTrue(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return true
	}
	return v != "0" && v != "false" && v != "off"
}

func envIntDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n := 0
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return fallback
	}
	if n < 0 {
		return fallback
	}
	return n
}

func terminalDrainWindow() time.Duration {
	ms := envIntDefault("ACP_TERMINAL_DRAIN_MS", 500)
	return time.Duration(ms) * time.Millisecond
}

func softTerminalIdleSec() int {
	// Default off. Some ACP agents can pause between streamed chunks and the
	// terminal event; forcing a synthetic terminal here truncates output.
	return envIntDefault("ACP_SOFT_TERMINAL_IDLE_SEC", 0)
}

func isSessionResourceNotFound(errObj any) bool {
	if m, ok := errObj.(map[string]any); ok {
		code := toInt(m["code"])
		msg := strings.ToLower(strings.TrimSpace(anyString(m["message"])))
		if code == -32002 {
			return true
		}
		return strings.Contains(msg, "resource not found") || strings.Contains(msg, "session not found")
	}
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(errObj)))
	return strings.Contains(msg, "resource not found") || strings.Contains(msg, "session not found")
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n := strings.TrimSpace(t)
		if n == "" {
			return 0
		}
		var out int
		_, _ = fmt.Sscanf(n, "%d", &out)
		return out
	default:
		return 0
	}
}

func sessionUpdateType(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if u, ok := payload["update"].(map[string]any); ok {
		return strings.ToLower(strings.TrimSpace(anyString(u["sessionUpdate"])))
	}
	return ""
}

func isChunkUpdate(t string) bool {
	v := strings.ToLower(strings.TrimSpace(t))
	return strings.HasSuffix(v, "_chunk")
}

func stopReasonFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if stop := strings.ToLower(strings.TrimSpace(anyString(payload["stopReason"]))); stop != "" {
		return stop
	}
	return strings.ToLower(strings.TrimSpace(anyString(payload["stop_reason"])))
}

func appendChunk(base, chunk string) string {
	next := strings.TrimSpace(chunk)
	if next == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return next
	}
	if punctuationOnly(next) {
		return base + next
	}
	if strings.HasSuffix(base, " ") || strings.HasSuffix(base, "\n") {
		return base + next
	}
	return base + " " + next
}

func appendSummaryChunk(base, text, raw string, hasRaw bool) string {
	if hasRaw {
		return appendRawChunk(base, raw)
	}
	return appendChunk(base, text)
}

func appendRawChunk(base, chunk string) string {
	if chunk == "" {
		return base
	}
	return base + chunk
}

func punctuationOnly(s string) bool {
	switch s {
	case ".", ",", "!", "?", ":", ";", "。", "，", "！", "？", "：", "；":
		return true
	default:
		return false
	}
}
