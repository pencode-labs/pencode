//go:build linux || darwin

package transport

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd)      { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func terminate(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
