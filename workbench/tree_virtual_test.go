package workbench

import (
	"fmt"
	"slices"
	"testing"
	"unsafe"

	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
)

func TestPencodeTreeVirtualWindowAndHitTargets(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	world, project := model.fontResources, model.activeProjectState()
	const count = 10000
	project.Entries = make([]treeEntry, count)
	for i := range project.Entries {
		name := fmt.Sprintf("item-%05d.txt", i)
		project.Entries[i] = treeEntry{Path: name, Openable: true}
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
		scroll := world.Mut[scene.UIScroll](area)
		mounted := 0
		for _, access := range world.Query[htmlui.Component[treeProps, struct{}]]() {
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
	world.Mut[scene.UIScroll](area).Position[1] = -5000 * pencodeTreeRowHeight
	languageInteractionStep(t, app)
	verify(5000)
	if _, exists := world.HTMLNode(model.surface, model.projectTreeRowID(project, project.Entries[0].Path)); exists {
		t.Fatalf("offscreen row was retained: first=%d end=%d scroll=%+v", project.tree.first, project.tree.end, world.Ref[scene.UIScroll](area))
	}
	if !slices.Equal(first, project.visibleTreeRows(model)) {
		t.Fatal("scroll rebuilt the flattened rows incorrectly")
	}
	if n := testing.AllocsPerRun(100, func() { model.syncTreeScroll(world); project.visibleTreeRows(model) }); n != 0 {
		t.Fatalf("stable tree viewport allocated %g", n)
	}
	// A staged background refresh must not block navigation within the
	// visible snapshot, including rows that have no mounted component.
	project.stopScan()
	project.scan = &pencodeProjectScan{staged: true, cancel: func() {}}
	t.Cleanup(project.stopScan)
	entry := project.Entries[count-1]
	project.Files[entry.Path] = &openFile{Path: entry.Path, Name: entry.Name()}
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
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 4}) {
		t.Fatal(got)
	}
	model.toggleProjectDirectory(&project, "folder", 0)
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 1, 2, 4}) {
		t.Fatal(got)
	}
	model.toggleProjectDirectory(&project, "folder/nested", 1)
	if got := project.visibleTreeRows(&model); len(got) != 5 {
		t.Fatal(got)
	}
	model.collapseProjectDirectories(&project)
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 4}) {
		t.Fatal(got)
	}
	project.Entries = append(project.Entries, treeEntry{Path: "new"})
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 4, 5}) {
		t.Fatal(got)
	}
}

func TestPencodeTreeFlagsReleaseDefaultPaths(t *testing.T) {
	project := &pencodeProject{}
	model := workspaceModel{projects: []*pencodeProject{project}, activeProjectIndex: 0}
	for i := range 1000 {
		name := fmt.Sprintf("folder-%d", i)
		model.toggleProjectDirectory(project, name, 0)
		if !project.ExpandedDirs[name] {
			t.Fatal("directory did not expand")
		}
		model.toggleProjectDirectory(project, name, 0)
		id := "sc-folder:changes:" + name
		model.sourceAction(id)
		if !project.sourceControl.Collapsed["changes:"+name] {
			t.Fatal("source directory did not collapse")
		}
		model.sourceAction(id)
	}
	if len(project.ExpandedDirs) != 0 || len(project.sourceControl.Collapsed) != 0 {
		t.Fatal("default states retained paths after visiting directories")
	}
	if n := testing.AllocsPerRun(100, func() {
		model.toggleProjectDirectory(project, "folder", 0)
		model.toggleProjectDirectory(project, "folder", 0)
		model.sourceAction("sc-folder:changes:folder")
		model.sourceAction("sc-folder:changes:folder")
	}); n != 0 {
		t.Fatalf("warm directory toggling allocated %g times", n)
	}
}

func TestPencodeTreeReindexesReplacementAndDeepSubtrees(t *testing.T) {
	model := workspaceModel{}
	project := pencodeProject{Entries: []treeEntry{
		{Path: "folder", IsDir: true}, {Path: "folder/child", Depth: 1}, {Path: "last"},
	}}
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 2}) {
		t.Fatal(got)
	}
	// Replace in place with the same length, as a root preview can do.
	project.Entries[1] = treeEntry{Path: "sibling"}
	project.tree.entriesChanged()
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 1, 2}) {
		t.Fatal("replacement reused stale subtree boundaries", got)
	}
	project.Entries = make([]treeEntry, 258)
	for i := range 257 {
		project.Entries[i] = treeEntry{Path: fmt.Sprintf("directory-%d", i), Depth: uint16(i), IsDir: true}
	}
	project.Entries[257] = treeEntry{Path: "last"}
	if got := project.visibleTreeRows(&model); !slices.Equal(got, []int32{0, 257}) {
		t.Fatal("maximum-depth subtree was not skipped", got)
	}
	if n := testing.AllocsPerRun(100, func() {
		project.tree.revision++
		project.visibleTreeRows(&model)
	}); n != 0 {
		t.Fatalf("warm folding allocated %g times", n)
	}
}

func BenchmarkPencodeTreeRows(b *testing.B) {
	for _, count := range []int32{1024, 262144} {
		for _, expanded := range []bool{false, true} {
			b.Run(fmt.Sprintf("entries=%d/expanded=%v", count, expanded), func(b *testing.B) {
				model := workspaceModel{}
				project := pencodeProject{Entries: make([]treeEntry, count), ExpandedDirs: make(map[string]bool)}
				for i := range project.Entries {
					directory := fmt.Sprintf("folder-%03d", i/1024)
					if i%1024 == 0 {
						project.Entries[i] = treeEntry{Path: directory, IsDir: true}
						project.ExpandedDirs[directory] = expanded
					} else {
						project.Entries[i] = treeEntry{Path: fmt.Sprintf("%s/file-%04d.go", directory, i%1024), Depth: 1, Openable: true}
					}
				}
				project.visibleTreeRows(&model)
				b.ReportAllocs()
				for b.Loop() {
					project.tree.revision++
					project.visibleTreeRows(&model)
				}
				b.ReportMetric(float64(unsafe.Sizeof(treeEntry{})), "B/entry")
			})
		}
	}
}
