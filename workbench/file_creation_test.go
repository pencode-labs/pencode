package workbench

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestFileCreationInlineValidationAndCancellation(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	world, project := model.fontResources, model.activeProjectState()
	resourceTestWrite(t, project, "src/nested/existing.txt", "saved")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	clear(project.ExpandedDirs)
	project.tree.revision++
	model.fileContext = pencodeFileContext{project: project, path: "src/nested", directory: true, open: true}
	model.handleFileContextEvent(world, "file-context-new-folder", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	if !project.ExpandedDirs["src"] || !project.ExpandedDirs["src/nested"] {
		t.Fatal("creation did not expand the destination's ancestors")
	}
	control, ok := world.HTMLControl(model.surface, "file-tree-name")
	field := world.Mut[engineui.TextField](control)
	if !ok || field == nil || !field.Focused {
		t.Fatalf("inline name input was not focused: control=%v found=%v field=%+v context=%+v entries=%+v rows=%v", control, ok, field, model.fileContext, project.Entries, project.visibleTreeRows(model))
	}
	if editor := world.Ref[htmlui.Control](model.editor.control); editor.Disabled {
		t.Fatal("inline creation disabled the background editor")
	}
	parent := editorContextBounds(t, model, model.projectTreeRowID(project, "src/nested"))
	row := editorContextBounds(t, model, "file-tree-create")
	if row.Y != parent.Y+pencodeTreeRowHeight || row.Height != pencodeTreeRowHeight {
		t.Fatalf("creation row was not inserted directly below its parent: parent=%+v row=%+v", parent, row)
	}
	key := func(key input.KeyCode) {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	typeName := func(name string) {
		field := world.Mut[engineui.TextField](control)
		field.Anchor = 0
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: name})
		languageInteractionStep(t, app)
	}
	for _, name := range []string{"", "..", "existing.txt"} {
		if name != "" {
			typeName(name)
		}
		key(input.KeyEnter)
		if model.fileContext.err == "" || !model.fileContext.inline() || model.fileContext.pending {
			t.Fatalf("invalid name %q did not retain inline validation: %+v", name, model.fileContext)
		}
		editorContextBounds(t, model, "file-tree-error")
	}
	typeName("资料")
	if model.fileContext.err != "" {
		t.Fatal("editing the name did not clear validation")
	}
	key(input.KeyEnter)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if info, err := os.Stat(filepath.Join(project.Path, "src/nested/资料")); err != nil || !info.IsDir() {
		t.Fatalf("inline folder creation failed: %v", err)
	}
	if model.fileContext.dialog != "" {
		t.Fatal("successful creation did not remove the input")
	}
	// Selecting a file creates a sibling in its containing directory.
	model.fileContext = pencodeFileContext{project: project, path: "src/nested/existing.txt", open: true}
	model.handleFileContextEvent(world, "file-context-new-file", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	if model.fileContext.path != "src/nested" || model.fileContext.depth != 2 {
		t.Fatalf("file creation chose the wrong directory: %+v", model.fileContext)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "cancelled.txt"})
	languageInteractionStep(t, app)
	key(input.KeyEscape)
	if model.fileContext.dialog != "" {
		t.Fatal("Escape did not dismiss the input")
	}
	if _, err := os.Stat(filepath.Join(project.Path, "src/nested/cancelled.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancelled creation wrote a file: %v", err)
	}
}

func TestFileCreationVirtualTreeAndInactiveProject(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	world, active := model.fontResources, model.activeProjectState()
	other := &pencodeProject{Path: t.TempDir(), Name: "other", Files: make(map[string]*openFile), Collapsed: true}
	attachPencodeProject(t, other, active.backend)
	t.Cleanup(other.stopScan)
	model.projects = append(model.projects, other)
	for i := range 100 {
		name := fmt.Sprintf("folder-%03d", i)
		other.Entries = append(other.Entries, treeEntry{Path: name, Name: name, IsDir: true})
	}
	model.fileContext = pencodeFileContext{project: other, path: "folder-099", directory: true, open: true}
	model.handleFileContextEvent(world, "file-context-new-file", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	control, ok := world.HTMLControl(model.surface, "file-tree-name")
	field := world.Ref[engineui.TextField](control)
	if !ok || field == nil || !field.Focused || other.Collapsed || model.activeProjectState() != active {
		t.Fatal("creation failed to reveal/focus the inactive project's destination")
	}
	row := editorContextBounds(t, model, "file-tree-create")
	viewport := editorContextBounds(t, model, treeRowID(other.Path))
	scroll := world.Ref[scene.UIScroll](other.tree.area)
	if scroll == nil || row.Y+scroll.Position.Y() < viewport.Y || row.Y+scroll.Position.Y()+row.Height > viewport.Y+viewport.Height {
		t.Fatalf("virtualized input was outside the viewport: row=%+v viewport=%+v scroll=%+v", row, viewport, scroll)
	}
	if n := testing.AllocsPerRun(100, func() { other.visibleTreeRows(model); model.syncTreeScroll(world) }); n != 0 {
		t.Fatalf("stable inline tree allocated %g times", n)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	// An empty project still has a first row available for inline creation.
	other.Entries = nil
	other.tree.revision++
	model.fileContext = pencodeFileContext{project: other, directory: true, open: true}
	model.handleFileContextEvent(world, "file-context-new-folder", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	editorContextBounds(t, model, "file-tree-create")
	if rows := other.visibleTreeRows(model); len(rows) != 1 || rows[0] != -1 {
		t.Fatalf("empty project's input row was lost: %v", rows)
	}
}

func TestFileCreationBackendFailureAndClickAway(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	world, project := model.fontResources, model.activeProjectState()
	model.fileContext = pencodeFileContext{project: project, directory: true, open: true}
	model.handleFileContextEvent(world, "file-context-new-file", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	// Simulate a name created by another process after the last scan.
	resourceTestWrite(t, project, "raced.txt", "existing content")
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "raced.txt"})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	_ = awaitFileIO(t, model)
	if content, err := os.ReadFile(filepath.Join(project.Path, "raced.txt")); err != nil || string(content) != "existing content" {
		t.Fatalf("failed creation changed the existing file: %q %v", content, err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if !model.fileContext.inline() || model.fileContext.name != "raced.txt" || model.fileContext.err == "" || model.fileContext.pending {
		t.Fatalf("backend failure did not retain the name and inline error: %+v", model.fileContext)
	}
	editorContextBounds(t, model, "file-tree-error")
	control, _ := world.HTMLControl(model.surface, "file-tree-name")
	field := world.Mut[engineui.TextField](control)
	field.Anchor = 0
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "created.txt"})
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "editor")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	if model.fileContext.inline() || model.activePath != "created.txt" {
		t.Fatalf("clicking away did not commit the corrected name: %+v", model.fileContext)
	}
	model.fileContext = pencodeFileContext{project: project, directory: true, open: true}
	model.handleFileContextEvent(world, "file-context-new-folder", "")
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	editorContextClick(t, app, model, state, "editor")
	if model.fileContext.inline() || model.fileIO != nil && len(model.fileIO.pending) != 0 {
		t.Fatal("clicking away from an empty name did not cancel creation")
	}
}
