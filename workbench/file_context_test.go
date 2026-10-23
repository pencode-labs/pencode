package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func fileContextRightClick(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string) {
	t.Helper()
	r := editorContextBounds(t, model, ref)
	zoom := float64(model.settings.Zoom) / 100
	x, y := float64(r.X+r.Width/2)*zoom, float64(r.Y+r.Height/2)*zoom
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
}

func TestFileContextNativeRenameCreateDeleteAndRefresh(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	model.updateActiveContent("unsaved 中文🙂\n")
	model.setEditorValueAndCaret(model.fontResources, model.activeContent(), 4)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(model.fontResources, zoom)
		languageInteractionStep(t, app)
		row := model.projectTreeRowID(project, "main.go")
		fileContextRightClick(t, app, model, state, row)
		if !model.fileContext.open || model.fileContext.path != "main.go" || model.activePath != "main.go" || model.languageUI.contextOpen {
			t.Fatalf("right click did not target the file at zoom %d: %+v", zoom, model.fileContext)
		}
		menu := editorContextBounds(t, model, "file-context")
		if menu.Width > 240.1 || menu.X < 0 || menu.Y < 0 {
			t.Fatalf("file menu is not a compact popup: %+v", menu)
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
		languageInteractionStep(t, app)
	}
	model.setZoom(model.fontResources, 100)
	languageInteractionStep(t, app)
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	editorContextClick(t, app, model, state, "file-context-copy-relative")
	if clipboard != "main.go" {
		t.Fatalf("copied relative path = %q", clipboard)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	editorContextClick(t, app, model, state, "file-context-rename")
	if model.fileContext.dialog != "rename" {
		t.Fatal("rename did not show the name dialog")
	}
	if editor := module.ContentRef[htmlui.Control](model.fontResources, model.editor.control); editor == nil || !editor.Disabled {
		t.Fatal("name dialog did not disable background editor controls")
	}
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "renamed.go"})
	languageInteractionStep(t, app)
	if model.fileContext.name != "renamed.go" {
		t.Fatalf("rename input did not accept typing: %+v", model.fileContext)
	}
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if model.fileContext.dialog != "" || model.activePath != "renamed.go" || model.activeContent() != "unsaved 中文🙂\n" || !model.activeFile().Dirty || len(model.activeFile().undo) == 0 {
		t.Fatalf("rename lost the active unsaved buffer: path=%q content=%q dialog=%+v", model.activePath, model.activeContent(), model.fileContext)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("old path still exists after rename: %v", err)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "renamed.go"))
	editorContextClick(t, app, model, state, "file-context-refresh")
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	if !model.activeFile().Dirty || model.activeContent() != "unsaved 中文🙂\n" {
		t.Fatal("refresh discarded unsaved edits")
	}
	languageInteractionStep(t, app)
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "renamed.go"))
	editorContextClick(t, app, model, state, "file-context-new-file")
	languageInteractionStep(t, app)
	if model.fileContext.dialog != "new-file" {
		t.Fatalf("new file did not show its dialog: %+v", model.fileContext)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "created.txt"})
	languageInteractionStep(t, app)
	if model.fileContext.name != "created.txt" {
		t.Fatalf("new file input did not accept typing: %+v", model.fileContext)
	}
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if model.activePath != "created.txt" {
		t.Fatalf("new file was not opened: active=%q error=%q", model.activePath, model.fileContext.err)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "created.txt"))
	editorContextClick(t, app, model, state, "file-context-delete")
	if _, err := os.Stat(filepath.Join(project.Path, "created.txt")); err != nil {
		t.Fatal("file was deleted before confirmation")
	}
	editorContextClick(t, app, model, state, "file-dialog-cancel")
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(project, "created.txt"))
	editorContextClick(t, app, model, state, "file-context-delete")
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Path, "created.txt")); !os.IsNotExist(err) || project.Files["created.txt"] != nil {
		t.Fatalf("delete did not remove file and tab: %v", err)
	}
}

func TestFileContextTargetsScrolledRowsAndInactiveProjects(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	for i := range 50 {
		if err := os.WriteFile(filepath.Join(project.Path, fmt.Sprintf("item-%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	rowID := model.projectTreeRowID(project, "item-35.txt")
	treeID := treeRowID(project.Path)
	treeNode, _ := model.fontResources.HTMLNode(model.surface, treeID)
	area, ok := treeNode.ScrollContainer()
	if !ok {
		t.Fatal("missing file tree scroll area")
	}
	scroll := module.ContentMut[scene.UIScroll](model.fontResources, area)
	for index, entry := range project.visibleTreeRows() {
		if project.Entries[entry].Path == "item-35.txt" {
			scroll.Position[1] = 40 - 5 - float32(index*pencodeTreeRowHeight)
			break
		}
	}
	languageInteractionStep(t, app)
	row := editorContextBounds(t, model, rowID)
	x, y := float64(row.X+row.Width/2), float64(row.Y+scroll.Position.Y()+row.Height/2)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	if model.fileContext.path != "item-35.txt" || model.activePath != "main.go" {
		t.Fatalf("scrolled tree targeted the wrong file: %+v", model.fileContext)
	}
	model.fileContext = pencodeFileContext{}
	other := &pencodeProject{Path: t.TempDir(), Name: "other", Files: make(map[string]*openFile)}
	model.projects = append(model.projects, other)
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	fileContextRightClick(t, app, model, state, treeRowID(other.Path)+"-summary")
	if model.fileContext.project != other || model.fileContext.path != "" || model.activeProjectState() != project {
		t.Fatal("right click switched the active project or lost the target root")
	}
}

func TestFileContextIdleDoesNotAllocate(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	local, _ := module.ContentResource[*input.State](model.fontResources)
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileContext(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("idle file context allocated %g times", allocations)
	}
	fileContextRightClick(t, app, model, state, model.projectTreeRowID(model.activeProjectState(), "main.go"))
	local, _ = module.ContentResource[*input.State](model.fontResources)
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileContext(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("open idle file context allocated %g times", allocations)
	}
	field := module.ContentRef[engineui.TextField](model.fontResources, model.editor.control)
	if field == nil {
		t.Fatal("file context replaced the native editor")
	}
}
