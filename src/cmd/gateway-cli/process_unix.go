//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func signalTerminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

func signalKill(proc *os.Process) error {
	return proc.Signal(syscall.SIGKILL)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
