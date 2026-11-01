package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pencode-labs/pencode/protocol"
)

func TestWorkspaceWatchOverflowForcesRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	w := &workspaceWatch{watcher: watcher, directories: make(map[string]bool), signal: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run(ctx, t.TempDir())
	t.Cleanup(func() { cancel(); <-w.done })
	wait := func() {
		t.Helper()
		select {
		case <-w.signal:
		case <-w.done:
			t.Fatalf("watch ended: %v", w.err)
		case <-time.After(5 * time.Second):
			t.Fatal("watch did not signal")
		}
	}
	wait()
	w.pending.Store(0)
	// Populate both gates immediately before overflow on the watch goroutine.
	// A native event first exercises the ordinary publish path.
	select {
	case watcher.Events <- fsnotify.Event{Name: filepath.Join(t.TempDir(), "file"), Op: fsnotify.Remove}:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not accept event")
	}
	wait()
	w.pending.Store(0)
	select {
	case watcher.Errors <- fsnotify.ErrEventOverflow:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not accept overflow")
	}
	wait()
	all := protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged
	if got := protocol.WorkspaceChange(w.pending.Load()); got != all {
		t.Fatalf("overflow refresh=%b, want %b", got, all)
	}
}

func TestWorkspaceWatchRebuildReconcilesDirectories(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "removed")
	if err := os.Mkdir(removed, 0700); err != nil {
		t.Fatal(err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	w := &workspaceWatch{watcher: watcher, directories: make(map[string]bool), signal: make(chan struct{}, 1)}
	t.Cleanup(func() { w.watcher.Close() })
	if err := w.setup(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(root, "added", "nested")
	git := filepath.Join(root, ".git", "refs", "heads")
	for _, name := range []string{added, git} {
		if err := os.MkdirAll(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.rebuild(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if _, exists := w.directories[removed]; exists {
		t.Fatal("removed directory retained after rebuild")
	}
	for _, directory := range []string{added, git} {
		if !slices.Contains(w.watcher.WatchList(), directory) {
			t.Fatalf("directory not watched after rebuild: %s", directory)
		}
		name := filepath.Join(directory, "file")
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
	read:
		for {
			select {
			case event := <-w.watcher.Events:
				if event.Name == name {
					break read
				}
			case err := <-w.watcher.Errors:
				t.Fatal(err)
			case <-deadline:
				t.Fatalf("rebuilt watch missed %s", name)
			}
		}
	}
}

func TestWorkspaceWatchCoalescesAndDropsBursts(t *testing.T) {
	w := &workspaceWatch{signal: make(chan struct{}, 1)}
	now := time.Now()
	all := protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged
	w.publish(protocol.WorkspaceGitChanged, now)
	w.publish(all, now)
	if got := protocol.WorkspaceChange(w.pending.Swap(0)); got != all || len(w.signal) != 1 {
		t.Fatalf("coalesced changes=%b, signals=%d", got, len(w.signal))
	}
	<-w.signal
	if allocations := testing.AllocsPerRun(1000, func() {
		w.publish(all, now.Add(workspaceWatchInterval-1))
	}); allocations != 0 {
		t.Fatalf("dropped events allocated %g times", allocations)
	}
	if w.pending.Load() != 0 || len(w.signal) != 0 {
		t.Fatal("burst triggered another refresh")
	}
	w.publish(all, now.Add(workspaceWatchInterval))
	if got := protocol.WorkspaceChange(w.pending.Load()); got != all || len(w.signal) != 1 {
		t.Fatal("first event after the rate limit was dropped")
	}
}

func TestWorkspaceWatchClassifiesEventsWithoutAllocating(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	for _, test := range []struct {
		name string
		op   fsnotify.Op
		want protocol.WorkspaceChange
	}{
		{"source.go", fsnotify.Write, protocol.WorkspaceGitChanged},
		{"source.go", fsnotify.Chmod, protocol.WorkspaceGitChanged},
		{"source.go", fsnotify.Remove, protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "HEAD"), fsnotify.Write, protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "index"), fsnotify.Rename, protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "index.lock"), fsnotify.Write, 0},
		{filepath.Join(".git", "objects"), fsnotify.Create, 0},
		{"node_modules", fsnotify.Create, 0},
		{"dist", fsnotify.Create, 0},
	} {
		t.Run(test.name+test.op.String(), func(t *testing.T) {
			w := &workspaceWatch{signal: make(chan struct{}, 1), directories: map[string]bool{root: false, git: true}}
			event := fsnotify.Event{Name: filepath.Join(root, test.name), Op: test.op}
			ctx := t.Context()
			if allocations := testing.AllocsPerRun(100, func() {
				if err := w.event(ctx, event); err != nil {
					t.Fatal(err)
				}
			}); allocations != 0 {
				t.Fatalf("warm event allocated %g times", allocations)
			}
			if got := protocol.WorkspaceChange(w.pending.Load()); got != test.want {
				t.Fatalf("changes=%b, want %b", got, test.want)
			}
		})
	}
}
