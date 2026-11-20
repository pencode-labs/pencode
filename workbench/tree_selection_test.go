package workbench

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
)

func TestFileTreeMultiSelectionActions(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	names := []string{"a.docx", "b.pptx", "c.xlsx"}
	for _, name := range names {
		resourceTestWrite(t, project, name, name)
	}
	resourceTestWrite(t, project, "copies/keep.txt", "keep")
	resourceTestWrite(t, project, "moved/keep.txt", "keep")
	model.startProjectScan(project, false)
	waitTree := func() {
		waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
		languageInteractionStep(t, app)
	}
	waitTree()

	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	row := func(name string) string { return model.projectTreeRowID(project, name) }
	click := func(key input.KeyCode, name string) {
		if key != 0 {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		}
		editorContextClick(t, app, model, state, row(name))
		if key != 0 {
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
			languageInteractionStep(t, app)
		}
	}
	click(0, names[0])
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	languageInteractionStep(t, app)
	active := model.activePath
	click(primary, names[2])
	selection := &project.tree.selection
	if !slices.Equal(selection.targets(names[2], true), []string{names[0], names[2]}) || model.activePath != active {
		t.Fatalf("modifier click did not select two files independently of the editor: %+v", selection)
	}
	click(primary, names[2])
	click(input.KeyLeftShift, names[2])
	if !slices.Equal(selection.targets(names[2], true), names) {
		t.Fatalf("range selection missed a row: %+v", selection)
	}
	for _, name := range names {
		node, ok := model.fontResources.HTMLNode(model.surface, row(name))
		if !ok {
			t.Fatalf("missing row %s", name)
		}
		source, err := node.Serialize()
		if err != nil || !strings.Contains(source, `aria-selected="true"`) || !strings.Contains(source, `class="pencode-tree-row selected"`) {
			t.Fatalf("missing selection highlight for %s: %s (%v)", name, source, err)
		}
	}

	copyKey := keymapState(pencodeBindingChord("Primary+C"), runtime.GOOS)
	if !model.handleShortcutKeys(model.fontResources, copyKey, runtime.GOOS) || !slices.Equal(model.fileClipboard.paths, names) {
		t.Fatalf("copy shortcut missed the selection: %+v", model.fileClipboard)
	}
	fileContextRightClick(t, app, model, state, row("copies"))
	editorContextClick(t, app, model, state, "file-context-paste")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitTree()
	for _, name := range names {
		if content, err := os.ReadFile(filepath.Join(project.Path, "copies", name)); err != nil || string(content) != name {
			t.Fatalf("copy of %s: %q, %v", name, content, err)
		}
	}

	click(0, names[0])
	click(input.KeyLeftShift, names[2])
	fileDragStart(t, app, model, state, row(names[1]))
	fileDragOver(t, app, model, state, row("moved"))
	fileDragRelease(t, app, state)
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	waitTree()
	moved := []string{"moved/a.docx", "moved/b.pptx", "moved/c.xlsx"}
	for i, name := range moved {
		if _, err := os.Stat(filepath.Join(project.Path, names[i])); !os.IsNotExist(err) {
			t.Fatalf("original %s survived the move: %v", names[i], err)
		}
		if content, err := os.ReadFile(filepath.Join(project.Path, name)); err != nil || string(content) != names[i] {
			t.Fatalf("moved %s: %q, %v", name, content, err)
		}
	}
	fileContextRightClick(t, app, model, state, row(moved[0]))
	editorContextClick(t, app, model, state, "file-context-delete")
	if !slices.Equal(model.fileContext.paths, moved) {
		t.Fatalf("delete confirmation missed the moved selection: %+v", model.fileContext)
	}
	editorContextClick(t, app, model, state, "file-dialog-submit")
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	for _, name := range moved {
		if _, err := os.Stat(filepath.Join(project.Path, name)); !os.IsNotExist(err) {
			t.Fatalf("selected file %s survived deletion: %v", name, err)
		}
	}
}

func TestFileTreePartialMoveRetainsPendingCut(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "a.txt", "a")
	resourceTestWrite(t, project, "z.txt", "z")
	resourceTestWrite(t, project, "target/keep.txt", "keep")
	openProjectFileAndWait(t, model, project, "a.txt")
	model.updateActiveContent("unsaved")
	model.fileClipboard = pencodeFileClipboard{project: project, paths: []string{"a.txt", "missing.txt", "z.txt"}, cut: true}
	if err := model.pasteProjectResource(project, "target"); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, model); err == nil {
		t.Fatal("expected missing source to interrupt the batch")
	}
	if project.ActivePath != "target/a.txt" || model.activeContent() != "unsaved" || !model.activeFile().Dirty {
		t.Fatal("successful move lost the open buffer after a later failure")
	}
	if !slices.Equal(model.fileClipboard.paths, []string{"missing.txt", "z.txt"}) {
		t.Fatalf("cut clipboard did not retain the pending paths: %+v", model.fileClipboard)
	}
	resourceTestWrite(t, project, "missing.txt", "restored")
	if err := model.pasteProjectResource(project, "target"); err != nil {
		t.Fatal(err)
	}
	if err := awaitFileIO(t, model); err != nil {
		t.Fatal(err)
	}
	if model.fileClipboard.project != nil {
		t.Fatal("completed cut retained its clipboard")
	}
	for _, name := range []string{"a.txt", "missing.txt", "z.txt"} {
		if _, err := os.Stat(filepath.Join(project.Path, "target", name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileTreeSelectionTargetsIncludeEachSubtreeOnce(t *testing.T) {
	selection := pencodeTreeSelection{anchor: "src/nested/file.txt", paths: map[string]struct{}{
		"src": {}, "src/nested": {}, "src/nested/file.txt": {}, "src-extra": {},
	}}
	if got := selection.targets("src/nested/file.txt", true); !slices.Equal(got, []string{"src", "src-extra"}) {
		t.Fatalf("batch roots = %v", got)
	}
	if got := selection.targets("src/nested/file.txt", false); !slices.Equal(got, []string{"src", "src-extra", "src/nested", "src/nested/file.txt"}) {
		t.Fatalf("copied paths = %v", got)
	}
}
