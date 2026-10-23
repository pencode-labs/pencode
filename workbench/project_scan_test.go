package workbench

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/diiyw/onix"
)

type pencodeScanTestFS struct {
	fs.FS
	beforeReadDir func(string) error
}

func (source pencodeScanTestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := source.beforeReadDir(name); err != nil {
		return nil, err
	}
	return fs.ReadDir(source.FS, name)
}

func waitPencodeProjectScan(t testing.TB, project *pencodeProject, step func()) {
	t.Helper()
	t.Cleanup(project.stopScan)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for project.scan != nil {
		step()
		if project.scan == nil {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("project scan did not finish")
		case <-ticker.C:
		}
	}
}

func waitPencodeScanSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach filesystem barrier")
	}
}

func waitPencodeScanStopped(t *testing.T, scan *pencodeProjectScan) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case _, ok := <-scan.results:
			if !ok {
				return
			}
		case <-timeout.C:
			t.Fatal("cancelled scan did not exit")
		}
	}
}

func TestPencodeLoadProjectDefersTreeWithoutOpeningFiles(t *testing.T) {
	dir := t.TempDir()
	for _, directory := range []string{"src", ".git", "node_modules", "dist"} {
		if err := os.Mkdir(filepath.Join(dir, directory), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, directory, "first.go"), []byte("package first\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	model := workspaceModel{backend: newPencodeBackend(t), activeProjectIndex: -1}
	if err := loadProjectAndWait(t, &model, dir); err != nil {
		t.Fatal(err)
	}
	project := model.activeProjectState()
	t.Cleanup(project.stopScan)
	if model.screen != pencodeWorkspace || project.scan == nil || len(project.Entries) != 0 || model.activeFile() != nil {
		t.Fatal("opening a project waited for tree data or opened a file")
	}
	scan := project.scan
	if err := loadProjectAndWait(t, &model, dir); err != nil {
		t.Fatal(err)
	}
	if model.activeProjectState() != project || project.scan != scan || len(model.projects) != 1 {
		t.Fatal("reopening a project restarted its scan")
	}
	waitPencodeProjectScan(t, project, func() { model.pollFileIO(); model.pollProjectScans() })
	if model.activeFile() != nil || len(project.Files) != 0 || len(project.Tabs) != 0 || project.ActivePath != "" || project.PreviewPath != "" || project.Status != "" {
		t.Fatalf("opening a project opened a file or left a status: %+v", project)
	}
	if len(project.Entries) != 3 || project.Entries[0].Path != "src" || project.Entries[1].Path != "src/first.go" || project.Entries[1].Depth != 1 || project.Entries[2].Path != "main.go" {
		t.Fatalf("unexpected tree entries: %+v", project.Entries)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if model.pollProjectScans() {
			t.Fatal("completed scan changed the model")
		}
	}); allocations != 0 {
		t.Fatalf("idle scan polling allocated %g times", allocations)
	}
}

func TestPencodeScanPublishesBeforeSlowSubtreeAndKeepsUserEdits(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	source := pencodeScanTestFS{
		FS: fstest.MapFS{"a/child.go": &fstest.MapFile{}, "main.go": &fstest.MapFile{Data: []byte("package main\n")}},
		beforeReadDir: func(name string) error {
			if name == "a" {
				close(entered)
				<-release
			}
			return nil
		},
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chosen.go"), []byte("package chosen\n"), 0644); err != nil {
		t.Fatal(err)
	}
	project := &pencodeProject{Path: dir, Files: make(map[string]*openFile), Status: pencodeTreeLoading}
	connectPencodeProject(t, project)
	project.scan = newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(source, name) }, true, nil)
	t.Cleanup(project.stopScan)
	model := workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0}
	t.Cleanup(model.stopFileIO)
	waitPencodeScanSignal(t, entered)
	if !model.pollProjectScans() || len(project.Entries) != 1 || project.Entries[0].Path != "a" || project.scan == nil {
		t.Fatal("first directory was not available while its subtree was blocked")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if model.pollProjectScans() {
			t.Fatal("blocked scan changed the model")
		}
	}); allocations != 0 {
		t.Fatalf("pending scan polling allocated %g times", allocations)
	}
	openProjectFile(project, "chosen.go")
	file := project.Files["chosen.go"]
	file.Content, file.Dirty = "package edited\n", true
	model.syncActiveProjectFields()
	unblock()
	waitPencodeProjectScan(t, project, func() { model.pollFileIO(); model.pollProjectScans() })
	if model.activeFile() != file || file.Content != "package edited\n" || !file.Dirty || len(project.Files) != 1 || project.PreviewPath != "" {
		t.Fatal("scan completion overwrote the user's file or edits")
	}
}

func TestPencodeScanBatchesInvalidateQuickOpen(t *testing.T) {
	root := t.TempDir()
	const fileCount = 3000
	for index := range fileCount {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.go", index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	project := &pencodeProject{Path: root, Files: make(map[string]*openFile)}
	connectPencodeProject(t, project)
	project.scan = newBackendProjectScan(project.backend, root, false, nil)
	t.Cleanup(project.stopScan)
	model := workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0, quickQuery: "file-2999"}
	model.refreshQuickMatches()
	if model.quickCount != 0 {
		t.Fatal("empty project returned a quick-open match")
	}
	waitPencodeProjectScan(t, project, func() {
		before := len(project.Entries)
		model.pollProjectScans()
		if len(project.Entries)-before > pencodeTreeFrameBatches*pencodeTreeBatchSize {
			t.Fatal("one frame consumed an unbounded number of entries")
		}
		model.refreshQuickMatches()
	})
	settleQuick(t, &model)
	if len(project.Entries) != fileCount || model.quickCount != 1 || model.quickMatches[0] != "file-2999.go" {
		t.Fatalf("progressive index missed a file: entries=%d matches=%v", len(project.Entries), model.quickMatches)
	}
}

func TestPencodeScanCancellationUnblocksBufferBackpressure(t *testing.T) {
	source := make(fstest.MapFS)
	for index := range 1024 {
		source[fmt.Sprintf("file-%04d.go", index)] = &fstest.MapFile{}
	}
	wakes := make(chan struct{}, 2)
	scan := newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(source, name) }, false, func() { wakes <- struct{}{} })
	defer scan.cancel()
	waitPencodeScanSignal(t, wakes)
	waitPencodeScanSignal(t, wakes)
	scan.cancel()
	waitPencodeScanStopped(t, scan)
}

func TestPencodeScanWakesIdleWorkspace(t *testing.T) {
	app, session := newPencodeTestApp(t)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	model := session.active.model
	project := model.activeProjectState()
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	files := make(fstest.MapFS)
	for index := range 3000 {
		files[fmt.Sprintf("src/file-%04d.go", index)] = &fstest.MapFile{}
	}
	source := pencodeScanTestFS{FS: files, beforeReadDir: func(name string) error {
		if name == "." {
			close(entered)
			<-release
		}
		return nil
	}}
	project.Entries = nil
	project.scan = newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(source, name) }, false, func() { model.app.WakeAt(time.Now()) })
	t.Cleanup(project.stopScan)
	waitPencodeScanSignal(t, entered)
	step := func() bool {
		t.Helper()
		rendered, err := app.Step()
		if err != nil {
			t.Fatal(err)
		}
		return rendered
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	step()
	if step() {
		t.Fatal("waiting for filesystem reads kept rendering an idle workspace")
	}
	unblock()
	waitPencodeProjectScan(t, project, func() { step() })
	if len(project.Entries) != len(files)+1 {
		t.Fatal("idle workspace did not receive all scan batches")
	}
	// Consume a wake posted concurrently with the completion frame.
	step()
	if step() {
		t.Fatal("completed scan prevented the workspace returning to idle")
	}
}

func TestPencodeRefreshAndCloseDiscardStaleScans(t *testing.T) {
	for _, closeProject := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%v", closeProject), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "fresh.go"), nil, 0644); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			source := pencodeScanTestFS{
				FS: fstest.MapFS{"old/obsolete.go": &fstest.MapFile{}},
				beforeReadDir: func(name string) error {
					if name == "old" {
						close(entered)
						<-release
					}
					return nil
				},
			}
			project := &pencodeProject{Path: dir, Files: make(map[string]*openFile)}
			connectPencodeProject(t, project)
			old := newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(source, name) }, true, nil)
			project.scan = old
			t.Cleanup(project.stopScan)
			model := workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0}
			t.Cleanup(model.stopFileIO)
			waitPencodeScanSignal(t, entered)
			if closeProject {
				model.closeProject(project)
			} else {
				model.reloadProjectFromDisk(project)
			}
			unblock()
			waitPencodeScanStopped(t, old)
			if closeProject {
				if model.pollProjectScans() || len(model.projects) != 0 || len(project.Entries) != 0 || model.screen != pencodeLauncher {
					t.Fatal("closed project was restored by a stale result")
				}
			} else {
				waitPencodeProjectScan(t, project, func() { model.pollFileIO(); model.pollProjectScans() })
				if len(project.Entries) != 1 || project.Entries[0].Path != "fresh.go" || project.Status != "已从磁盘重新加载" {
					t.Fatalf("refresh applied a stale result: %+v", project.Entries)
				}
			}
		})
	}
}

func TestPencodeScanContinuesPastUnreadableSubtrees(t *testing.T) {
	source := pencodeScanTestFS{
		FS: fstest.MapFS{"a/hidden.go": &fstest.MapFile{}, "main.go": &fstest.MapFile{Data: []byte("package main\n")}},
		beforeReadDir: func(name string) error {
			if name == "a" {
				return fs.ErrPermission
			}
			return nil
		},
	}
	project := &pencodeProject{Path: "fixture", Files: make(map[string]*openFile)}
	project.scan = newPencodeProjectScan(func(name string) ([]fs.DirEntry, error) { return fs.ReadDir(source, name) }, true, nil)
	model := workspaceModel{backend: project.backend, projects: []*pencodeProject{project}, activeProjectIndex: 0}
	t.Cleanup(model.stopFileIO)
	waitPencodeProjectScan(t, project, func() { model.pollFileIO(); model.pollProjectScans() })
	if len(project.Entries) != 2 || project.Entries[1].Path != "main.go" || model.activeFile() != nil || !strings.Contains(project.Status, fs.ErrPermission.Error()) {
		t.Fatalf("unreadable subtree hid remaining files: entries=%+v status=%q", project.Entries, project.Status)
	}
}
