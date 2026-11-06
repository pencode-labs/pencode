//go:build !windows

package updater

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureHelper(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func waitParent(pid int) error {
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
