package clipboard

import (
	"context"
	"errors"
	"testing"
	"unsafe"
)

func TestNativeClipboardMemory(t *testing.T) {
	if id, err := pngFormat(); id == 0 || err != nil {
		t.Fatalf("register PNG: %d, %v", id, err)
	}
	// Exercise native memory access without changing the user's clipboard.
	handle, _, err := kernel32.NewProc("GlobalAlloc").Call(2, 4)
	if handle == 0 {
		t.Fatal(err)
	}
	defer kernel32.NewProc("GlobalFree").Call(handle)
	ptr := lockClipboardMemory(handle)
	if ptr == nil {
		t.Fatal("GlobalLock failed")
	}
	defer globalUnlock.Call(handle)
	data := unsafe.Slice((*byte)(ptr), 4)
	copy(data, "test")
	if string(data) != "test" {
		t.Fatal("native buffer read failed")
	}
}

func TestReadImageCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if data, err := ReadImage(ctx); data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v, %v", data, err)
	}
}
