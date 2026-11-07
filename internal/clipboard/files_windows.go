package clipboard

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var dragQueryFile = windows.NewLazySystemDLL("shell32.dll").NewProc("DragQueryFileW")

func HasFiles() bool {
	ok, _, _ := isClipboardFormatAvailable.Call(15) // CF_HDROP
	return ok != 0
}

// ReadFiles borrows CF_HDROP without changing the clipboard or its drop effect.
func ReadFiles(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !HasFiles() {
		return nil, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for {
		if ok, _, _ := openClipboard.Call(0); ok != 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer closeClipboard.Call()
	handle, _, err := getClipboardData.Call(15)
	if handle == 0 {
		return nil, fmt.Errorf("GetClipboardData: %w", err)
	}
	return droppedFiles(handle)
}

func droppedFiles(handle uintptr) ([]string, error) {
	count, _, _ := dragQueryFile.Call(handle, 0xffffffff, 0, 0)
	if count > 4096 {
		return nil, fmt.Errorf("剪贴板文件数量超过 4096")
	}
	files := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		size, _, _ := dragQueryFile.Call(handle, i, 0, 0)
		if size == 0 || size > 32767 {
			return nil, fmt.Errorf("无效的剪贴板文件路径")
		}
		buffer := make([]uint16, size+1)
		n, _, _ := dragQueryFile.Call(handle, i, uintptr(unsafe.Pointer(&buffer[0])), size+1)
		if n != size {
			return nil, fmt.Errorf("无法读取剪贴板文件路径")
		}
		files = append(files, windows.UTF16ToString(buffer))
	}
	return files, nil
}
