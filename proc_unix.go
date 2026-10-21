//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// Whether a process is running
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Ask a share to stop; it cleans up after itself
func terminate(pid int) {
	syscall.Kill(pid, syscall.SIGTERM)
}

// A process of its own session, so it outlives the terminal it was started from
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
