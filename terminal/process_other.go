//go:build !linux && !darwin && !windows

package terminal

import (
	"fmt"
	"os/exec"
)

func startNative(string, int, int) (Process, error) {
	return nil, fmt.Errorf("native terminal is unavailable on this platform")
}
func startNativeCommand(*exec.Cmd, int, int) (Process, error) {
	return nil, fmt.Errorf("native terminal is unavailable on this platform")
}
