package desktopopen

import (
	"github.com/onix-engine/onix/window"
	"golang.org/x/sys/windows"
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	isIconic            = user32.NewProc("IsIconic")
	showWindow          = user32.NewProc("ShowWindow")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
)

func Focus(context *window.Context) {
	if hwnd := context.NativeHandle(); hwnd != 0 {
		if minimized, _, _ := isIconic.Call(hwnd); minimized != 0 {
			showWindow.Call(hwnd, windows.SW_RESTORE)
		}
		setForegroundWindow.Call(hwnd)
	}
}
