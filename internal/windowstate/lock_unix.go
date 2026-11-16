//go:build !windows

package windowstate

import (
	"os"

	"golang.org/x/sys/unix"
)

var errLocked = unix.EWOULDBLOCK

func lock(name string, wait bool) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	flags := unix.LOCK_EX
	if !wait {
		flags |= unix.LOCK_NB
	}
	if err := unix.Flock(int(file.Fd()), flags); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
