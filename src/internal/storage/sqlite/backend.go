package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"cli-agent-gateway/internal/storageapi"
)

type Backend struct {
	db        *sql.DB
	reportDir string
}

func New(dbPath, reportDir string) (*Backend, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("empty sqlite db path")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + dbPath
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	b := &Backend{db: db, reportDir: reportDir}
	if err := b.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return b, nil
}

func (b *Backend) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS kv_state (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS interactions (id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL, payload_json TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS reports (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, ts TEXT NOT NULL, payload_json TEXT NOT NULL, file_path TEXT);`,
		`CREATE INDEX IF NOT EXISTS idx_interactions_ts ON interactions(ts);`,
		`CREATE INDEX IF NOT EXISTS idx_reports_ts ON reports(ts);`,
	}
	for _, s := range stmts {
		if _, err := b.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func applyPragmas(db *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA busy_timeout=5000;`,
		`PRAGMA foreign_keys=ON;`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) LoadState() (storageapi.StateData, error) {
	row := b.db.QueryRow(`SELECT value_json FROM kv_state WHERE key='state' LIMIT 1`)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return emptyState(), nil
		}
		return storageapi.StateData{}, err
	}
	var d storageapi.StateData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return storageapi.StateData{}, err
	}
	normalizeState(&d)
	return d, nil
}

func (b *Backend) SaveState(d storageapi.StateData) error {
	normalizeState(&d)
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = b.db.Exec(`INSERT INTO kv_state(key, value_json, updated_at) VALUES('state', ?, ?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`, string(raw), now)
	return err
}

func (b *Backend) AppendInteraction(node map[string]any) error {
	raw, err := json.Marshal(node)
	if err != nil {
		return err
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	if v, ok := node["ts"].(string); ok && strings.TrimSpace(v) != "" {
		ts = v
	}
	_, err = b.db.Exec(`INSERT INTO interactions(ts, payload_json) VALUES(?, ?)`, ts, string(raw))
	return err
}

func (b *Backend) WriteReport(report map[string]any, messageID string) (string, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	filePath := ""
	if strings.TrimSpace(b.reportDir) != "" {
		filePath, _ = writeReportFile(b.reportDir, report, messageID)
	}
	_, err = b.db.Exec(`INSERT INTO reports(message_id, ts, payload_json, file_path) VALUES(?, ?, ?, ?)`, strings.TrimSpace(messageID), ts, string(raw), filePath)
	if err != nil {
		return "", err
	}
	return filePath, nil
}

func writeReportFile(reportDir string, report map[string]any, messageID string) (string, error) {
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102_150405")
	name := fmt.Sprintf("%s_%s.json", ts, strings.TrimSpace(messageID))
	if strings.TrimSpace(messageID) == "" {
		name = fmt.Sprintf("%s_report.json", ts)
	}
	path := filepath.Join(reportDir, name)
	pretty, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(pretty, '\n'), 0o644); err != nil {
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
