package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	gatewayv1 "cli-agent-gateway/internal/gen/gatewayv1"

	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/storage"
)

type SessionMessageItem struct {
	ID           string `json:"id"`
	SourceMsgID  string `json:"source_msg_id"`
	Role         string `json:"role"`
	Text         string `json:"text"`
	Time         string `json:"time"`
	Status       string `json:"status,omitempty"`
	StatusDetail string `json:"status_detail,omitempty"`
}

type SessionProcessEvent struct {
	ID     string `json:"id"`
	Time   string `json:"time"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type SessionTimelineItem struct {
	MsgID  string                `json:"msg_id"`
	Events []SessionProcessEvent `json:"events"`
}

type MessagesPayload struct {
	OK         bool                  `json:"ok"`
	Action     string                `json:"action"`
	SessionKey string                `json:"session_key"`
	Messages   []SessionMessageItem  `json:"messages"`
	Timeline   []SessionTimelineItem `json:"timeline"`
}

type SessionMutationPayload struct {
	OK         bool   `json:"ok"`
	Action     string `json:"action"`
	SessionKey string `json:"session_key,omitempty"`
}

func runMessages(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionKey := fs.String("session-key", "", "session key")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	key := normalizeSessionKey(*sessionKey)
	if key == "" {
		if *jsonOut {
			printJSONActionError("messages", "session_key_required", "--session-key is required")
			return 1
		}
		fmt.Fprintln(os.Stderr, "messages requires --session-key")
		return 2
	}

	grpcRes, err := trySessionMessagesViaGRPC(repoRoot, key)
	if err != nil {
		if *jsonOut {
			printJSONActionError("messages", "gateway_unreachable", formatGatewayUnavailable(err))
			return 1
		}
		fmt.Fprintf(os.Stderr, "messages failed: %s\n", formatGatewayUnavailable(err))
		return 1
	}
	if !grpcRes.GetOk() {
		if *jsonOut {
			printJSONActionError("messages", "grpc_messages_failed", grpcRes.GetError())
			return 1
		}
		fmt.Fprintf(os.Stderr, "messages failed: %s\n", grpcRes.GetError())
		return 1
	}
	payload := MessagesPayload{
		OK:         true,
		Action:     "messages",
		SessionKey: key,
		Messages:   make([]SessionMessageItem, 0, len(grpcRes.GetMessages())),
		Timeline:   make([]SessionTimelineItem, 0, len(grpcRes.GetTimeline())),
	}
	for _, m := range grpcRes.GetMessages() {
		payload.Messages = append(payload.Messages, SessionMessageItem{
			ID:           m.GetId(),
			SourceMsgID:  m.GetSourceMsgId(),
			Role:         m.GetRole(),
			Text:         m.GetText(),
			Time:         m.GetTime(),
			Status:       m.GetStatus(),
			StatusDetail: m.GetStatusDetail(),
		})
	}
	for _, t := range grpcRes.GetTimeline() {
		item := SessionTimelineItem{
			MsgID:  t.GetMsgId(),
			Events: make([]SessionProcessEvent, 0, len(t.GetEvents())),
		}
		for _, ev := range t.GetEvents() {
			item.Events = append(item.Events, SessionProcessEvent{
				ID:     ev.GetId(),
				Time:   ev.GetTime(),
				Title:  ev.GetTitle(),
				Detail: ev.GetDetail(),
			})
		}
		payload.Timeline = append(payload.Timeline, item)
	}
	if *jsonOut {
		printJSON(payload)
		return 0
	}
	for _, m := range payload.Messages {
		fmt.Printf("%s\t%s\t%s\n", m.Time, m.Role, m.Text)
	}
	return 0
}

func runSessionClear(repoRoot string, args []string) int {
	return runSessionMutation(repoRoot, args, "session-clear")
}

func runSessionDelete(repoRoot string, args []string) int {
	return runSessionMutation(repoRoot, args, "session-delete")
}

func runSessionsDeleteAll(repoRoot string, args []string) int {
	fs := flag.NewFlagSet("sessions-delete-all", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	grpcRes, err := tryDeleteAllSessionsViaGRPC(repoRoot)
	if err != nil {
		if *jsonOut {
			printJSONActionError("sessions-delete-all", "gateway_unreachable", formatGatewayUnavailable(err))
			return 1
		}
		fmt.Fprintf(os.Stderr, "sessions-delete-all failed: %s\n", formatGatewayUnavailable(err))
		return 1
	}
	if !grpcRes.GetOk() {
		if *jsonOut {
			printJSONActionError("sessions-delete-all", "grpc_delete_all_failed", grpcRes.GetError())
			return 1
		}
		fmt.Fprintf(os.Stderr, "sessions-delete-all failed: %s\n", grpcRes.GetError())
		return 1
	}
	if *jsonOut {
		printJSON(SessionMutationPayload{OK: true, Action: "sessions-delete-all"})
	} else {
		fmt.Println("all sessions deleted")
	}
	return 0
}

func runSessionMutation(repoRoot string, args []string, action string) int {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sessionKey := fs.String("session-key", "", "session key")
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	key := normalizeSessionKey(*sessionKey)
	if key == "" {
		if *jsonOut {
			printJSONActionError(action, "session_key_required", "--session-key is required")
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s requires --session-key\n", action)
		return 2
	}

	var (
		res *gatewayv1.SessionMutationResponse
		err error
	)
	switch action {
	case "session-clear":
		res, err = tryClearSessionViaGRPC(repoRoot, key)
	case "session-delete":
		res, err = tryDeleteSessionViaGRPC(repoRoot, key)
	default:
		if *jsonOut {
			printJSONActionError(action, "unsupported_action", "unsupported session mutation action")
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s failed: unsupported action\n", action)
		return 1
	}
	if err != nil {
		if *jsonOut {
			printJSONActionError(action, "gateway_unreachable", formatGatewayUnavailable(err))
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s failed: %s\n", action, formatGatewayUnavailable(err))
		return 1
	}
	if !res.GetOk() {
		if *jsonOut {
			printJSONActionError(action, "grpc_mutation_failed", res.GetError())
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s failed: %s\n", action, res.GetError())
		return 1
	}
	if *jsonOut {
		printJSON(SessionMutationPayload{OK: true, Action: action, SessionKey: key})
	} else {
		fmt.Printf("%s ok: %s\n", action, key)
	}
	return 0
}

func collectSessionMessages(cfg config.AppConfig, sessionKey string) ([]SessionMessageItem, []SessionTimelineItem, error) {
	records, err := loadInteractionRecords(cfg)
	if err != nil {
		return nil, nil, err
	}
	msgIDs := map[string]struct{}{}
	for _, rec := range records {
		kind := cleanAnyString(rec["kind"])
		msgID := cleanAnyString(rec["msg_id"])
		if msgID == "" {
			continue
		}
		if kind == "trace" && cleanAnyString(rec["stage"]) == "session_resolved" {
			if cleanAnyString(rec["session_key"]) == sessionKey {
				msgIDs[msgID] = struct{}{}
			}
		}
		if kind == "inbound_received" {
			profile, _ := rec["user_profile"].(map[string]any)
			channel := cleanAnyString(profile["channel"])
			threadID := cleanAnyString(profile["thread_id"])
			sender := cleanAnyString(rec["sender"])
			if buildSessionKey(channel, sender, threadID) == sessionKey {
				msgIDs[msgID] = struct{}{}
			}
		}
	}

	msgs := make([]SessionMessageItem, 0, len(msgIDs)*2)
	timelineMap := map[string][]SessionProcessEvent{}
	seenInbound := map[string]struct{}{}
	seenFinal := map[string]struct{}{}

	for idx, rec := range records {
		msgID := cleanAnyString(rec["msg_id"])
		if msgID == "" {
			continue
		}
		if _, ok := msgIDs[msgID]; !ok {
			continue
		}
		kind := cleanAnyString(rec["kind"])
		ts := cleanAnyString(rec["ts"])
		if ts == "" {
			ts = cleanAnyString(rec["time"])
		}
		if ts == "" {
			ts = time.Now().UTC().Format(time.RFC3339)
		}

		if kind == "inbound_received" {
			if _, ok := seenInbound[msgID]; ok {
				continue
			}
			text := cleanAnyString(rec["text"])
			if text == "" {
				continue
			}
			seenInbound[msgID] = struct{}{}
			msgs = append(msgs, SessionMessageItem{
				ID:          "persist-u-" + msgID,
				SourceMsgID: msgID,
				Role:        "user",
				Text:        text,
				Time:        ts,
			})
			continue
		}
		if kind == "trace" {
			title := cleanAnyString(rec["stage"])
			if title == "" {
				continue
			}
			detail := cleanAnyString(rec["text"])
			if detail == "" {
				detail = cleanAnyString(rec["error"])
			}
			timelineMap[msgID] = append(timelineMap[msgID], SessionProcessEvent{
				ID:     fmt.Sprintf("evt-%s-%d", msgID, idx),
				Time:   ts,
				Title:  title,
				Detail: detail,
			})
			continue
		}

		resultText := cleanAnyString(rec["result"])
		errText := cleanAnyString(rec["error"])
		status := cleanAnyString(rec["status"])
		role := "assistant"
		finalText := resultText
		if finalText == "" && errText != "" {
			finalText = errText
			role = "system"
		}
		if finalText == "" {
			continue
		}
		if _, ok := seenFinal[msgID]; ok {
			continue
		}
		seenFinal[msgID] = struct{}{}
		msgs = append(msgs, SessionMessageItem{
			ID:           "persist-a-" + msgID,
			SourceMsgID:  msgID,
			Role:         role,
			Text:         finalText,
			Time:         ts,
			StatusDetail: status,
		})
	}

	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Time < msgs[j].Time
	})

	keys := make([]string, 0, len(timelineMap))
	for k := range timelineMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	timeline := make([]SessionTimelineItem, 0, len(keys))
	for _, k := range keys {
		timeline = append(timeline, SessionTimelineItem{
			MsgID:  k,
			Events: timelineMap[k],
		})
	}
	return msgs, timeline, nil
}

func clearSessionMapping(cfg config.AppConfig, key string) error {
	return mutateSessionMap(cfg, func(m map[string]string) {
		delete(m, key)
	})
}

func deleteAllSessionMappings(cfg config.AppConfig) error {
	return mutateSessionMap(cfg, func(m map[string]string) {
		for k := range m {
			delete(m, k)
		}
	})
}

func mutateSessionMap(cfg config.AppConfig, mutator func(map[string]string)) error {
	store, err := storage.NewBackend(
		cfg.StorageBackend,
		cfg.StateFile,
		cfg.InteractionLogFile,
		cfg.ReportDir,
		cfg.StorageSQLitePath,
	)
	if err != nil {
		return err
	}
	st, err := store.LoadState()
	if err != nil {
		return err
	}
	if st.SessionMap == nil {
		st.SessionMap = map[string]string{}
	}
	mutator(st.SessionMap)
	return store.SaveState(st)
}

func normalizeSessionKey(v string) string {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "#"); idx > 0 {
		raw = raw[:idx]
	}
	return strings.TrimSpace(raw)
}

func cleanAnyString(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}
