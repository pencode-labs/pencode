package workbench

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/lsp"
	"github.com/pencode-labs/pencode/server"
)

type fileIOGate struct {
	net.Conn
	pause                  atomic.Bool
	entered, release, done chan struct{}
	enteredOnce, closeOnce sync.Once
}

func (g *fileIOGate) Write(p []byte) (int, error) {
	if g.pause.Load() {
		g.enteredOnce.Do(func() { close(g.entered) })
		select {
		case <-g.release:
		case <-g.done:
			return 0, net.ErrClosed
		}
	}
	return g.Conn.Write(p)
}
func (g *fileIOGate) Close() error {
	g.closeOnce.Do(func() { close(g.done) })
	return g.Conn.Close()
}
func delayedFileModel(t *testing.T) (*workspaceModel, *pencodeProject, *fileIOGate, func()) {
	t.Helper()
	a, b := net.Pipe()
	g := &fileIOGate{Conn: b, entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	options := server.Options{ExtensionDirectory: t.TempDir()}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(context.Background(), g, options) }()
	c, err := client.New(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); <-done })
	p := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile), ExpandedDirs: make(map[string]bool)}
	attachPencodeProject(t, p, c)
	m := &workspaceModel{backend: c, projects: []*pencodeProject{p}, activeProjectIndex: 0}
	m.syncActiveProjectFields()
	unblock := sync.OnceFunc(func() { close(g.release) })
	t.Cleanup(unblock)
	t.Cleanup(m.stopFileIO)
	t.Cleanup(p.stopScan)
	return m, p, g, unblock
}
func awaitFileGate(t *testing.T, g *fileIOGate) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not reach gated response")
	}
}
func TestFileIOOpenKeepsPreviewAndRejectsStaleSelection(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "old.go", "package old\n")
	resourceTestWrite(t, p, "new.go", "package new\n")
	openProjectFile(p, "old.go")
	p.PreviewPath = "old.go"
	m.syncActiveProjectFields()
	g.pause.Store(true)
	if err := m.requestFileOpen(p, "new.go", true, false, nil); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	if p.PreviewPath != "old.go" || p.Files["old.go"] == nil {
		t.Fatal("pending open evicted preview")
	}
	if err := m.requestFileOpen(p, "old.go", false, false, nil); err != nil {
		t.Fatal(err)
	}
	m.updateActiveContent("package retained\n")
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if p.ActivePath != "old.go" || p.Files["new.go"] != nil || m.activeContent() != "package retained\n" {
		t.Fatal("stale open stole selection")
	}
	if err := m.requestFileOpen(p, "missing.go", true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err == nil {
		t.Fatal("missing file succeeded")
	}
	if p.Files["old.go"] == nil {
		t.Fatal("failed open evicted old buffer")
	}
}
func TestFileIOSaveSnapshotAndCloseBarrier(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "main.go", "package original\n")
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	m.updateActiveContent("package snapshot\n")
	g.pause.Store(true)
	// Closing a dirty tab starts a save but must wait for its result.
	m.closeProjectTab(p, "main.go")
	m.resolveClose(true)
	if m.closing.saveID == 0 {
		t.Fatal("save did not hold close dialog")
	}
	awaitFileGate(t, g)
	m.updateActiveContent("package newer\n")
	history := len(m.activeFile().undo)
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := p.Files["main.go"]
	disk, _ := os.ReadFile(filepath.Join(p.Path, "main.go"))
	if string(disk) != "package snapshot\n" || f == nil || !f.Dirty || f.Content != "package newer\n" || len(f.undo) != history {
		t.Fatal("save lost newer edit or closed dirty tab", string(disk))
	}
	if m.closing.saveID != 0 || m.closing.error == "" {
		t.Fatal("close dialog did not retain changed file")
	}
	m.resolveClose(true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if p.Files["main.go"] != nil {
		t.Fatal("successful save did not finish close")
	}
	disk, _ = os.ReadFile(filepath.Join(p.Path, "main.go"))
	if string(disk) != "package newer\n" {
		t.Fatal("second save lost newer text")
	}
}
func TestFileIOReloadRetainsTypingAndConflictBaseline(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "main.go", "package original\n")
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	resourceTestWrite(t, p, "main.go", "package external\n")
	g.pause.Store(true)
	m.reloadProjectFromDisk(p)
	awaitFileGate(t, g)
	m.updateActiveContent("package typing\n")
	if _, err := m.requestFileSave(p, m.activeFile()); err == nil {
		t.Fatal("save accepted a retiring document")
	}
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := m.activeFile()
	if f.Content != "package typing\n" || !f.Dirty || len(f.undo) != 1 || f.document != nil {
		t.Fatal("reload overwrote typing or retained retired handle")
	}
	if err := saveFileAndWait(t, m, p.Path, f); err == nil {
		t.Fatal("lazy save overwrote external change")
	}
	disk, _ := os.ReadFile(filepath.Join(p.Path, "main.go"))
	if string(disk) != "package external\n" {
		t.Fatal("conflict changed disk")
	}
}
func TestFileIORenamePreservesEditsAndDefersClose(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "main.go", "package original\n")
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	g.pause.Store(true)
	m.fileContext = pencodeFileContext{project: p, path: "main.go", dialog: "rename", name: "renamed.go"}
	if err := m.applyFileDialog(); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	m.updateActiveContent("package typed\n")
	m.closeProjectTab(p, "main.go")
	if m.fileClose.action != pencodeCloseTab {
		t.Fatal("close was not deferred during mutation")
	}
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	f := p.Files["renamed.go"]
	if f == nil || f.Content != "package typed\n" || len(f.undo) != 1 || m.closing.path != "renamed.go" {
		t.Fatal("rename lost edits or close target")
	}
	m.clearCloseRequest()
	if err := saveFileAndWait(t, m, p.Path, f); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(filepath.Join(p.Path, "renamed.go"))
	if string(disk) != f.Content {
		t.Fatal("moved document did not save")
	}
}
func TestFileIOLanguageEditRejectsWholeStalePlan(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	resourceTestWrite(t, p, "main.go", "package original\n")
	resourceTestWrite(t, p, "other.go", "package other\n")
	openProjectFile(p, "main.go")
	m.syncActiveProjectFields()
	m.languages = &pencodeLanguageState{sessions: make(map[pencodeLanguageKey]*pencodeLanguageSession), documents: make(map[*openFile]*pencodeLanguageDocument)}
	session := &pencodeLanguageSession{key: pencodeLanguageKey{root: p.Path}, client: &lsp.Client{Encoding: lsp.UTF16}}
	edit := lsp.WorkspaceEdit{Changes: map[lsp.URI][]lsp.TextEdit{
		lsp.FileURI(filepath.Join(p.Path, "main.go")):  {{Range: lsp.Range{}, NewText: "// edit\n"}},
		lsp.FileURI(filepath.Join(p.Path, "other.go")): {{Range: lsp.Range{}, NewText: "// edit\n"}},
	}}
	g.pause.Store(true)
	callbacks := 0
	var result error
	if err := m.applyLanguageWorkspaceEdit(session, edit, func(err error) { callbacks++; result = err }); err != nil {
		t.Fatal(err)
	}
	awaitFileGate(t, g)
	if callbacks != 0 {
		t.Fatal("edit acknowledged before commit")
	}
	m.updateActiveContent("package typing\n")
	unblock()
	if err := awaitFileIO(t, m); err == nil {
		t.Fatal("stale workspace edit accepted")
	}
	if callbacks != 1 || result == nil || p.Files["other.go"] != nil || m.activeContent() != "package typing\n" {
		t.Fatal("workspace edit partially committed", result)
	}
}
func TestFileIOQueueBoundAndIdleAllocations(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	g.pause.Store(true)
	for range fileIOCapacity {
		if err := m.submitFileIO(&fileIOJob{kind: fileOpen, project: p, workspace: p.workspace.ID, relative: "missing.go"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.submitFileIO(&fileIOJob{kind: fileOpen}); err == nil {
		t.Fatal("queue grew beyond bound")
	}
	awaitFileGate(t, g)
	if n := testing.AllocsPerRun(100, func() { m.pollFileIO() }); n != 0 {
		t.Fatalf("idle polling allocated %g", n)
	}
	m.cancelProjectFileIO(p)
	unblock()
	if err := awaitFileIO(t, m); err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "missing.go") {
		t.Fatal(err)
	}
}
func TestFileIOCloseAndReopenWorkspaceIsOrdered(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	resourceTestWrite(t, p, "main.go", "package original\n")
	m.closeProject(p)
	if err := m.loadProject(p.Path); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	reopened := m.activeProjectState()
	if reopened == nil || reopened == p {
		t.Fatal("project was not reopened")
	}
	t.Cleanup(reopened.stopScan)
	if err := m.requestFileOpen(reopened, "main.go", false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal("old cleanup invalidated new workspace", err)
	}
}
