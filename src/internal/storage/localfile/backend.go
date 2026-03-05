package localfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.MkdirAll(b.reportDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102_150405")
	name := fmt.Sprintf("%s_%s.json", ts, strings.TrimSpace(messageID))
	if strings.TrimSpace(messageID) == "" {
		name = fmt.Sprintf("%s_report.json", ts)
	}
	path := filepath.Join(b.reportDir, name)
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func emptyState() storageapi.StateData {
	return storageapi.StateData{ProcessedIDs: []string{}, SessionMap: map[string]string{}, Inflight: map[string]any{}}
}

func normalizeState(d *storageapi.StateData) {
	if d.ProcessedIDs == nil {
		d.ProcessedIDs = []string{}
	}
	if d.SessionMap == nil {
		d.SessionMap = map[string]string{}
	}
	if d.Inflight == nil {
		d.Inflight = map[string]any{}
	}
}
