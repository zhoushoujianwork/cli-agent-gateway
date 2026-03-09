package acp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"cli-agent-gateway/internal/core"
)

// SessionRuntime keeps a single ACP client/session alive for repeated prompts.
type SessionRuntime struct {
	adapter    *Adapter
	sessionID  string
	sessionKey string
	workdir    string
	mu         sync.Mutex
}

func NewSessionRuntime(command, cwd, permissionPolicy string, timeoutSec, initializeTimeoutSec, sessionNewTimeoutSec, sessionNewRetries int, sessionNewBackoffSec float64) *SessionRuntime {
	return &SessionRuntime{
		adapter: NewAdapter(command, cwd, permissionPolicy, timeoutSec, initializeTimeoutSec, sessionNewTimeoutSec, sessionNewRetries, sessionNewBackoffSec),
		workdir: strings.TrimSpace(cwd),
	}
}

func (r *SessionRuntime) SessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID
}

func (r *SessionRuntime) Attach(req core.TaskRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attachLocked(req)
}

func (r *SessionRuntime) Execute(req core.TaskRequest) (core.TaskResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessionID, err := r.attachLocked(req)
	if err != nil {
		return core.TaskResult{}, err
	}
	return r.adapter.runPrompt(req, sessionID)
}

func (r *SessionRuntime) Clear(req core.TaskRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.adapter.ensureReady(); err != nil {
		return "", err
	}
	r.sessionID = ""
	return r.attachLocked(req)
}

func (r *SessionRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = ""
	return r.adapter.Close()
}

func (r *SessionRuntime) attachLocked(req core.TaskRequest) (string, error) {
	if err := r.adapter.ensureReady(); err != nil {
		return "", err
	}
	if strings.TrimSpace(r.sessionID) != "" {
		return r.sessionID, nil
	}
	sessionID, err := r.adapter.createSession(req)
	if err != nil {
		return "", err
	}
	r.sessionID = strings.TrimSpace(sessionID)
	r.sessionKey = strings.TrimSpace(req.SessionKey)
	if wd := strings.TrimSpace(fmt.Sprint(req.Metadata["workdir"])); wd != "" {
		r.workdir = wd
	}
	return r.sessionID, nil
}

func (r *SessionRuntime) RuntimeInfo() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]any{
		"session_key": r.sessionKey,
		"workdir":     r.workdir,
	}
	if strings.TrimSpace(r.sessionID) != "" {
		out["session_id"] = r.sessionID
	}
	out["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	return out
}
