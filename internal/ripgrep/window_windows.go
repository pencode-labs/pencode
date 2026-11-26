package ripgrep

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func init() {
	configureCommand = func(cmd *exec.Cmd) {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	}
}
