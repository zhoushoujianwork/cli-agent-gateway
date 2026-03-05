//go:build windows

package lockfile

import "errors"

type Status struct {
	Locked         bool
	OwnerPID       *int
	OwnerStartedAt *string
	Metadata       map[string]any
}

func Inspect(path string) (Status, error) {
	return Status{}, errors.New("lock inspection on windows is not implemented yet")
}
