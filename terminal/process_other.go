//go:build !linux && !darwin && !windows

package terminal

import "fmt"

func startNative(string, int, int) (Process, error) {
	return nil, fmt.Errorf("native terminal is unavailable on this platform")
}
