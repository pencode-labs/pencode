package clipboard

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"unicode/utf16"
	"unsafe"
)

func TestDroppedFilesUnicodeAndMultiplePaths(t *testing.T) {
	if _, err := pngFormat(); err != nil {
		t.Fatal(err)
	}
	want := []string{`C:\资料\中文 文件.txt`, `C:\other\folder`}
	var names []uint16
	for _, name := range want {
		names = append(names, utf16.Encode([]rune(name))...)
		names = append(names, 0)
	}
	names = append(names, 0)
	size := 20 + len(names)*2 // DROPFILES header followed by UTF-16 paths.
	handle, _, err := kernel32.NewProc("GlobalAlloc").Call(2, uintptr(size))
	if handle == 0 {
		t.Fatal(err)
	}
	defer kernel32.NewProc("GlobalFree").Call(handle)
	ptr := lockClipboardMemory(handle)
	if ptr == nil {
		t.Fatal("GlobalLock failed")
	}
	data := unsafe.Slice((*byte)(ptr), size)
	clear(data)
	binary.LittleEndian.PutUint32(data, 20)
	binary.LittleEndian.PutUint32(data[16:], 1)
	for i, value := range names {
		binary.LittleEndian.PutUint16(data[20+i*2:], value)
	}
	globalUnlock.Call(handle)
	if got, err := droppedFiles(handle); err != nil || !slices.Equal(got, want) {
		t.Fatalf("dropped files: %q, %v", got, err)
	}
}

func TestReadFilesCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if files, err := ReadFiles(ctx); files != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %q, %v", files, err)
	}
}
