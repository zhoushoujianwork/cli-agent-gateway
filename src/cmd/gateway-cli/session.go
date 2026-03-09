package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"cli-agent-gateway/internal/core"
	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"
	"cli-agent-gateway/internal/utils/sessionctl"
)

type CommandSpec struct {
	Path         string
	Purpose      string
	FeatureGroup string
	SunsetGroup  string
}

type MessagesPayload struct {
	OK         bool                             `json:"ok"`
	Action     string                           `json:"action"`
	SessionKey string                           `json:"session_key"`
	Messages   []sessionctl.SessionMessageItem  `json:"messages"`
	Timeline   []sessionctl.SessionTimelineItem `json:"timeline"`
}

type SessionMutationPayload struct {
	OK         bool   `json:"ok"`
	Action     string `json:"action"`
	SessionKey string `json:"session_key,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Workdir    string `json:"workdir,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	Status     string `json:"status,omitempty"`
}

func commandSpecs() []CommandSpec {
	return []CommandSpec{
		{Path: "session create", Purpose: "Create a new local task session", FeatureGroup: "session-lifecycle", SunsetGroup: "session"},
		{Path: "session delete", Purpose: "Archive a session and remove its bindings", FeatureGroup: "session-lifecycle", SunsetGroup: "session"},
		{Path: "session list", Purpose: "List gateway sessions", FeatureGroup: "session-read-model", SunsetGroup: "session"},
		{Path: "session show", Purpose: "Show one session with bindings/runtime", FeatureGroup: "session-read-model", SunsetGroup: "session"},
		{Path: "session send", Purpose: "Send a message into a session runtime", FeatureGroup: "session-io", SunsetGroup: "session"},
		{Path: "session messages", Purpose: "Read session messages and timeline", FeatureGroup: "session-read-model", SunsetGroup: "session"},
		{Path: "session clear", Purpose: "Reset live session context while keeping history", FeatureGroup: "session-runtime", SunsetGroup: "session"},
		{Path: "session attach", Purpose: "Attach a live runtime to a session", FeatureGroup: "session-runtime", SunsetGroup: "session"},
		{Path: "session detach", Purpose: "Detach the live runtime from a session", FeatureGroup: "session-runtime", SunsetGroup: "session"},
		{Path: "channel list", Purpose: "List supported channel entrypoints", FeatureGroup: "channel-read-model", SunsetGroup: "channel"},
		{Path: "channel inbox", Purpose: "List unassigned channel conversations", FeatureGroup: "channel-routing", SunsetGroup: "channel"},
		{Path: "channel show", Purpose: "Show one channel conversation or binding state", FeatureGroup: "channel-routing", SunsetGroup: "channel"},
		{Path: "binding create", Purpose: "Bind a channel conversation to a session", FeatureGroup: "binding-routing", SunsetGroup: "binding"},
		{Path: "binding delete", Purpose: "Delete a channel-to-session binding", FeatureGroup: "binding-routing", SunsetGroup: "binding"},
		{Path: "binding list", Purpose: "List channel-to-session bindings", FeatureGroup: "binding-routing", SunsetGroup: "binding"},
		{Path: "binding show", Purpose: "Show one binding", FeatureGroup: "binding-routing", SunsetGroup: "binding"},
		{Path: "runtime status", Purpose: "Show global runtime status", FeatureGroup: "runtime-ops", SunsetGroup: "runtime"},
		{Path: "runtime ps", Purpose: "List live session runtimes", FeatureGroup: "runtime-ops", SunsetGroup: "runtime"},
		{Path: "runtime logs", Purpose: "Show runtime log path or stream logs", FeatureGroup: "runtime-ops", SunsetGroup: "runtime"},
	}
}

func runSessionCommand(repoRoot string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "session requires a subcommand")
		return 2
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "create":
		fs := flag.NewFlagSet("session create", flag.ContinueOnError)
		key := fs.String("key", "", "session key")
		workdir := fs.String("workdir", "", "workdir")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "session.create", *jsonOut, &gatewayv1.ActionRequest{
			SessionKey: *key,
			Workdir:    *workdir,
		})
	case "delete", "show", "attach", "detach", "clear", "messages":
		fs := flag.NewFlagSet("session "+args[0], flag.ContinueOnError)
		key := fs.String("key", "", "session key")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "session."+strings.ToLower(strings.TrimSpace(args[0])), *jsonOut, &gatewayv1.ActionRequest{
			SessionKey: *key,
		})
	case "list":
		fs := flag.NewFlagSet("session list", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		includeArchived := fs.Bool("include-archived", false, "include archived sessions")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		return runAction(repoRoot, "session.list", *jsonOut, &gatewayv1.ActionRequest{
			IncludeArchived: *includeArchived,
		})
	case "send":
		fs := flag.NewFlagSet("session send", flag.ContinueOnError)
		key := fs.String("key", "", "session key")
		text := fs.String("text", "", "text to send")
		filePath := fs.String("file", "", "read text from file")
		source := fs.String("source", "session.send", "logical source")
		messageID := fs.String("message-id", "", "message id")
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		body := strings.TrimSpace(*text)
		if body == "" && strings.TrimSpace(*filePath) != "" {
			raw, err := os.ReadFile(strings.TrimSpace(*filePath))
			if err != nil {
				if *jsonOut {
					printJSONActionError("session.send", "file_read_failed", err.Error())
				} else {
					fmt.Fprintf(os.Stderr, "session send failed: %v\n", err)
				}
				return 1
			}
			body = strings.TrimSpace(string(raw))
		}
		if strings.TrimSpace(*messageID) == "" {
			*messageID = fmt.Sprintf("cli-%d", time.Now().UnixMilli())
		}
		return runAction(repoRoot, "session.send", *jsonOut, &gatewayv1.ActionRequest{
			SessionKey: *key,
			Text:       body,
			Source:     *source,
			MessageId:  *messageID,
		})
	default:
		fmt.Fprintf(os.Stderr, "unknown session subcommand: %s\n", args[0])
		return 2
	}
}

func runAction(repoRoot, action string, jsonOut bool, req *gatewayv1.ActionRequest) int {
	req.Action = action
	resp, err := tryActionViaGRPC(repoRoot, req)
	if err != nil {
		if jsonOut {
			printJSONActionError(action, "gateway_unreachable", formatGatewayUnavailable(err))
		} else {
			fmt.Fprintf(os.Stderr, "%s failed: %s\n", action, formatGatewayUnavailable(err))
		}
		return 1
	}
	payload, err := decodeActionPayload(resp)
	if err != nil {
		if jsonOut {
			printJSONActionError(action, "decode_failed", err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "%s failed: %v\n", action, err)
		}
		return 1
	}
	if jsonOut {
		printJSON(payload)
		if ok, present := payload["ok"].(bool); present && !ok {
			return 1
		}
		return 0
	}
	renderPlainAction(action, payload)
	if ok, present := payload["ok"].(bool); present && !ok {
		return 1
	}
	return 0
}

func renderPlainAction(action string, payload map[string]any) {
	switch action {
	case "session.list", "channel.inbox", "binding.list", "runtime.ps":
		if items, ok := payload["items"].([]any); ok {
			for _, item := range items {
				fmt.Println(mustJSON(item))
			}
			return
		}
	case "session.messages":
		if items, ok := payload["messages"].([]any); ok {
			for _, item := range items {
				fmt.Println(mustJSON(item))
			}
			return
		}
	}
	fmt.Println(mustJSON(payload))
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

type gatewaySessionProxyAgent struct {
	repoRoot string
}

func (a *gatewaySessionProxyAgent) Execute(req core.TaskRequest) (core.TaskResult, error) {
	msgID := strings.TrimSpace(fmt.Sprint(req.Metadata["message_id"]))
	resp, err := tryActionViaGRPC(a.repoRoot, &gatewayv1.ActionRequest{
		Action:     "session.send",
		SessionKey: req.SessionKey,
		Text:       req.UserText,
		MessageId:  msgID,
		Source:     req.Channel,
	})
	if err != nil {
		return core.TaskResult{}, err
	}
	payload, err := decodeActionPayload(resp)
	if err != nil {
		return core.TaskResult{}, err
	}
	if ok, _ := payload["ok"].(bool); !ok {
		return core.TaskResult{
			TraceID:    req.TraceID,
			Status:     "error",
			Summary:    sessionctl.CleanString(payload["error"]),
			ErrorText:  sessionctl.CleanString(payload["error"]),
			OutputText: sessionctl.CleanString(payload["raw_output"]),
		}, fmt.Errorf("%s", sessionctl.CleanString(payload["error"]))
	}
	return core.TaskResult{
		TraceID:        req.TraceID,
		Status:         sessionctl.CleanString(payload["status"]),
		Summary:        sessionctl.CleanString(payload["summary"]),
		TerminalReason: sessionctl.CleanString(payload["terminal_reason"]),
		ElapsedSec:     intFromAny(payload["elapsed_sec"]),
		OutputText:     sessionctl.CleanString(payload["raw_output"]),
	}, nil
}

func (a *gatewaySessionProxyAgent) Close() error {
	return nil
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}
