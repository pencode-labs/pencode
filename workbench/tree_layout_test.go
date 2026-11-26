package workbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/onix-engine/onix/scene"
)

func TestPencodeTreeRefreshKeepsScrolledRowPositions(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	world, project := model.fontResources, model.activeProjectState()
	project.Entries = nil
	project.ExpandedDirs = make(map[string]bool)
	for i := range 100 {
		name := fmt.Sprintf("folder-%02d", i)
		project.Entries = append(project.Entries, treeEntry{Path: name, IsDir: true})
		for j := range 7 {
			child := fmt.Sprintf("child-%02d-%02d", i, j)
			project.Entries = append(project.Entries, treeEntry{Path: name + "/" + child, Depth: 1})
		}
	}
	project.tree.revision++
	model.revealProject, model.revealPath = nil, ""
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	verify := func() {
		t.Helper()
		viewport := editorContextBounds(t, model, treeRowID(project.Path))
		scroll := world.Ref[scene.UIScroll](project.tree.area)
		for i, index := range project.visibleTreeRows(model) {
			want := viewport.Y + float32(5+i*pencodeTreeRowHeight)
			y := want + scroll.Position.Y()
			visible := y >= viewport.Y && y+pencodeTreeRowHeight <= viewport.Y+viewport.Height
			if i < project.tree.first || i >= project.tree.end {
				if visible {
					t.Fatalf("visible row %d outside mounted range [%d,%d)", i, project.tree.first, project.tree.end)
				}
				continue
			}
			entry := project.Entries[index]
			id := model.projectTreeRowID(project, entry.Path)
			bounds := editorContextBounds(t, model, id)
			if math.Abs(float64(bounds.Y-want)) > .01 {
				t.Fatalf("%s bounds=%+v want=%v first=%d end=%d", entry.Path, bounds, want, project.tree.first, project.tree.end)
			}
			if visible {
				control, _ := world.HTMLControl(model.surface, id)
				transform := world.Ref[scene.UIGlobalTransform](control)
				if transform == nil || math.Abs(float64(transform.Matrix[13]-y)) > .01 {
					t.Fatalf("%s hit target does not match row at %v: %+v", entry.Path, y, transform)
				}
			}
			found := false
			for entity, access := range app.World.Query[scene.Text2D]().All {
				if access.Ref().Text().String() != entry.Name() {
					continue
				}
				found = true
				transform := app.World.GetRef[scene.UIGlobalTransform](entity)
				if transform == nil || transform.Matrix[13] < y || transform.Matrix[13] >= y+bounds.Height {
					t.Fatalf("%s bounds=%+v scroll=%v transform=%+v", entry.Path, bounds, scroll.Position, transform)
				}
			}
			if !found && visible {
				t.Fatalf("visible row %s has no text at scroll %v", entry.Path, scroll.Position.Y())
			}
		}
	}
	verify()
	// Move the scroll window on consecutive frames, including partial rows.
	for _, offset := range []float32{-369, -725, -1213, -43} {
		world.Mut[scene.UIScroll](project.tree.area).Position[1] = offset
		languageInteractionStep(t, app)
		verify()
	}
	for _, offset := range []float32{0, -300, -600} {
		world.Mut[scene.UIScroll](project.tree.area).Position[1] = offset
		languageInteractionStep(t, app)
		for _, i := range []int{1, 5, 10, 15, 20, 10, 5, 1} {
			name := fmt.Sprintf("folder-%02d", i)
			model.toggleProjectDirectory(project, name, 0)
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			verify()
		}
	}
	other := &pencodeProject{Path: t.TempDir(), Name: "second"}
	model.projects = append(model.projects, other)
	// A remounted tree restores its native scroll position after layout.
	for _, offset := range []float32{-50, -1650} {
		world.Mut[scene.UIScroll](project.tree.area).Position[1] = offset
		languageInteractionStep(t, app)
		model.activateProject(other)
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		model.activateProject(project)
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		if got := world.Ref[scene.UIScroll](project.tree.area).Position.Y(); got != offset {
			t.Fatalf("restored scroll position %v, want %v", got, offset)
		}
		verify()
	}
	// Shrink the snapshot past the current window, then down to one directory.
	for _, count := range []int{50, 1} {
		project.Entries = project.Entries[:8*count]
		project.tree.entriesChanged()
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		verify()
	}
}
