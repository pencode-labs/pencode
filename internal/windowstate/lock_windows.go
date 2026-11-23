package windowstate

import (
	"encoding/binary"
	"os"

	"golang.org/x/sys/windows"
)

var errLocked = windows.ERROR_LOCK_VIOLATION

func lock(name string, wait bool) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{}); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

var allowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// The foreground process launching a second instance can pass its permission
// to activate a window to the GUI. The dispatch lock protects the owner's PID.
func desktopForeground(guard *os.File, owner bool) {
	var data [4]byte
	if owner {
		binary.LittleEndian.PutUint32(data[:], uint32(os.Getpid()))
		_, _ = guard.WriteAt(data[:], 0)
	} else if _, err := guard.ReadAt(data[:], 0); err == nil {
		allowSetForegroundWindow.Call(uintptr(binary.LittleEndian.Uint32(data[:])))
	}
}
