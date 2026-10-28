//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}
func ownProcesses() error  { return nil }

// Replace the launcher so the host owns the actual server's lifetime and pipes.
func launch(path string, args []string) error {
	return syscall.Exec(path, append([]string{path}, args...), os.Environ())
}
