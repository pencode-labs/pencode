package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diiyw/onix/module"
	"github.com/pencode-labs/pencode/protocol"
)

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
	m.discardProject(p)
	if p.watch != nil || p.scan != nil {
		t.Fatal("closing a project retained its event listener")
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
