//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow stops `go env` from opening a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
