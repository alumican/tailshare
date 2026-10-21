//go:build windows

package main

import (
	"os"
	"syscall"
)

const stillActive = 259

// Whether a process is running
func alive(pid int) bool {
	handle, err := syscall.OpenProcess(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	return syscall.GetExitCodeProcess(handle, &code) == nil && code == stillActive
}

// Windows cannot ask another process to stop gracefully; it is killed, and --stop cleans up for it
func terminate(pid int) {
	if process, err := os.FindProcess(pid); err == nil {
		process.Kill()
	}
}

// A process detached from this console, in a group of its own
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000008 /* DETACHED_PROCESS */ | syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}

// The tailscale serve a share runs, in a process group of its own: a Ctrl-C reaches tailshare
// alone, which then stops it
func serveProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}
