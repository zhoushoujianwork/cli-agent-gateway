package sessionctl

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cli-agent-gateway/internal/agents/acp"
	"cli-agent-gateway/internal/config"
	"cli-agent-gateway/internal/core"
	"cli-agent-gateway/internal/infra/proclog"
	"cli-agent-gateway/internal/storage"

	_ "modernc.org/sqlite"
)

type RuntimeManager struct {
	repoRoot   string
	mu         sync.Mutex
	runtimes   map[string]*managedSessionRuntime
	recovered  bool
	recoverErr error
}

type managedSessionRuntime struct {
	runtime *acp.SessionRuntime
	cfg     config.AppConfig
}

func NewRuntimeManager(repoRoot string) *RuntimeManager {
	return &RuntimeManager{
		repoRoot: strings.TrimSpace(repoRoot),
		runtimes: map[string]*managedSessionRuntime{},
	}
}

func (m *RuntimeManager) EnsureRecovered() error {
	m.mu.Lock()
	if m.recovered {
		err := m.recoverErr
		m.mu.Unlock()
		return err
	}
	m.recovered = true
	m.mu.Unlock()

	cfg, err := config.Load(m.repoRoot, "")
	if err != nil {
		m.setRecoverErr(err)
		return err
	}
	store, err := OpenStore(cfg)
	if err != nil {
		m.setRecoverErr(err)
		return err
	}
	state, err := store.Load()
	if err != nil {
		m.setRecoverErr(err)
		return err
	}
	for key, rt := range state.RuntimeIndex {
		if !rt.Attached {
			continue
		}
		session := state.Sessions[key]
		if strings.TrimSpace(session.Key) == "" || strings.EqualFold(strings.TrimSpace(session.Status), "archived") {
			continue
		}
		req := BuildRuntimeTaskRequest(session, "gatewayd-recover", "", "gatewayd-recover", "recover-"+key, nil)
		if _, err := m.attachRuntime(cfg, session, req, true); err != nil {
			proclog.Warn("gatewayd", map[string]any{
				"event":       "runtime_recover_failed",
				"session_key": key,
				"err":         err.Error(),
			})
		}
	}
	return nil
}

func (m *RuntimeManager) Attach(cfg config.AppConfig, session storage.SessionRecord, source string, metadata map[string]any) (storage.RuntimeRecord, error) {
	if err := m.EnsureRecovered(); err != nil {
		return storage.RuntimeRecord{}, err
	}
	req := BuildRuntimeTaskRequest(session, source, "", source, "attach-"+session.Key, metadata)
	return m.attachRuntime(cfg, session, req, false)
}

func (m *RuntimeManager) Send(cfg config.AppConfig, session storage.SessionRecord, req core.TaskRequest) (core.TaskResult, storage.RuntimeRecord, error) {
	if err := m.EnsureRecovered(); err != nil {
		return core.TaskResult{}, storage.RuntimeRecord{}, err
	}
	rt, err := m.attachRuntime(cfg, session, req, false)
	if err != nil {
		return core.TaskResult{}, storage.RuntimeRecord{}, err
	}
	m.mu.Lock()
	entry := m.runtimes[session.Key]
	m.mu.Unlock()
	if entry == nil {
		return core.TaskResult{}, storage.RuntimeRecord{}, fmt.Errorf("runtime missing for session %s", session.Key)
	}
	result, err := entry.runtime.Execute(req)
	return result, rt, err
}

func (m *RuntimeManager) Clear(cfg config.AppConfig, session storage.SessionRecord, source string) (storage.RuntimeRecord, error) {
	if err := m.EnsureRecovered(); err != nil {
		return storage.RuntimeRecord{}, err
	}
	req := BuildRuntimeTaskRequest(session, source, "", source, "clear-"+session.Key, nil)
	m.mu.Lock()
	entry := m.runtimes[session.Key]
	if entry == nil {
		entry = &managedSessionRuntime{
			cfg: cfg,
			runtime: acp.NewSessionRuntime(
				cfg.ACPAgentCmd,
				session.Workdir,
				cfg.PermissionPolicy,
				cfg.TimeoutSec,
				cfg.InitializeTimeoutSec,
				cfg.SessionNewTimeoutSec,
				cfg.SessionNewRetries,
				cfg.SessionNewBackoffSec,
			),
		}
		m.runtimes[session.Key] = entry
	}
	m.mu.Unlock()
	if _, err := entry.runtime.Clear(req); err != nil {
		return storage.RuntimeRecord{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return storage.RuntimeRecord{
		SessionKey:     session.Key,
		Attached:       true,
		Status:         "attached",
		UpdatedAt:      now,
		LastAttachedAt: now,
	}, nil
}

func (m *RuntimeManager) Detach(sessionKey string) error {
	if err := m.EnsureRecovered(); err != nil {
		return err
	}
	m.mu.Lock()
	entry := m.runtimes[sessionKey]
	delete(m.runtimes, sessionKey)
	m.mu.Unlock()
	if entry == nil {
		return nil
	}
	return entry.runtime.Close()
}

func (m *RuntimeManager) Snapshot() []storage.RuntimeRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.RuntimeRecord, 0, len(m.runtimes))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for key := range m.runtimes {
		out = append(out, storage.RuntimeRecord{
			SessionKey: key,
			Attached:   true,
			Status:     "attached",
			UpdatedAt:  now,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionKey < out[j].SessionKey })
	return out
}

func AppendSessionMessageRecord(cfg config.AppConfig, sessionKey, msgID, role, text, status, source, sourceMsgID string) error {
	store, err := OpenStore(cfg)
	if err != nil {
		return err
	}
	return store.backend.AppendInteraction(map[string]any{
		"kind":          "session_message",
		"session_key":   sessionKey,
		"msg_id":        msgID,
		"source_msg_id": sourceMsgID,
		"role":          role,
		"text":          text,
		"status":        status,
		"source":        source,
		"ts":            time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func AppendSessionTraceRecord(cfg config.AppConfig, sessionKey, msgID, title, detail string) error {
	store, err := OpenStore(cfg)
	if err != nil {
		return err
	}
	return store.backend.AppendInteraction(map[string]any{
		"kind":        "session_trace",
		"session_key": sessionKey,
		"msg_id":      msgID,
		"title":       title,
		"detail":      detail,
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func CollectSessionMessages(cfg config.AppConfig, sessionKey string) ([]SessionMessageItem, []SessionTimelineItem, error) {
	records, err := loadInteractionRecords(cfg)
	if err != nil {
		return nil, nil, err
	}
	msgs := make([]SessionMessageItem, 0, 32)
	timelineByMsg := map[string][]SessionProcessEvent{}
	for idx, rec := range records {
		if NormalizeSessionKey(CleanString(rec["session_key"])) != sessionKey {
			continue
		}
		kind := CleanString(rec["kind"])
		ts := CleanString(rec["ts"])
		switch kind {
		case "session_message":
			msgs = append(msgs, SessionMessageItem{
				ID:           CleanString(rec["msg_id"]),
				SourceMsgID:  CleanString(rec["source_msg_id"]),
				Role:         CleanString(rec["role"]),
				Text:         CleanString(rec["text"]),
				Time:         ts,
				Status:       CleanString(rec["status"]),
				StatusDetail: CleanString(rec["source"]),
			})
		case "session_trace":
			msgID := CleanString(rec["msg_id"])
			timelineByMsg[msgID] = append(timelineByMsg[msgID], SessionProcessEvent{
				ID:     fmt.Sprintf("evt-%s-%d", msgID, idx),
				Time:   ts,
				Title:  CleanString(rec["title"]),
				Detail: CleanString(rec["detail"]),
			})
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Time < msgs[j].Time })
	keys := make([]string, 0, len(timelineByMsg))
	for key := range timelineByMsg {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	timeline := make([]SessionTimelineItem, 0, len(keys))
	for _, key := range keys {
		timeline = append(timeline, SessionTimelineItem{
			MsgID:  key,
			Events: timelineByMsg[key],
		})
	}
	return msgs, timeline, nil
}

func ResolveRuntimeLogPath(repoRoot string) string {
	path := resolveGatewaydLogPath(repoRoot)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(repoRoot, path)
}

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

func (m *RuntimeManager) setRecoverErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recoverErr = err
}

func (m *RuntimeManager) attachRuntime(cfg config.AppConfig, session storage.SessionRecord, req core.TaskRequest, recovered bool) (storage.RuntimeRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.runtimes[session.Key]
	if entry == nil {
		entry = &managedSessionRuntime{
			cfg: cfg,
			runtime: acp.NewSessionRuntime(
				cfg.ACPAgentCmd,
				session.Workdir,
				cfg.PermissionPolicy,
				cfg.TimeoutSec,
				cfg.InitializeTimeoutSec,
				cfg.SessionNewTimeoutSec,
				cfg.SessionNewRetries,
				cfg.SessionNewBackoffSec,
			),
		}
		m.runtimes[session.Key] = entry
	}
	if _, err := entry.runtime.Attach(req); err != nil {
		return storage.RuntimeRecord{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := storage.RuntimeRecord{
		SessionKey:     session.Key,
		Attached:       true,
		Status:         "attached",
		UpdatedAt:      now,
		LastAttachedAt: now,
	}
	if recovered {
		record.LastRecoveredAt = now
	}
	return record, nil
}

func BuildRuntimeTaskRequest(session storage.SessionRecord, source, text, sender, messageID string, metadata map[string]any) core.TaskRequest {
	out := map[string]any{
		"workdir": session.Workdir,
		"source":  strings.TrimSpace(source),
	}
	for k, v := range metadata {
		out[k] = v
	}
	return core.TaskRequest{
		TraceID:    nonEmpty(strings.TrimSpace(messageID), "runtime-"+session.Key),
		SessionKey: session.Key,
		UserText:   text,
		Sender:     nonEmpty(strings.TrimSpace(sender), "gatewayd"),
		Channel:    nonEmpty(strings.TrimSpace(source), "session"),
		ThreadID:   "",
		Metadata:   out,
	}
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func CleanString(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}

func loadInteractionRecords(cfg config.AppConfig) ([]map[string]any, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.StorageBackend)) {
	case "localfile":
		return loadInteractionRecordsFromFile(cfg.InteractionLogFile)
	default:
		return loadInteractionRecordsFromSQLite(cfg.StorageSQLitePath)
	}
}

func loadInteractionRecordsFromFile(path string) ([]map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var node map[string]any
		if err := json.Unmarshal([]byte(line), &node); err != nil {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func loadInteractionRecordsFromSQLite(dbPath string) ([]map[string]any, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT payload_json FROM interactions ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var node map[string]any
		if err := json.Unmarshal([]byte(raw), &node); err != nil {
			continue
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func resolveGatewaydLogPath(repoRoot string) string {
	_ = repoRoot
	baseDir := filepath.Join(config.CAGHomeDir(), "gatewayd")
	if v := strings.TrimSpace(os.Getenv("GATEWAYD_LOG_FILE")); v != "" {
		if filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(baseDir, v)
	}
	return filepath.Join(baseDir, "gatewayd.log")
}
