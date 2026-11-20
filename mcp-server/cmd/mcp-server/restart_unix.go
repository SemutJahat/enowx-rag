//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execSelf replaces this process with a fresh copy of the same binary, same
// arguments and environment (same PID, so Docker and systemd keep tracking
// it). Go opens every file descriptor close-on-exec, so the listener and the
// database connections don't leak into the new process.
func execSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

const canRestart = true
