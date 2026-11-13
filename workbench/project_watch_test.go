package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onix-engine/onix/module"
	"github.com/pencode-labs/pencode/protocol"
)

func TestProjectWatchRetriesWithoutBlockingInitialScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := &pencodeProjectWatch{ready: make(chan struct{}), errors: make(chan error, 1)}
	calls, recover := make(chan struct{}, 1), make(chan struct{})
	done := make(chan struct{})
	failure := errors.New("native watch unavailable")
	all := protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged | protocol.WorkspaceRepositoriesChanged
	go func() {
		defer close(done)
		attempt := 0
		w.run(ctx, func(ctx context.Context) (protocol.WorkspaceChange, error) {
			attempt++
			calls <- struct{}{}
			if attempt == 1 {
				return 0, failure
			}
			if attempt == 2 {
				select {
				case <-recover:
					return all, nil
				case <-ctx.Done():
					return 0, ctx.Err()
				}
			}
			<-ctx.Done()
			return 0, ctx.Err()
		}, nil)
	}()
	select {
	case err := <-w.errors:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch failure was not reported")
	}
	waitPencodeScanSignal(t, w.ready)
	if got := protocol.WorkspaceChange(w.pending.Swap(0)); got != all {
		t.Fatalf("failure did not invalidate snapshots: %b", got)
	}
	waitPencodeScanSignal(t, calls)
	waitPencodeScanSignal(t, calls)
	close(recover)
	waitPencodeScanSignal(t, calls)
	if got := protocol.WorkspaceChange(w.pending.Swap(0)); got != all {
		t.Fatalf("recovery did not request a new snapshot: %b", got)
	}
	if err := <-w.errors; err != nil {
		t.Fatalf("recovery left an error status: %v", err)
	}
	cancel()
	waitPencodeScanSignal(t, done)
}

func TestProjectWatchRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitProjectWatchRetry(ctx, projectWatchRetryMax) {
		t.Fatal("cancelled watch retried")
	}
}

func TestProjectWatchRefreshesTreeAndGitWithoutReloadingDrafts(t *testing.T) {
	r := sourceTestRepository(t)
	sourceTestWrite(t, r, "main.go", "package original\n")
	sourceTestGit(t, r, "add", "--all")
	sourceTestGit(t, r, "commit", "-m", "base")
	m := &workspaceModel{backend: r.Backend, activeProjectIndex: -1}
	if err := loadProjectAndWait(t, m, r.Root); err != nil {
		t.Fatal(err)
	}
	p := m.activeProjectState()
	t.Cleanup(p.stopWatch)
	t.Cleanup(p.sourceControl.stop)
	waitPencodeProjectScan(t, p, func() { m.pollProjectScans() })
	// Drain the initial Git snapshot before making an external change.
	deadline := time.Now().Add(5 * time.Second)
	for !p.sourceControl.RefreshStarted || p.sourceControl.Done != nil || p.watch.pending.Load() != 0 {
		m.pollProjectWatches()
		m.updateSourceControl(nil, nil)
		if time.Now().After(deadline) {
			t.Fatal("initial Git snapshot did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	draft := &openFile{Path: "main.go", contentSnapshot: snapshotText("unsaved draft"), Dirty: true}
	p.Files[draft.Path] = draft
	p.sourceControl.RepositoriesLoaded = true
	if err := os.WriteFile(filepath.Join(p.Path, "created.go"), []byte("package created\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		m.pollProjectScans()
		m.pollProjectWatches()
		m.updateSourceControl(nil, nil)
		found := false
		for _, entry := range p.Entries {
			found = found || entry.Path == "created.go"
		}
		if found && p.scan == nil && len(p.sourceControl.Snapshot.Files) == 1 && p.sourceControl.Done == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("event did not refresh tree/Git: entries=%v, changes=%v", p.Entries, p.sourceControl.Snapshot.Files)
		}
		time.Sleep(time.Millisecond)
	}
	if p.Files[draft.Path] != draft || draft.content() != "unsaved draft" || !draft.Dirty {
		t.Fatal("filesystem refresh overwrote an unsaved document")
	}
	if !p.sourceControl.RepositoriesLoaded {
		t.Fatal("ordinary file creation invalidated repository discovery")
	}
	m.discardProject(p)
	if p.watch != nil || p.scan != nil {
		t.Fatal("closing a project retained its event listener")
	}
}

func TestProjectWatchPublishesCompleteTreeSnapshot(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			old := treeEntry{Path: "old.go"}
			scan := &pencodeProjectScan{staged: true, cancel: func() {}, results: make(chan *pencodeTreeBatch, 1), buffers: make(chan *pencodeTreeBatch, 2)}
			p := &pencodeProject{Path: "/project", Entries: []treeEntry{old}, scan: scan, index: 3}
			m := &workspaceModel{projects: []*pencodeProject{p}, activeProjectIndex: 0}
			batch := &pencodeTreeBatch{index: 7, count: 1}
			batch.entries[0] = treeEntry{Path: "new.go"}
			scan.results <- batch
			if m.pollProjectScans() || len(p.Entries) != 1 || p.Entries[0] != old || p.index != 3 {
				t.Fatal("partial refresh changed the visible tree")
			}
			batch = &pencodeTreeBatch{index: 7, done: true}
			if fail {
				batch.err = errors.New("directory read failed")
			}
			scan.results <- batch
			if !m.pollProjectScans() || p.scan != nil {
				t.Fatal("completed snapshot was not applied")
			}
			if fail {
				if p.Entries[0] != old || p.index != 3 {
					t.Fatal("failed refresh replaced the last complete tree")
				}
			} else if p.Entries[0].Path != "new.go" || p.index != 7 {
				t.Fatal("new tree and index were not published together")
			}
		})
	}
}

func TestProjectWatchCoalescesBusyRefreshAndKeepsIdleAllocationFree(t *testing.T) {
	p := &pencodeProject{Path: "/project", watch: &pencodeProjectWatch{errors: make(chan error, 1)}}
	m := &workspaceModel{projects: []*pencodeProject{p}, activeProjectIndex: 0, projectPath: p.Path,
		app: &module.Host{WakeAt: func(time.Time) { t.Fatal("idle refresh scheduled a timer") }}}
	s := &p.sourceControl
	s.RefreshStarted = true
	if allocations := testing.AllocsPerRun(100, func() {
		if m.pollProjectWatches() || m.updateSourceControl(nil, nil) {
			t.Fatal("idle workspace requested a refresh")
		}
	}); allocations != 0 {
		t.Fatalf("idle refresh allocated %g times", allocations)
	}
	p.scan = &pencodeProjectScan{}
	s.Done = make(chan sourceResult, 1)
	s.JobKind = sourceRefresh
	for range 100 {
		p.watch.pending.Or(uint32(protocol.WorkspaceTreeChanged | protocol.WorkspaceGitChanged))
		if m.pollProjectWatches() || m.updateSourceControl(nil, nil) {
			t.Fatal("filesystem event restarted a busy scan or Git refresh")
		}
	}
	if !p.treePending || !s.RefreshPending {
		t.Fatal("busy refresh did not coalesce changes into one pending snapshot")
	}
	m.quickOpen = true
	p.scan = nil
	if m.pollProjectWatches() || p.scan != nil || !p.treePending {
		t.Fatal("automatic scan invalidated the index during file search")
	}
}
