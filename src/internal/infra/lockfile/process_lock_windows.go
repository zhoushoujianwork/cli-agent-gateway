//go:build windows

package lockfile

import "errors"

type ProcessLock struct {
	path string
}

func NewProcessLock(path string) *ProcessLock {
	return &ProcessLock{path: path}
}

func (l *ProcessLock) Acquire() (bool, error) {
	return false, errors.New("process lock on windows is not implemented yet")
}

func (l *ProcessLock) WriteMetadata(data map[string]any) error {
	return errors.New("process lock on windows is not implemented yet")
}

func (l *ProcessLock) Close() error {
	return nil
}
