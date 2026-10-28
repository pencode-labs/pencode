package workbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

func TestPencodeTreeToggleKeepsScrolledRowPositions(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	world, project := model.fontResources, model.activeProjectState()
	project.Entries = nil
	project.ExpandedDirs = make(map[string]bool)
	for i := range 30 {
		name := fmt.Sprintf("folder-%02d", i)
		project.Entries = append(project.Entries, treeEntry{Path: name, Name: name, IsDir: true})
		for j := range 7 {
			child := fmt.Sprintf("child-%02d-%02d", i, j)
			project.Entries = append(project.Entries, treeEntry{Path: name + "/" + child, Name: child, Depth: 1})
		}
	}
	project.tree.revision++
	model.revealProject, model.revealPath = nil, ""
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	verify := func() {
		t.Helper()
		viewport := editorContextBounds(t, model, treeRowID(project.Path))
		scroll := module.ContentRef[scene.UIScroll](world, project.tree.area)
		for i, index := range project.visibleTreeRows(model) {
			if i < project.tree.first || i >= project.tree.end {
				continue
			}
			entry := project.Entries[index]
			bounds := editorContextBounds(t, model, model.projectTreeRowID(project, entry.Path))
			want := viewport.Y + 5 + float32(i*pencodeTreeRowHeight)
			if math.Abs(float64(bounds.Y-want)) > .01 {
				t.Fatalf("%s bounds=%+v want=%v first=%d end=%d", entry.Path, bounds, want, project.tree.first, project.tree.end)
			}
			found := false
			y := bounds.Y + scroll.Position.Y()
			for entity, access := range ecs.Query[scene.Text2D](app.World).All {
				if access.Ref().Value != entry.Name {
					continue
				}
				found = true
				transform := ecs.GetRef[scene.UIGlobalTransform](app.World, entity)
				if transform == nil || transform.Matrix[13] < y || transform.Matrix[13] >= y+bounds.Height {
					t.Fatalf("%s bounds=%+v scroll=%v transform=%+v", entry.Path, bounds, scroll.Position, transform)
				}
			}
			if !found && y >= viewport.Y && y+bounds.Height <= viewport.Y+viewport.Height {
				t.Fatalf("visible row %s has no text in the update frame", entry.Path)
			}
		}
	}
	verify()
	for _, offset := range []float32{0, -300, -600} {
		module.ContentMut[scene.UIScroll](world, project.tree.area).Position[1] = offset
		languageInteractionStep(t, app)
		for _, i := range []int{1, 5, 10, 15, 20, 10, 5, 1} {
			name := fmt.Sprintf("folder-%02d", i)
			model.toggleProjectDirectory(project, name, 0)
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			verify()
		}
	}
}
