package acp

import (
	"encoding/json"
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
			eventParams := map[string]any{
				"method": "session/prompt.response",
				"params": map[string]any{
					"id":     resp.ID,
					"error":  resp.Error,
					"result": resp.Result,
				},
			}
			rawEvents = append(rawEvents, eventParams)
			a.emitTaskEvent(req, len(rawEvents), "session/prompt.response", eventParams["params"])
			a.debugf("prompt response id=%d payload=%s", resp.ID, marshalLogJSON(map[string]any{
				"error":  resp.Error,
				"result": resp.Result,
			}, 16384))
			if resp.Error != nil {
				return core.TaskResult{}, newProtocolError("session/prompt", fmt.Sprintf("jsonrpc error: %v", resp.Error))
			}
			if result, ok := resp.Result.(map[string]any); ok {
				text := extractText(result)
				rawText, hasRawText := extractRawText(result)
				if shouldUsePayloadTextForAssistantOutput(result) && (text != "" || hasRawText) {
					if shouldAppendACPOutput(result) {
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
			eventParams := map[string]any{
				"method": "session/server_request",
				"params": map[string]any{
					"id":     serverReq.ID,
					"method": serverReq.Method,
					"params": serverReq.Params,
				},
			}
			rawEvents = append(rawEvents, eventParams)
			a.emitTaskEvent(req, len(rawEvents), "session/server_request", eventParams["params"])
			a.debugf("server request method=%s id=%d payload=%s", serverReq.Method, serverReq.ID, marshalLogJSON(serverReq.Params, 8192))
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
			a.debugf("notification method=%s payload=%s", n.Method, marshalLogJSON(n.Params, 16384))
			rawEvents = append(rawEvents, map[string]any{"method": n.Method, "params": n.Params})
			a.emitTaskEvent(req, len(rawEvents), n.Method, n.Params)
			text := extractText(n.Params)
			rawText, hasRawText := extractRawText(n.Params)
			if shouldUsePayloadTextForAssistantOutput(n.Params) && (text != "" || hasRawText) {
				if shouldAppendACPOutput(n.Params) {
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

func (a *Adapter) emitTaskEvent(req core.TaskRequest, index int, method string, raw any) {
	if req.EventSink == nil {
		return
	}
	params, ok := raw.(map[string]any)
	if !ok {
		return
	}
	event, ok := buildTaskEvent(index, method, params)
	if !ok {
		return
	}
	req.EventSink(event)
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
	text, _, _ := extractPayloadText(payload)
	return strings.TrimSpace(text)
}

func extractRawText(payload map[string]any) (string, bool) {
	_, raw, hasRaw := extractPayloadText(payload)
	return raw, hasRaw
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

func shouldAppendACPOutput(payload map[string]any) bool {
	updateType := sessionUpdateType(payload)
	if isChunkUpdate(updateType) {
		return true
	}
	if updateType == "" || isTerminal(payload) {
		return false
	}
	switch {
	case strings.Contains(updateType, "tool"),
		strings.Contains(updateType, "skill"),
		strings.Contains(updateType, "reason"),
		strings.Contains(updateType, "think"),
		strings.Contains(updateType, "plan"),
		strings.Contains(updateType, "progress"),
		strings.HasSuffix(updateType, "_update"):
		return true
	default:
		return false
	}
}

func shouldUsePayloadTextForAssistantOutput(payload map[string]any) bool {
	updateType := strings.ToLower(strings.TrimSpace(sessionUpdateType(payload)))
	if updateType == "" {
		return true
	}
	return strings.Contains(updateType, "agent_message")
}

func buildTaskEvent(index int, method string, params map[string]any) (core.TaskEvent, bool) {
	method = strings.TrimSpace(method)
	if params == nil {
		return core.TaskEvent{}, false
	}
	event := core.TaskEvent{
		ID:     fmt.Sprintf("acp-%d", index),
		Method: method,
		Stage:  method,
		Kind:   "trace",
	}

	update, _ := params["update"].(map[string]any)
	if update != nil {
		if stage := strings.TrimSpace(anyString(update["sessionUpdate"])); stage != "" {
			event.Stage = stage
		}
		event.Status = strings.TrimSpace(anyString(update["status"]))
		event.ActivityKey = firstNonEmptyString(
			anyString(update["toolCallId"]),
			anyString(update["tool_call_id"]),
			anyString(update["skillCallId"]),
			anyString(update["skill_call_id"]),
			strings.TrimSpace(anyString(update["title"])),
			event.Stage,
		)
		event.Title = firstNonEmptyString(
			anyString(update["title"]),
			anyString(update["name"]),
			anyString(update["toolName"]),
			anyString(update["tool_name"]),
			anyString(update["skillName"]),
			anyString(update["skill_name"]),
		)
		event.Detail = eventDetailForUpdate(update)
		if _, raw, ok := extractUpdateText(update); ok {
			event.Text = raw
		}
		event.PayloadPreview = marshalLogJSON(update, 4096)
		assignTaskEventKind(&event, update)
		if event.Title == "" {
			event.Title = defaultEventTitle(event.Kind, event.Stage)
		}
		return event, true
	}

	if method == "session/prompt.response" {
		if result, ok := params["result"].(map[string]any); ok {
			event.Stage = firstNonEmptyString(sessionUpdateType(result), "prompt_response")
			event.Text = strings.TrimSpace(extractText(result))
			event.PayloadPreview = marshalLogJSON(result, 4096)
			if event.Text != "" {
				event.Kind = "message"
				event.Title = "Assistant"
				return event, true
			}
			if isTerminal(result) {
				event.Kind = "status"
				event.Title = "Completed"
				event.Status = firstNonEmptyString(statusFrom(result), "ok")
				event.Detail = firstNonEmptyString(terminalReasonFrom(result), event.Status)
				return event, true
			}
		}
		return core.TaskEvent{}, false
	}

	if method == "session/server_request" {
		event.Kind = "permission"
		event.Title = strings.TrimSpace(anyString(params["method"]))
		event.Detail = marshalLogJSON(params["params"], 1024)
		event.PayloadPreview = event.Detail
		if event.Title == "" && event.Detail == "" {
			return core.TaskEvent{}, false
		}
		return event, true
	}
	return core.TaskEvent{}, false
}

func assignTaskEventKind(event *core.TaskEvent, update map[string]any) {
	stage := strings.ToLower(strings.TrimSpace(event.Stage))
	switch {
	case strings.Contains(stage, "agent_message"):
		event.Kind = "message"
	case strings.Contains(stage, "agent_thought"), strings.Contains(stage, "reason"), strings.Contains(stage, "think"):
		event.Kind = "thought"
	case strings.Contains(stage, "tool"), strings.TrimSpace(anyString(update["toolCallId"])) != "":
		event.Kind = "tool"
	case strings.Contains(stage, "skill"), strings.TrimSpace(anyString(update["skillCallId"])) != "":
		event.Kind = "skill"
	case strings.Contains(stage, "plan"):
		event.Kind = "plan"
	case strings.Contains(stage, "available_commands"):
		event.Kind = "command"
	case strings.Contains(stage, "usage"):
		event.Kind = "usage"
	default:
		if event.Text != "" {
			event.Kind = "message"
		}
	}
}

func defaultEventTitle(kind, stage string) string {
	switch strings.TrimSpace(kind) {
	case "message":
		return "Assistant"
	case "thought":
		return "Thinking"
	case "tool":
		return "Tool"
	case "skill":
		return "Skill"
	case "plan":
		return "Plan"
	case "command":
		return "Commands"
	case "usage":
		return "Usage"
	case "permission":
		return "Permission"
	default:
		return strings.TrimSpace(stage)
	}
}

func eventDetailForUpdate(update map[string]any) string {
	if update == nil {
		return ""
	}
	switch {
	case strings.TrimSpace(anyString(update["sessionUpdate"])) == "available_commands_update":
		if commands, ok := update["availableCommands"].([]any); ok {
			names := make([]string, 0, len(commands))
			for _, item := range commands {
				if m, ok := item.(map[string]any); ok {
					if name := strings.TrimSpace(anyString(m["name"])); name != "" {
						names = append(names, name)
					}
				}
			}
			return strings.Join(names, ", ")
		}
	case strings.Contains(strings.ToLower(strings.TrimSpace(anyString(update["sessionUpdate"]))), "tool"):
		if command := summarizeToolCommand(update["rawInput"]); command != "" {
			return command
		}
		if path := summarizeToolLocation(update["locations"]); path != "" {
			return path
		}
		return ""
	case strings.Contains(strings.ToLower(strings.TrimSpace(anyString(update["sessionUpdate"]))), "usage"):
		used := strings.TrimSpace(fmt.Sprint(update["used"]))
		size := strings.TrimSpace(fmt.Sprint(update["size"]))
		if used != "" || size != "" {
			return strings.TrimSpace("used " + used + " / " + size)
		}
	}
	if _, raw, ok := extractLooseText(update); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func summarizeToolCommand(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if parsed, ok := m["parsed_cmd"].([]any); ok && len(parsed) > 0 {
		if entry, ok := parsed[0].(map[string]any); ok {
			return firstNonEmptyString(
				anyString(entry["cmd"]),
				anyString(entry["path"]),
				anyString(entry["name"]),
			)
		}
	}
	if command, ok := m["command"].([]any); ok && len(command) > 0 {
		parts := make([]string, 0, len(command))
		for _, item := range command {
			part := strings.TrimSpace(fmt.Sprint(item))
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func summarizeToolLocation(raw any) string {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	if m, ok := arr[0].(map[string]any); ok {
		return firstNonEmptyString(anyString(m["path"]), anyString(m["uri"]))
	}
	return ""
}

func extractPayloadText(payload map[string]any) (string, string, bool) {
	if payload == nil {
		return "", "", false
	}
	if u, ok := payload["update"].(map[string]any); ok {
		if summary, raw, ok := extractUpdateText(u); ok {
			return summary, raw, true
		}
	}
	return extractLooseText(payload)
}

func extractUpdateText(update map[string]any) (string, string, bool) {
	if update == nil {
		return "", "", false
	}
	return extractLooseText(update)
}

func extractLooseText(payload map[string]any) (string, string, bool) {
	if payload == nil {
		return "", "", false
	}
	if text, ok := nonEmptyString(payload["content"]); ok {
		return text, text, true
	}
	if text, ok := renderContentText(payload["content"]); ok {
		return strings.TrimSpace(text), text, true
	}
	for _, key := range []string{"summary", "message", "text", "output"} {
		if text, ok := payload[key].(string); ok {
			return strings.TrimSpace(text), text, true
		}
	}
	return "", "", false
}

func renderContentText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if text, ok := renderContentText(item); ok && text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) == 0 {
			return "", false
		}
		return strings.Join(parts, ""), true
	case map[string]any:
		blockType := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
			anyString(x["type"]),
			anyString(x["kind"]),
		)))
		if text, ok := nonEmptyString(x["text"]); ok {
			return text, true
		}
		if blockType == "text" || blockType == "content" || blockType == "" || strings.Contains(blockType, "reason") || strings.Contains(blockType, "think") {
			if content, ok := renderContentText(x["content"]); ok {
				return content, true
			}
		}
		if inner, ok := renderContentText(x["parts"]); ok {
			return inner, true
		}
		if inner, ok := renderContentText(x["items"]); ok {
			return inner, true
		}
	}
	return "", false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nonEmptyString(v any) (string, bool) {
	text, ok := v.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func marshalLogJSON(v any, limit int) string {
	raw, err := json.Marshal(v)
	if err != nil {
		fallback := strings.TrimSpace(fmt.Sprint(v))
		return truncateForLog(fallback, limit)
	}
	return truncateForLog(string(raw), limit)
}

func truncateForLog(raw string, limit int) string {
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	if limit <= len("...(truncated)") {
		return raw[:limit]
	}
	return raw[:limit-len("...(truncated)")] + "...(truncated)"
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
