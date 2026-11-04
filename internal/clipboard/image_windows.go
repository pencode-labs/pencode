package clipboard

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/windows"
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	globalSize                 = kernel32.NewProc("GlobalSize")
	lockClipboardMemory        func(uintptr) unsafe.Pointer
	pngFormat                  = sync.OnceValues(func() (uintptr, error) {
		// Keep the native pointer typed across the call (safe for checkptr/GC).
		purego.RegisterFunc(&lockClipboardMemory, globalLock.Addr())
		name := [...]uint16{'P', 'N', 'G', 0}
		id, _, err := registerClipboardFormat.Call(uintptr(unsafe.Pointer(&name[0])))
		if id == 0 {
			return 0, err
		}
		return id, nil
	})
)

// ReadImage returns PNG bytes, or nil when the clipboard contains no image.
func ReadImage(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	png, err := pngFormat()
	if err != nil {
		return nil, err
	}
	format := uintptr(0)
	for _, candidate := range [...]uintptr{png, 17, 8} { // PNG, CF_DIBV5, CF_DIB.
		if available, _, _ := isClipboardFormatAvailable.Call(candidate); available != 0 {
			format = candidate
			break
		}
	}
	if format == 0 {
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
	handle, _, err := getClipboardData.Call(format)
	if handle == 0 {
		return nil, fmt.Errorf("GetClipboardData: %w", err)
	}
	size, _, err := globalSize.Call(handle)
	if size == 0 {
		return nil, fmt.Errorf("GlobalSize: %w", err)
	}
	if size > maxImageBytes {
		return nil, errImageTooLarge
	}
	ptr := lockClipboardMemory(handle)
	if ptr == nil {
		return nil, errors.New("GlobalLock failed")
	}
	defer globalUnlock.Call(handle)
	data := unsafe.Slice((*byte)(ptr), int(size))
	if format == png {
		return append([]byte(nil), data...), nil
	}
	return dibPNG(data)
}
