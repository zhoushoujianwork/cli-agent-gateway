package storage

import (
	"fmt"
	"strings"

	"cli-agent-gateway/internal/storage/localfile"
	"cli-agent-gateway/internal/storage/sqlite"
)

func NewBackend(backend, stateFile, interactionLogFile, reportDir, sqlitePath string) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", "localfile", "file", "json":
		return localfile.New(stateFile, interactionLogFile, reportDir), nil
	case "sqlite":
		return sqlite.New(sqlitePath, reportDir)
	default:
		return nil, fmt.Errorf("unsupported storage backend: %s", backend)
	}
}
