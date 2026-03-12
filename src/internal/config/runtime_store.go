package config

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"cli-agent-gateway/internal/infra/envfile"
)

const runtimeConfigKVKey = "config.runtime"

func runtimeConfigDBPath(repoRoot string, envValues map[string]string) string {
	_ = repoRoot
	_ = envValues
	return DefaultRuntimePaths("").StorageSQLitePath
}

func loadRuntimeValues(repoRoot string) (map[string]string, error) {
	return loadRuntimeValuesFromPath(runtimeConfigDBPath(repoRoot, nil))
}

func loadRuntimeValuesFromPath(dbPath string) (map[string]string, error) {
	if strings.TrimSpace(dbPath) == "" {
		return map[string]string{}, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv_state (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);`); err != nil {
		return nil, err
	}
	row := db.QueryRow(`SELECT value_json FROM kv_state WHERE key=? LIMIT 1`, runtimeConfigKVKey)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveRuntimeValues(repoRoot string, updates map[string]*string) (string, error) {
	dbPath := runtimeConfigDBPath(repoRoot, nil)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return "", err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv_state (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, updated_at TEXT NOT NULL);`); err != nil {
		return "", err
	}
	values, err := loadRuntimeValuesFromPath(dbPath)
	if err != nil {
		return "", err
	}
	for key, ptr := range updates {
		k := normalizeKey(key)
		if ptr == nil || strings.TrimSpace(*ptr) == "" {
			delete(values, k)
			continue
		}
		values[k] = strings.TrimSpace(*ptr)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO kv_state(key, value_json, updated_at) VALUES(?, ?, ?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`, runtimeConfigKVKey, string(raw), now); err != nil {
		return "", err
	}
	return dbPath, nil
}

func migrateRuntimeKeys(repoRoot string, envValues map[string]string) error {
	updates := map[string]*string{}
	for key, value := range envValues {
		spec, ok := LookupSpec(key)
		if !ok || spec.Scope != ScopeRuntimeDB {
			continue
		}
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		copyValue := v
		updates[spec.Key] = &copyValue
	}
	if len(updates) == 0 {
		return nil
	}
	_, err := saveRuntimeValues(repoRoot, updates)
	return err
}

func migrateUserScopedKeys(repoRoot string, envValues map[string]string) error {
	updates := map[string]*string{}
	for key, value := range envValues {
		spec, ok := LookupSpec(key)
		if !ok || spec.Scope != ScopeUserEnv || spec.Key == "GATEWAYD_ADDR" {
			continue
		}
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		copyValue := v
		updates[spec.Key] = &copyValue
	}
	if len(updates) == 0 {
		return nil
	}
	return updateUserEnvValues(updates)
}

func userInitValues(workdir string, existing map[string]string) map[string]string {
	_ = workdir
	values := map[string]string{
		"CHANNEL_TYPE":  defaultChannelType,
		"ACP_AGENT_CMD": defaultACPAgentCmd,
		"ACP_PERMISSION_POLICY": defaultPermissionPolicy,
		"SMS_FETCH_CMD": defaultFetchCmd,
		"SMS_SEND_CMD": defaultSendCmd,
		"REMOTE_USER_ID": "tester",
		"ALLOWED_FROM": "tester",
		"IMESSAGE_FETCH_CMD": defaultIMessageFetchCmd,
		"IMESSAGE_SEND_CMD": defaultIMessageSendCmd,
		"CAG_GO_DEBUG": "1",
		"STORAGE_BACKEND": defaultStorageBackend,
	}
	for key := range values {
		if existingValue := strings.TrimSpace(existing[key]); existingValue != "" {
			values[key] = existingValue
		}
	}
	return values
}

func orderedUserInitKeys(values map[string]string) []string {
	ordered := []string{
		"CHANNEL_TYPE",
		"ACP_AGENT_CMD",
		"ACP_PERMISSION_POLICY",
		"SMS_FETCH_CMD",
		"SMS_SEND_CMD",
		"REMOTE_USER_ID",
		"ALLOWED_FROM",
		"IMESSAGE_FETCH_CMD",
		"IMESSAGE_SEND_CMD",
		"CAG_GO_DEBUG",
		"STORAGE_BACKEND",
	}
	for _, key := range []string{
		"GATEWAYD_ADDR",
		"DINGTALK_SEND_MODE",
		"DINGTALK_APP_KEY",
		"DINGTALK_APP_SECRET",
		"DINGTALK_AGENT_ID",
		"DINGTALK_BOT_WEBHOOK",
		"DINGTALK_BOT_SECRET",
		"DINGTALK_DEFAULT_TO_USER",
		"DINGTALK_TOKEN_URL",
		"DINGTALK_SEND_URL",
	} {
		if strings.TrimSpace(values[key]) != "" {
			ordered = append(ordered, key)
		}
	}
	return ordered
}

func listEntries(repoRoot string) ([]Entry, error) {
	repoEnv := map[string]string{}
	userEnv, err := loadUserEnv()
	if err != nil {
		return nil, err
	}
	runtimeValues, err := loadRuntimeValues(repoRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(orderedSpecs))
	for _, spec := range orderedSpecs {
		value, source := resolveEntryValue(spec, repoEnv, userEnv, runtimeValues)
		out = append(out, Entry{
			Key:    spec.Key,
			Value:  value,
			Scope:  spec.Scope,
			Source: source,
		})
	}
	return out, nil
}

func getEntry(repoRoot, key string) (Entry, error) {
	entries, err := listEntries(repoRoot)
	if err != nil {
		return Entry{}, err
	}
	normalized := normalizeKey(key)
	for _, entry := range entries {
		if entry.Key == normalized {
			return entry, nil
		}
	}
	return Entry{}, os.ErrNotExist
}

func resolveEntryValue(spec KeySpec, repoEnv, userEnv, runtimeValues map[string]string) (string, string) {
	if value, ok := os.LookupEnv(spec.Key); ok {
		return value, SourceProcessEnv
	}
	switch spec.Scope {
	case ScopeRepoEnv:
		if value := strings.TrimSpace(repoEnv[spec.Key]); value != "" {
			return value, SourceRepoEnv
		}
	case ScopeUserEnv:
		if value := strings.TrimSpace(userEnv[spec.Key]); value != "" {
			return value, SourceUserEnv
		}
	case ScopeRuntimeDB:
		if value := strings.TrimSpace(runtimeValues[spec.Key]); value != "" {
			return value, SourceRuntimeDB
		}
	}
	return spec.Default, SourceDefault
}

func loadUserEnv() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return map[string]string{}, nil
	}
	return envfile.Parse(filepath.Join(home, ".cag", ".env"))
}

func updateEnvFile(path string, updates map[string]*string, ordered []string, headers []string, removals map[string]struct{}) error {
	existing, err := envfile.Parse(path)
	if err != nil {
		return err
	}
	values := map[string]string{}
	for key, value := range existing {
		if _, drop := removals[key]; drop {
			continue
		}
		values[key] = value
	}
	for key, ptr := range updates {
		k := normalizeKey(key)
		if ptr == nil {
			delete(values, k)
			continue
		}
		v := strings.TrimSpace(*ptr)
		if v == "" {
			delete(values, k)
			continue
		}
		values[k] = v
	}
	return envfile.Write(path, values, ordered, headers)
}

func orderedUserEnvKeys(values map[string]string) []string {
	ordered := []string{
		"GATEWAYD_ADDR",
		"DINGTALK_SEND_MODE",
		"DINGTALK_APP_KEY",
		"DINGTALK_APP_SECRET",
		"DINGTALK_AGENT_ID",
		"DINGTALK_BOT_WEBHOOK",
		"DINGTALK_BOT_SECRET",
		"DINGTALK_DEFAULT_TO_USER",
		"DINGTALK_TOKEN_URL",
		"DINGTALK_SEND_URL",
	}
	extra := make([]string, 0)
	for key := range values {
		skip := false
		for _, orderedKey := range ordered {
			if key == orderedKey {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(ordered, extra...)
}
