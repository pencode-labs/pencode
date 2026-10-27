package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pencode-labs/pencode/protocol"
)

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
			if allocations := testing.AllocsPerRun(100, func() {
				if err := w.event(t.Context(), event); err != nil {
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
