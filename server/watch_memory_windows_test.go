package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestWorkspaceWatchDirectoryMemory(t *testing.T) {
	root := t.TempDir()
	for i := range 128 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprint(i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	w := &workspaceWatch{watcher: watcher, directories: make(map[string]bool)}
	if err := w.setup(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	// Allow directory metadata and runtime noise, while rejecting a return to
	// the Windows default of 64 KiB per directory.
	used := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	budget := int64(len(w.directories)*(20<<10) + (512 << 10))
	if used > budget {
		t.Fatalf("%d directories retain %d bytes, budget %d", len(w.directories), used, budget)
	}
	t.Logf("%d directories retain %.3f MiB", len(w.directories), float64(used)/(1<<20))
	runtime.KeepAlive(w)
}
