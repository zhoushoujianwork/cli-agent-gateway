//go:build !windows

package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type ProcessLock struct {
	path string
	file *os.File
}

func NewProcessLock(path string) *ProcessLock {
	return &ProcessLock{path: path}
}

func (l *ProcessLock) Acquire() (bool, error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return false, nil
		}
		return false, err
	}
	l.file = f
	return true, nil
}

func (l *ProcessLock) WriteMetadata(data map[string]any) error {
	if l.file == nil {
		return os.ErrInvalid
	}
	if data == nil {
		data = map[string]any{}
	}
	data["pid"] = os.Getpid()
	if _, ok := data["started_at"]; !ok {
		data["started_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := l.file.Write(append(b, '\n')); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *ProcessLock) Close() error {
	if l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
