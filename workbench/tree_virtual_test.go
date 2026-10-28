package workbench

import (
	"fmt"
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"slices"
	"testing"
)

func TestPencodeTreeVirtualWindowAndHitTargets(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	world, project := model.fontResources, model.activeProjectState()
	const count = 10000
	project.Entries = make([]treeEntry, count)
	for i := range project.Entries {
		name := fmt.Sprintf("item-%05d.txt", i)
		project.Entries[i] = treeEntry{Path: name, Name: name, Openable: true}
	}
	project.tree.revision++
	model.revealProject, model.revealPath = nil, ""
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	treeNode, _ := world.HTMLNode(model.surface, treeRowID(project.Path))
	area, ok := treeNode.ScrollContainer()
	if !ok {
		t.Fatal("missing virtual tree viewport")
	}
	verify := func(target int) {
		t.Helper()
		scroll := module.ContentMut[scene.UIScroll](world, area)
		mounted := 0
		for _, access := range module.ContentQuery[htmlui.Component[treeProps, struct{}]](world) {
			if access.Get().Props.Project == project {
				mounted++
			}
		}
		limit := int(scroll.ViewportSize.Y())/pencodeTreeRowHeight + 2*pencodeTreeOverscan + 2
		if mounted > limit || mounted == 0 {
			t.Fatalf("mounted %d rows, viewport bound %d", mounted, limit)
		}
		if scroll.ContentSize.Y() < count*pencodeTreeRowHeight {
			t.Fatal("virtualization truncated scroll extent")
		}
		entry := project.Entries[target]
		row := editorContextBounds(t, model, model.projectTreeRowID(project, entry.Path))
		if row.Height != pencodeTreeRowHeight {
			t.Fatalf("virtual row has variable height: %+v", row)
		}
		owner, path, directory, depth := model.fileTreeTarget(world, row.X+row.Width/2, row.Y+scroll.Position.Y()+row.Height/2)
		if owner != project || path != entry.Path || directory || depth != 0 {
			t.Fatalf("virtual row hit target=%p %q %v %d", owner, path, directory, depth)
		}
	}
	verify(0)
	first := slices.Clone(project.visibleTreeRows(model))
	module.ContentMut[scene.UIScroll](world, area).Position[1] = -5000 * pencodeTreeRowHeight
	languageInteractionStep(t, app)
	verify(5000)
	if _, exists := world.HTMLNode(model.surface, model.projectTreeRowID(project, project.Entries[0].Path)); exists {
		t.Fatalf("offscreen row was retained: first=%d end=%d scroll=%+v", project.tree.first, project.tree.end, module.ContentRef[scene.UIScroll](world, area))
	}
	if !slices.Equal(first, project.visibleTreeRows(model)) {
		t.Fatal("scroll rebuilt the flattened rows incorrectly")
	}
	if n := testing.AllocsPerRun(100, func() { model.syncTreeScroll(world); project.visibleTreeRows(model) }); n != 0 {
		t.Fatalf("stable tree viewport allocated %g", n)
	}
	// Navigation can reveal a row that has no mounted component.
	entry := project.Entries[count-1]
	project.Files[entry.Path] = &openFile{Path: entry.Path, Name: entry.Name}
	project.ActivePath = entry.Path
	model.syncActiveProjectFields()
	model.queueProjectFileReveal(project)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	verify(count - 1)
	if model.revealProject != nil {
		t.Fatal("offscreen reveal remained pending")
	}
}

func TestPencodeTreeFlattenCacheInvalidation(t *testing.T) {
	model := workspaceModel{}
	project := pencodeProject{ExpandedDirs: map[string]bool{}, Entries: []treeEntry{
		{Path: "folder", IsDir: true}, {Path: "folder/child", Depth: 1}, {Path: "folder/nested", Depth: 1, IsDir: true}, {Path: "folder/nested/file", Depth: 2}, {Path: "last"},
	}}
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int{0, 4}) {
		t.Fatal(got)
	}
	model.toggleProjectDirectory(&project, "folder", 0)
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int{0, 1, 2, 4}) {
		t.Fatal(got)
	}
	model.toggleProjectDirectory(&project, "folder/nested", 1)
	if got := project.visibleTreeRows(&model); len(got) != 5 {
		t.Fatal(got)
	}
	model.collapseProjectDirectories(&project)
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int{0, 4}) {
		t.Fatal(got)
	}
	project.Entries = append(project.Entries, treeEntry{Path: "new"})
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int{0, 4, 5}) {
		t.Fatal(got)
	}
}
