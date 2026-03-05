//go:build windows

package main

import (
	"os"
	"os/exec"
)

func configureDetachedProcess(cmd *exec.Cmd) {}

func signalTerminate(proc *os.Process) error {
	return proc.Kill()
}

func signalKill(proc *os.Process) error {
	return proc.Kill()
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(os.Interrupt)
	return err == nil
}
