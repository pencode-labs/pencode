package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/protocol"
)

func settleFolderSearch(t *testing.T, m *workspaceModel) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.folderSearch.busy {
		m.pollFolderSearch()
		if time.Now().After(deadline) {
			t.Fatal("folder search timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFolderSearchReplaceBuffersDiskAndConflicts(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	t.Cleanup(m.closeFolderSearchWorker)
	resourceTestWrite(t, p, "src/open.txt", "disk old")
	resourceTestWrite(t, p, "src/closed.txt", "中🙂 OLD old")
	resourceTestWrite(t, p, "outside.txt", "old")
	openProjectFile(p, "src/open.txt")
	m.syncActiveProjectFields()
	m.updateActiveContent("unsaved old old")
	m.openFolderSearch(p, "src", true)
	m.folderSearch.query, m.folderSearch.replacement = "old", "new"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	if len(m.folderSearch.rows) != 4 {
		t.Fatalf("rows=%v, message=%s", m.folderSearch.rows, m.folderSearch.message)
	}
	m.replaceFolderSearch(false)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	settleFolderSearch(t, m)
	if len(m.folderSearch.rows) != 3 {
		t.Fatal("single replace changed wrong number of matches", m.folderSearch.message)
	}
	m.replaceFolderSearch(true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	settleFolderSearch(t, m)
	if len(m.folderSearch.rows) != 0 || p.Files["src/open.txt"].content() != "unsaved new new" {
		t.Fatal("replace all failed")
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(p.Path, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if read("src/closed.txt") != "中🙂 new new" || read("src/open.txt") != "disk old" || read("outside.txt") != "old" {
		t.Fatal("disk/buffer scope was not preserved")
	}
	// Replacing an open buffer is undoable and remains dirty.
	m.undoActiveContent(nil)
	if m.activeContent() != "unsaved old old" || !m.activeFile().Dirty {
		t.Fatal("open buffer lost undo", m.activeContent())
	}
	m.folderSearch.query = "new"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	resourceTestWrite(t, p, "src/closed.txt", "external new")
	m.folderSearch.replacement = "bad"
	m.replaceFolderSearch(true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if read("src/closed.txt") != "external new" || !strings.Contains(p.Status, "失败") {
		t.Fatal("stale result overwrote external edit", p.Status)
	}
}

func TestFolderSearchPopupFromContextAndNavigation(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	p, world := m.activeProjectState(), m.fontResources
	file := m.activeFile()
	m.updateActiveContent("中🙂 target target")
	fileContextRightClick(t, app, m, state, m.projectTreeRowID(p, file.Path))
	editorContextClick(t, app, m, state, "file-context-find")
	for range 3 {
		languageInteractionStep(t, app)
	}
	if !m.folderSearch.open || !pencodeControlFocused(world, m.surface, "folder-search-query") {
		control, ok := world.HTMLControl(m.surface, "folder-search-query")
		field, _ := world.Get[engineui.TextField](control)
		t.Fatalf("context did not open/focus popup: open=%v focus=%v control=%v found=%v field=%+v context=%+v", m.folderSearch.open, m.folderSearch.needsFocus, control, ok, field, m.fileContext)
	}
	m.handleFolderSearchEvent(world, "folder-search-query", "target")
	settleFolderSearch(t, m)
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	if len(m.folderSearch.rows) != 2 {
		t.Fatal(m.folderSearch.message)
	}
	m.handleFolderSearchEvent(world, "folder-search-toggle", "")
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	if !m.folderSearch.replace {
		t.Fatal("replace toggle failed")
	}
	for _, zoom := range []int{100, 150, 70} {
		m.setZoom(world, zoom)
		languageInteractionStep(t, app)
		panel := editorContextBounds(t, m, "folder-search-panel")
		toggle := editorContextBounds(t, m, "folder-search-toggle")
		root := editorContextBounds(t, m, "pencode-root")
		query := editorContextBounds(t, m, "folder-search-query")
		if panel.X < root.X || panel.X+panel.Width > root.X+root.Width+1 || panel.Y < root.Y || panel.Y+panel.Height > root.Y+root.Height+1 || query.Width < 100 || toggle.Height < 55 {
			t.Fatalf("popup escaped viewport or toggle lost its row span at %d%%: panel=%+v root=%+v query=%+v toggle=%+v", zoom, panel, root, query, toggle)
		}
	}
	m.openFolderSearchMatch(world, 1)
	markPencodeRootDirtyByModel(world, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	field, _ := world.Get[engineui.TextField](m.editor.control)
	if m.folderSearch.open || field.Anchor != 10 || field.Caret != 16 || !field.Focused {
		t.Fatalf("navigation did not select exact match: %+v", field)
	}
}

func TestFolderSearchReplaceAllIncludesUndisplayedResults(t *testing.T) {
	m, p, _, _ := delayedFileModel(t)
	t.Cleanup(m.closeFolderSearchWorker)
	value := strings.Repeat("old ", protocol.TextSearchLimit+7)
	resourceTestWrite(t, p, "many.txt", value)
	resourceTestWrite(t, p, "z.txt", "old")
	m.openFolderSearch(p, "", true)
	m.folderSearch.query, m.folderSearch.replacement = "old", "new"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	if !m.folderSearch.result.Truncated || len(m.folderSearch.rows) != protocol.TextSearchLimit || len(m.folderSearch.result.Files) != 2 {
		t.Fatal("display cap lost matching files", m.folderSearch.message)
	}
	m.replaceFolderSearch(true)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"many.txt", "z.txt"} {
		data, err := os.ReadFile(filepath.Join(p.Path, name))
		if err != nil || strings.Contains(string(data), "old") || !strings.Contains(string(data), "new") {
			t.Fatal("replace all missed undisplayed matches", name, err)
		}
	}
}

func TestFolderSearchRejectsOutdatedBufferSnapshots(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	t.Cleanup(m.closeFolderSearchWorker)
	resourceTestWrite(t, p, "open.txt", "old")
	openProjectFile(p, "open.txt")
	m.syncActiveProjectFields()
	m.openFolderSearch(p, "", false)
	g.pause.Store(true)
	m.folderSearch.query = "old"
	m.startFolderSearch()
	awaitFileGate(t, g)
	m.updateActiveContent("new unsaved")
	unblock()
	settleFolderSearch(t, m)
	if len(m.folderSearch.rows) != 0 {
		t.Fatal("stale search results were accepted")
	}
	m.folderSearch.query = "new"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	if len(m.folderSearch.rows) != 1 {
		t.Fatal("search did not retry using current buffer", m.folderSearch.message)
	}
}

func TestFolderReplaceRetainsTypingDuringBackgroundIO(t *testing.T) {
	m, p, g, unblock := delayedFileModel(t)
	t.Cleanup(m.closeFolderSearchWorker)
	resourceTestWrite(t, p, "a.txt", "old")
	resourceTestWrite(t, p, "z.txt", "old")
	openProjectFile(p, "z.txt")
	m.syncActiveProjectFields()
	m.openFolderSearch(p, "", true)
	m.folderSearch.query, m.folderSearch.replacement = "old", "new"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	g.pause.Store(true)
	m.replaceFolderSearch(true)
	awaitFileGate(t, g)
	m.updateActiveContent("newer typing old")
	unblock()
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeContent() != "newer typing old" || !strings.Contains(p.Status, "失败") {
		t.Fatal("background replace overwrote newer typing", m.activeContent(), p.Status)
	}
}

func TestFolderSearchNavigationActivatesItsProject(t *testing.T) {
	m, first, _, _ := delayedFileModel(t)
	t.Cleanup(m.closeFolderSearchWorker)
	other := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile)}
	attachPencodeProject(t, other, m.backend)
	m.projects = append(m.projects, other)
	resourceTestWrite(t, other, "target.txt", "中🙂 target")
	m.openFolderSearch(other, "", false)
	m.folderSearch.query = "target"
	m.startFolderSearch()
	settleFolderSearch(t, m)
	if m.activeProjectState() != first {
		t.Fatal("search switched project before navigation")
	}
	m.openFolderSearchMatch(nil, 0)
	if err := awaitFileIO(t, m); err != nil {
		t.Fatal(err)
	}
	if m.activeProjectState() != other || m.activeFile() == nil || m.activeFile().Path != "target.txt" || m.activeFile().editorView.anchor != 3 || m.activeFile().editorView.caret != 9 {
		t.Fatal("navigation did not activate the target project/document")
	}
}
