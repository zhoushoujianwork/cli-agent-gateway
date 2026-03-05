package acp

import (
	"fmt"
	"os"
	"strings"
	"time"

	"cli-agent-gateway/internal/core"
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
		debug:                strings.TrimSpace(os.Getenv("CAG_GO_DEBUG")) == "1",
	}
}

func (a *Adapter) Close() error {
	return a.client.Close()
}

func (a *Adapter) Execute(req core.TaskRequest) (core.TaskResult, error) {
	if err := a.ensureReady(); err != nil {
		return core.TaskResult{}, err
	}

	start := time.Now()
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sid, err := a.createSession(req)
		if err != nil {
			return core.TaskResult{}, err
		}
		sessionID = sid
	}

	promptID, err := a.client.StartRequest("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]any{{
			"type": "text",
			"text": req.UserText,
		}},
		"metadata": req.Metadata,
	})
	if err != nil {
		return core.TaskResult{}, err
	}
	a.debugf("prompt start request_id=%d session_id=%s", promptID, sessionID)

	deadline := time.Now().Add(time.Duration(a.timeoutSec) * time.Second)
	summary := ""
	status := "timeout"
	output := ""
	rawEvents := make([]map[string]any, 0)

	for time.Now().Before(deadline) {
		resp, err := a.client.PollResponse(promptID, 100*time.Millisecond)
		if err != nil {
			return core.TaskResult{}, err
		}
		if resp != nil {
			a.debugf("prompt response id=%d error=%v", resp.ID, resp.Error)
			if resp.Error != nil {
				return core.TaskResult{}, fmt.Errorf("session/prompt error: %v", resp.Error)
			}
			if result, ok := resp.Result.(map[string]any); ok {
				text := extractText(result)
				if text != "" {
					summary = text
					output = text
				}
				if isTerminal(result) {
					status = statusFrom(result)
					if summary == "" {
						summary = "任务已处理完成。"
					}
					return core.TaskResult{
						TraceID:    req.TraceID,
						Status:     status,
						Summary:    summary,
						ElapsedSec: int(time.Since(start).Seconds()),
						SessionID:  sessionID,
						OutputText: output,
						RawEvents:  rawEvents,
					}, nil
				}
			}
		}

		serverReq := a.client.PopRequest(50 * time.Millisecond)
		if serverReq != nil {
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
			a.debugf("notification method=%s params=%v", n.Method, n.Params)
			rawEvents = append(rawEvents, map[string]any{"method": n.Method, "params": n.Params})
			if text := extractText(n.Params); text != "" {
				summary = text
				output = text
			}
			if isTerminal(n.Params) {
				status = statusFrom(n.Params)
				if summary == "" {
					summary = "任务已处理完成。"
				}
				return core.TaskResult{
					TraceID:    req.TraceID,
					Status:     status,
					Summary:    summary,
					ElapsedSec: int(time.Since(start).Seconds()),
					SessionID:  sessionID,
					OutputText: output,
					RawEvents:  rawEvents,
				}, nil
			}
		}
	}

	if summary == "" {
		summary = "任务超时，未收到终态事件。"
	}
	return core.TaskResult{
		TraceID:    req.TraceID,
		Status:     status,
		Summary:    summary,
		ElapsedSec: int(time.Since(start).Seconds()),
		SessionID:  sessionID,
		OutputText: output,
		RawEvents:  rawEvents,
	}, nil
}

func (a *Adapter) debugf(format string, args ...any) {
	if !a.debug {
		return
	}
	fmt.Fprintf(os.Stderr, "[go-acp] "+format+"\n", args...)
}

func (a *Adapter) ensureReady() error {
	a.debugf("ensureReady start")
	if err := a.client.Start(); err != nil {
		return err
	}
	if a.initialized {
		a.debugf("ensureReady already initialized")
		return nil
	}
	a.debugf("initialize request")
	_, err := a.client.SendRequest("initialize", map[string]any{
		"protocolVersion":    "0.2",
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "cli-agent-gateway-go", "version": "0.1.0"},
	}, time.Duration(a.initializeTimeoutSec)*time.Second)
	if err != nil {
		return err
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
			"idempotency_key": req.SessionKey,
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
			return "", nil
		}
		if i == attempts-1 {
			return "", err
		}
		if a.sessionNewBackoffSec > 0 {
			time.Sleep(time.Duration(a.sessionNewBackoffSec*float64(time.Second)) * time.Duration(1<<i))
		}
	}
	return "", nil
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

func isTerminal(payload map[string]any) bool {
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

func anyString(v any) string {
	s, _ := v.(string)
	return s
}
