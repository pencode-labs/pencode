package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/internal/filewatch"
	"github.com/pencode-labs/pencode/protocol"
)

func testWorkspaceWatcher(t *testing.T, root string) *workspaceWatch {
	t.Helper()
	watcher, err := filewatch.New()
	if err != nil {
		t.Fatal(err)
	}
	return &workspaceWatch{watcher: watcher, directories: make(map[string]bool), done: make(chan struct{}), journal: fileEventJournal{root: root, changed: make(chan struct{})}}
}
func TestWorkspaceWatchOverflowForcesRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	root := t.TempDir()
	w := testWorkspaceWatcher(t, root)
	go w.run(ctx, root)
	t.Cleanup(func() { cancel(); <-w.done })
	after := uint64(0)
	wait := func() protocol.FileChanges {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			var e protocol.Encoder
			w.journal.mu.Lock()
			ready := w.journal.encode(after, &e)
			changed := w.journal.changed
			w.journal.mu.Unlock()
			if ready {
				d := protocol.Decoder{Buffer: e.Buffer}
				result := protocol.FileChanges{Version: d.U64(), Changes: protocol.WorkspaceChange(d.U32()), Reset: d.U32() != 0}
				after = result.Version
				return result
			}
			select {
			case <-changed:
			case <-w.done:
				t.Fatalf("watch ended: %v", w.err)
			case <-timeout:
				t.Fatal("watch did not signal")
			}
		}
	}
	wait()
	w.watcher.Events <- filewatch.Event{Name: filepath.Join(root, "file"), Op: filewatch.Remove}
	wait()
	w.watcher.Errors <- filewatch.ErrOverflow
	if got := wait(); got.Changes != allWorkspaceChanges || !got.Reset {
		t.Fatalf("overflow did not invalidate subscribers: %+v", got)
	}
}

func TestWorkspaceWatchRebuildReconcilesDirectories(t *testing.T) {
	root := t.TempDir()
	removed := filepath.Join(root, "removed")
	if err := os.Mkdir(removed, 0700); err != nil {
		t.Fatal(err)
	}
	watcher, err := filewatch.New()
	if err != nil {
		t.Fatal(err)
	}
	w := &workspaceWatch{watcher: watcher, directories: make(map[string]bool), journal: fileEventJournal{root: root, changed: make(chan struct{})}}
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
		if _, ok := w.directories[directory]; !ok {
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

func TestFileJournalFanoutAndOverflow(t *testing.T) {
	j := fileEventJournal{root: "/project", changed: make(chan struct{})}
	j.publish(allWorkspaceChanges, nil, true)
	after := j.version
	file := protocol.FileEvent{Path: "main.go", Op: protocol.FileWritten}
	j.publish(protocol.WorkspaceGitChanged, []protocol.FileEvent{file}, false)
	for range 2 {
		var e protocol.Encoder
		if !j.encode(after, &e) {
			t.Fatal("subscriber stole another reader's event")
		}
		d := protocol.Decoder{Buffer: e.Buffer}
		d.U64()
		if d.U32() != uint32(protocol.WorkspaceGitChanged) || d.U32() != 0 || d.U32() != 1 || d.String() != file.Path || d.U32() != uint32(file.Op) {
			t.Fatal("incorrect event journal")
		}
	}
	for range fileEventHistory {
		j.publish(protocol.WorkspaceGitChanged, nil, false)
	}
	var e protocol.Encoder
	if !j.encode(after, &e) {
		t.Fatal("slow subscriber did not wake")
	}
	d := protocol.Decoder{Buffer: e.Buffer}
	d.U64()
	d.U32()
	if d.U32() != 1 {
		t.Fatal("history overflow did not force reconciliation")
	}
}

func TestWorkspaceWatchClassifiesEventsWithoutAllocating(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	for _, test := range []struct {
		name string
		op   filewatch.Op
		want protocol.WorkspaceChange
	}{
		{"source.go", filewatch.Write, protocol.WorkspaceGitChanged},
		{"source.go", filewatch.Chmod, protocol.WorkspaceGitChanged},
		{"source.go", filewatch.Remove, protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "HEAD"), filewatch.Write, protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "index"), filewatch.Rename, protocol.WorkspaceGitChanged},
		{filepath.Join(".git", "index.lock"), filewatch.Write, 0},
		{filepath.Join(".git", "objects"), filewatch.Create, 0},
		{"node_modules", filewatch.Create, 0},
		{"dist", filewatch.Create, 0},
	} {
		t.Run(fmt.Sprint(test.name, test.op), func(t *testing.T) {
			w := &workspaceWatch{directories: map[string]bool{root: false, git: true}}
			event := filewatch.Event{Name: filepath.Join(root, test.name), Op: test.op}
			ctx := t.Context()
			if allocations := testing.AllocsPerRun(100, func() {
				if err := w.event(ctx, event); err != nil {
					t.Fatal(err)
				}
			}); allocations != 0 {
				t.Fatalf("warm event allocated %g times", allocations)
			}
			if got := w.changes; got != test.want {
				t.Fatalf("changes=%b, want %b", got, test.want)
			}
		})
	}
}

func TestWorkspaceTreeChangesBoundedAndRetainedUntilSnapshot(t *testing.T) {
	var tree workspaceTreeChanges
	if allocations := testing.AllocsPerRun(1000, func() { tree.add("src") }); allocations != 0 {
		t.Fatalf("directory hint allocated %g times", allocations)
	}
	if delta := tree.take(); delta.full || delta.count != 1 || !delta.contains("src") {
		t.Fatalf("directory hints were not deduplicated: %+v", delta)
	}
	for i := range 129 {
		tree.add(fmt.Sprint(i))
	}
	if delta := tree.take(); !delta.full || delta.count != 0 {
		t.Fatal("directory budget did not request a full reconciliation")
	}
	if delta := tree.take(); delta.full || delta.count != 0 {
		t.Fatal("snapshot did not consume directory hints")
	}
}

func TestWorkspaceWatchFailureCanBeReinstalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	s := newServices(ctx, Options{})
	defer s.close()
	defer cancel()
	root := filepath.ToSlash(t.TempDir())
	s.roots[root] = true
	var encoder protocol.Encoder
	if err := s.watchWorkspace(ctx, root, 0, &encoder); err != nil {
		t.Fatal(err)
	}
	old := s.watches[root]
	failure := errors.New("injected native watcher failure")
	old.watcher.Errors <- failure
	old.journal.mu.Lock()
	after := old.journal.version
	old.journal.mu.Unlock()
	if err := s.watchWorkspace(ctx, root, after, &encoder); !errors.Is(err, failure) {
		t.Fatalf("watcher error = %v", err)
	}
	if err := s.watchWorkspace(ctx, root, after, &encoder); err != nil {
		t.Fatal(err)
	}
	if s.watches[root] == old {
		t.Fatal("failed native watcher remained cached")
	}
}
