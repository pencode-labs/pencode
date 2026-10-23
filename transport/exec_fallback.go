//go:build !linux && !darwin && !windows

package transport

import "os/exec"

func hideWindow(*exec.Cmd)          {}
func terminate(cmd *exec.Cmd) error { return cmd.Process.Kill() }
