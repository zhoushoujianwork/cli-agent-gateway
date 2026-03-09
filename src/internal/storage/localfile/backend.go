package localfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"cli-agent-gateway/internal/storageapi"
)

type Backend struct {
	stateFile          string
	interactionLogFile string
	reportDir          string
}

func New(stateFile, interactionLogFile, reportDir string) *Backend {
	return &Backend{stateFile: stateFile, interactionLogFile: interactionLogFile, reportDir: reportDir}
}

func (b *Backend) LoadState() (storageapi.StateData, error) {
	raw, err := os.ReadFile(b.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyState(), nil
		}
		return storageapi.StateData{}, err
	}
	if len(raw) == 0 {
		return emptyState(), nil
	}
	var d storageapi.StateData
	if err := json.Unmarshal(raw, &d); err != nil {
		return storageapi.StateData{}, err
	}
	normalizeState(&d)
	return d, nil
}

func (b *Backend) SaveState(d storageapi.StateData) error {
	normalizeState(&d)
	if err := os.MkdirAll(filepath.Dir(b.stateFile), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.stateFile, append(raw, '\n'), 0o644)
}

func (b *Backend) AppendInteraction(node map[string]any) error {
	if strings.TrimSpace(b.interactionLogFile) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(b.interactionLogFile), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(b.interactionLogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return err
}

func (b *Backend) WriteReport(report map[string]any, messageID string) (string, error) {
	if strings.TrimSpace(b.reportDir) == "" {
		return "", nil
	}
	reportDir := b.reportDir
	if sessionKey := extractReportSessionKey(report); sessionKey != "" {
		reportDir = filepath.Join(reportDir, "sessions", sessionKey)
	}
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102_150405")
	name := fmt.Sprintf("%s_%s.json", ts, strings.TrimSpace(messageID))
	if strings.TrimSpace(messageID) == "" {
		name = fmt.Sprintf("%s_report.json", ts)
	}
	path := filepath.Join(reportDir, name)
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func extractReportSessionKey(report map[string]any) string {
	req, ok := report["request"]
	if !ok || req == nil {
		return ""
	}
	if m, ok := req.(map[string]any); ok {
		return strings.TrimSpace(fmt.Sprint(m["session_key"]))
	}
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName("SessionKey")
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(f.String())
}

func emptyState() storageapi.StateData {
	return storageapi.StateData{
		ProcessedIDs:   []string{},
		SessionMap:     map[string]string{},
		SessionMeta:    map[string]storageapi.SessionMetaRecord{},
		SessionDeleted: map[string]string{},
		Sessions:       map[string]storageapi.SessionRecord{},
		Bindings:       map[string]storageapi.BindingRecord{},
		RuntimeIndex:   map[string]storageapi.RuntimeRecord{},
		Unassigned:     map[string]storageapi.ConversationRecord{},
		UserAccess:     map[string]storageapi.UserAccessRecord{},
		Inflight:       map[string]any{},
	}
}

func normalizeState(d *storageapi.StateData) {
	if d.ProcessedIDs == nil {
		d.ProcessedIDs = []string{}
	}
	if d.SessionMap == nil {
		d.SessionMap = map[string]string{}
	}
	if d.SessionMeta == nil {
		d.SessionMeta = map[string]storageapi.SessionMetaRecord{}
	}
	if d.SessionDeleted == nil {
		d.SessionDeleted = map[string]string{}
	}
	if d.Sessions == nil {
		d.Sessions = map[string]storageapi.SessionRecord{}
	}
	if d.Bindings == nil {
		d.Bindings = map[string]storageapi.BindingRecord{}
	}
	if d.RuntimeIndex == nil {
		d.RuntimeIndex = map[string]storageapi.RuntimeRecord{}
	}
	if d.Unassigned == nil {
		d.Unassigned = map[string]storageapi.ConversationRecord{}
	}
	if d.UserAccess == nil {
		d.UserAccess = map[string]storageapi.UserAccessRecord{}
	}
	if d.Inflight == nil {
		d.Inflight = map[string]any{}
	}
}
