package workbench

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
)

func fileDragPoint(t *testing.T, model *workspaceModel, ref string) (float64, float64) {
	t.Helper()
	bounds := editorContextBounds(t, model, ref)
	zoom := float64(model.settings.Zoom) / 100
	return float64(bounds.X+bounds.Width/2) * zoom, float64(bounds.Y+bounds.Height/2) * zoom
}

func fileDragStart(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string) {
	t.Helper()
	x, y := fileDragPoint(t, model, ref)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	if model.fileDrag.source == nil {
		t.Fatal("press did not arm a resource drag")
	}
}

func fileDragOver(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string) {
	t.Helper()
	x, y := fileDragPoint(t, model, ref)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	languageInteractionStep(t, app)
	if !model.fileDrag.active {
		t.Fatal("moving the held resource did not start dragging")
	}
}

func fileDragRelease(t *testing.T, app *onix.App, state *input.State) {
	t.Helper()
	pointer := state.Pointer()
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: float64(pointer.X), Y: float64(pointer.Y)})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
}

func TestFileDragMovesBuffersAndFolderToRoot(t *testing.T) {
	for _, zoom := range []int{70, 100, 150} {
		t.Run(strconv.Itoa(zoom), func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			project := model.activeProjectState()
			resourceTestWrite(t, project, "target/keep.txt", "keep")
			resourceTestWrite(t, project, "src/nested/note.txt", "note")
			model.startProjectScan(project, false)
			waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
			model.setZoom(model.fontResources, zoom)
			model.updateActiveContent("unsaved drag text")
			before := model.activeFile()
			model.fileClipboard = pencodeFileClipboard{project: project, path: "target/keep.txt"}
			languageInteractionStep(t, app)
			fileDragStart(t, app, model, state, model.projectTreeRowID(project, "main.go"))
			fileDragOver(t, app, model, state, model.projectTreeRowID(project, "target"))
			if !model.fileDrag.isTarget(project, "target") {
				t.Fatal("folder was not selected as the drop target")
			}
			fileDragRelease(t, app, state)
			waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
			if model.activePath != "target/main.go" || model.activeContent() != before.Content || !model.activeFile().Dirty || len(model.activeFile().undo) != len(before.undo) {
				t.Fatal("drag lost the active document or its edit history")
			}
			if model.fileClipboard.path != "target/keep.txt" {
				t.Fatal("drag changed an unrelated clipboard resource")
			}
			if _, err := os.Stat(filepath.Join(project.Path, "main.go")); !os.IsNotExist(err) {
				t.Fatalf("source remains after move: %v", err)
			}
			// A whole folder can be moved into another folder and back to root.
			languageInteractionStep(t, app)
			fileDragStart(t, app, model, state, model.projectTreeRowID(project, "src"))
			fileDragOver(t, app, model, state, model.projectTreeRowID(project, "target"))
			fileDragRelease(t, app, state)
			waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
			languageInteractionStep(t, app)
			fileDragStart(t, app, model, state, model.projectTreeRowID(project, "target/src"))
			fileDragOver(t, app, model, state, treeRowID(project.Path)+"-summary")
			fileDragRelease(t, app, state)
			waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
			if content, err := os.ReadFile(filepath.Join(project.Path, "src/nested/note.txt")); err != nil || string(content) != "note" {
				t.Fatalf("folder did not return to root: %q, %v", content, err)
			}
			if project.Collapsed {
				t.Fatal("drop on project header also triggered its collapse button")
			}
		})
	}
}

func TestFileDragCancelClickAndHoverExpansion(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	resourceTestWrite(t, project, "target/nested/note.txt", "note")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	project.ExpandedDirs = map[string]bool{"target": false}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	fileDragStart(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	fileDragOver(t, app, model, state, model.projectTreeRowID(project, "target"))
	if model.fileDrag.expandAt.IsZero() {
		t.Fatal("hovering a collapsed folder did not schedule expansion")
	}
	model.fileDrag.expandAt = time.Now().Add(-time.Second)
	languageInteractionStep(t, app)
	if !project.ExpandedDirs["target"] {
		t.Fatal("hover did not expand the drop folder")
	}
	local, _ := module.ContentResource[*input.State](model.fontResources)
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileDrag(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("stationary drag allocated %g times", allocations)
	}
	pointer := local.Pointer()
	offset := float64(1)
	if allocations := testing.AllocsPerRun(100, func() {
		local.MoveMouse(float64(pointer.X)+offset, float64(pointer.Y))
		offset = -offset
		model.updateFileDrag(model.fontResources, local)
	}); allocations != 0 {
		t.Fatalf("moving drag allocated %g times", allocations)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	fileDragRelease(t, app, state)
	if model.fileDrag.active || project.Files["main.go"] == nil {
		t.Fatal("Escape did not cancel the move")
	}
	// Pressing and releasing without crossing the threshold is still a click.
	fileDragStart(t, app, model, state, model.projectTreeRowID(project, "target"))
	fileDragRelease(t, app, state)
	if project.ExpandedDirs["target"] {
		t.Fatal("a normal folder click stopped toggling expansion")
	}
	local, _ = module.ContentResource[*input.State](model.fontResources)
	if allocations := testing.AllocsPerRun(100, func() { model.updateFileDrag(model.fontResources, local) }); allocations != 0 {
		t.Fatalf("idle drag handler allocated %g times", allocations)
	}
}

func TestFileDragInvalidTargetsAndCoalescedInput(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project := model.activeProjectState()
	resourceTestWrite(t, project, "src/nested/note.txt", "note")
	resourceTestWrite(t, project, "target/keep.txt", "keep")
	project.ExpandedDirs = map[string]bool{"src": true}
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	languageInteractionStep(t, app)
	fileDragStart(t, app, model, state, model.projectTreeRowID(project, "src"))
	fileDragOver(t, app, model, state, model.projectTreeRowID(project, "src/nested"))
	if model.fileDrag.target != nil {
		t.Fatal("a folder's descendant was accepted as its drop target")
	}
	fileDragRelease(t, app, state)
	fileDragStart(t, app, model, state, model.projectTreeRowID(project, "src"))
	fileDragOver(t, app, model, state, model.projectTreeRowID(project, "target"))
	fileDragOver(t, app, model, state, model.projectTreeRowID(project, "src"))
	fileDragRelease(t, app, state)
	if !project.ExpandedDirs["src"] {
		t.Fatal("returning to the source row also clicked and collapsed it")
	}
	fileDragStart(t, app, model, state, model.projectTreeRowID(project, "main.go"))
	fileDragOver(t, app, model, state, "editor-body")
	if model.fileDrag.target != nil {
		t.Fatal("the editor was accepted as a drop target")
	}
	fileDragRelease(t, app, state)
	if _, err := os.Stat(filepath.Join(project.Path, "main.go")); err != nil {
		t.Fatalf("dropping outside the tree moved the file: %v", err)
	}
	x, y := fileDragPoint(t, model, model.projectTreeRowID(project, "main.go"))
	tx, ty := fileDragPoint(t, model, model.projectTreeRowID(project, "target"))
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: tx, Y: ty})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: tx, Y: ty})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	if _, err := os.Stat(filepath.Join(project.Path, "target/main.go")); err != nil {
		t.Fatalf("coalesced input lost the drag's press origin: %v", err)
	}
}

func TestProjectResourceDragRejectsCollisionAndDescendant(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "src/nested/note.txt", "source")
	resourceTestWrite(t, project, "target/src/keep.txt", "keep")
	for _, directory := range []string{"target", "src", "src/nested"} {
		if err := moveResourceAndWait(t, model, project, "src", project, directory); err == nil {
			t.Fatalf("accepted conflicting or descendant move into %q", directory)
		}
	}
	if content, err := os.ReadFile(filepath.Join(project.Path, "src/nested/note.txt")); err != nil || string(content) != "source" {
		t.Fatal("rejected move changed the source")
	}
	other := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile)}
	attachPencodeProject(t, other, project.backend)
	t.Cleanup(other.stopScan)
	model.projects = append(model.projects, other)
	openProjectFile(project, "src/nested/note.txt")
	model.syncActiveProjectFields()
	model.updateActiveContent("unsaved cross-project text")
	model.fileClipboard = pencodeFileClipboard{project: project, path: "src/nested/note.txt"}
	if err := moveResourceAndWait(t, model, project, "src", other, ""); err != nil {
		t.Fatal(err)
	}
	if file := other.Files["src/nested/note.txt"]; file == nil || !file.Dirty || file.Content != "unsaved cross-project text" || len(file.undo) == 0 {
		t.Fatal("cross-project drag lost the edited buffer")
	}
	if model.fileClipboard.project != other || model.fileClipboard.path != "src/nested/note.txt" {
		t.Fatal("clipboard did not follow the moved resource")
	}
}
