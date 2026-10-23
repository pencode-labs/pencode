//go:build !linux && !darwin && !windows

package server

import "os/exec"

func hideServiceWindow(*exec.Cmd) {}
