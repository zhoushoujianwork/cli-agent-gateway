//go:build !windows

package lockfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type Status struct {
	Locked         bool
	OwnerPID       *int
	OwnerStartedAt *string
	Metadata       map[string]any
}

func Inspect(path string) (Status, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return Status{}, err
	}

	metadata := readMetadata(resolved)
	ownerPID, ownerStarted := ownerFromMetadata(metadata)

	fd, err := os.OpenFile(resolved, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return Status{}, err
	}
	defer fd.Close()

	err = syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
		if ownerPID != nil && !pidRunning(*ownerPID) {
			ownerPID = nil
			ownerStarted = nil
		}
		return Status{Locked: false, OwnerPID: ownerPID, OwnerStartedAt: ownerStarted, Metadata: metadata}, nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return Status{}, err
	}

	return Status{Locked: true, OwnerPID: ownerPID, OwnerStartedAt: ownerStarted, Metadata: metadata}, nil
}

func resolvePath(path string) (string, error) {
	cleaned := expandUser(path)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func expandUser(path string) string {
	if path == "~" {
		h, err := os.UserHomeDir()
		if err == nil {
			return h
		}
	}
	if len(path) > 2 && path[:2] == "~/" {
		h, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(h, path[2:])
		}
	}
	return path
}

func readMetadata(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]any{}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

func ownerFromMetadata(metadata map[string]any) (*int, *string) {
	var pid *int
	if v, ok := metadata["pid"]; ok {
		switch t := v.(type) {
		case float64:
			n := int(t)
			pid = &n
		case int:
			n := t
			pid = &n
		}
	}
	var started *string
	if v, ok := metadata["started_at"]; ok {
		if s, ok := v.(string); ok {
			started = &s
		}
	}
	return pid, started
}

func pidRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
