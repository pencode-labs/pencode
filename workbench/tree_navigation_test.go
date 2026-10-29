package workbench

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/lsp"
)

func TestPencodeFileNavigationQueuesTreeReveal(t *testing.T) {
	for _, action := range []string{"open", "tab", "project", "close", "rename"} {
		t.Run(action, func(t *testing.T) {
			model, project := resourceTestModel(t)
			const target = "src/nested/target.go"
			resourceTestWrite(t, project, target, "package target\n")
			resourceTestWrite(t, project, "other.go", "package other\n")
			openProjectFileAndWait(t, model, project, target)
			openProjectFileAndWait(t, model, project, "other.go")
			project.Collapsed = true
			clear(project.ExpandedDirs)
			model.revealProject, model.revealPath = nil, ""
			want := target
			switch action {
			case "open":
				openProjectFileAndWait(t, model, project, target)
			case "tab":
				model.selectProjectTab(project, target)
			case "project":
				project.ActivePath = target
				other := &pencodeProject{Path: t.TempDir()}
				model.projects = append(model.projects, other)
				model.activateProject(other)
				model.activateProject(project)
			case "close":
				model.discardProjectTab(project, "other.go")
			case "rename":
				model.selectProjectTab(project, target)
				model.relocateProjectResources(project, project, "src", "renamed")
				model.syncActiveProjectFields()
				want = "renamed/nested/target.go"
			}
			if project.Collapsed || model.revealProject != project || model.revealPath != want {
				t.Fatalf("navigation did not reveal %s: collapsed=%v pending=%s", want, project.Collapsed, model.revealPath)
			}
			for parent := filepath.ToSlash(filepath.Dir(want)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
				if !project.ExpandedDirs[parent] {
					t.Fatalf("ancestor %s remained collapsed", parent)
				}
			}
			model.revealProject, model.revealPath = nil, ""
			model.syncActiveProjectFields()
			if model.revealProject != nil {
				t.Fatal("ordinary synchronization queued another reveal")
			}
		})
	}
}

func TestPencodeRepeatedFileNavigationDoesNotAllocate(t *testing.T) {
	model, project := resourceTestModel(t)
	const target = "src/nested/target.go"
	resourceTestWrite(t, project, target, "package target\n")
	openProjectFileAndWait(t, model, project, target)
	if allocations := testing.AllocsPerRun(100, func() {
		model.revealProject, model.revealPath = nil, ""
		model.openProjectFile(project, target)
		model.selectProjectTab(project, target)
	}); allocations != 0 {
		t.Fatalf("repeated navigation allocated %g times", allocations)
	}
	if model.revealProject != project || model.revealPath != target {
		t.Fatal("repeated navigation did not queue a reveal")
	}
}

func TestPencodeTreeRevealWaitsForScanAndSidebar(t *testing.T) {
	model, project := resourceTestModel(t)
	resourceTestWrite(t, project, "target.go", "package target\n")
	openProjectFileAndWait(t, model, project, "target.go")
	project.scan = &pencodeProjectScan{}
	model.centerRevealedTreeRow(nil)
	project.scan = nil
	model.sidebarHidden = true
	model.centerRevealedTreeRow(nil)
	if model.revealProject != project || model.revealPath != "target.go" {
		t.Fatal("pending navigation was lost before the tree could be displayed")
	}
	project.Collapsed = true
	model.centerRevealedTreeRow(nil)
	if model.revealProject != nil {
		t.Fatal("manual project collapse did not cancel the pending reveal")
	}
}

func TestPencodeNavigationCentersExplorerRows(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	world := model.fontResources
	project := model.activeProjectState()
	const directory = "src/nested"
	for i := range 80 {
		resourceTestWrite(t, project, fmt.Sprintf("%s/item-%02d.go", directory, i), "package target\n")
	}
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	for _, action := range []string{"open", "tab", "quick", "language", "locate", "first", "last", "tree"} {
		t.Run(action, func(t *testing.T) {
			target := directory + "/item-40.go"
			if action == "first" {
				target = directory + "/item-00.go"
			} else if action == "last" {
				target = directory + "/item-79.go"
			}
			openProjectFileAndWait(t, model, project, target)
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			area, ok := pencodeTreeScrollArea(world, model.surface, model.projectTreeRowID(project, target))
			if !ok {
				t.Fatal("missing tree scroll area")
			}
			world.Mut[scene.UIScroll](area).Position[1] = 0
			if action == "tree" {
				world.Mut[scene.UIScroll](area).Position[1] = -900
			} else {
				project.Collapsed = true
				clear(project.ExpandedDirs)
			}
			switch action {
			case "tree":
				for _, relative := range []string{directory + "/item-41.go", target} {
					handleTreeEvent(&module.ContentEventContext[treeProps, struct{}]{
						World: world,
						Props: &treeProps{Model: model, Project: project, Path: relative},
						Ref:   "row",
					})
					if err := awaitFileIO(t, model); err != nil {
						t.Fatal(err)
					}
					languageInteractionStep(t, app)
					if project.ActivePath != relative || model.activePath != relative {
						t.Fatalf("tree click did not activate %s", relative)
					}
					if got := world.Ref[scene.UIScroll](area).Position.Y(); got != -900 {
						t.Fatalf("tree click moved the list: %v", got)
					}
				}
			case "open", "first", "last":
				openProjectFileAndWait(t, model, project, target)
			case "tab":
				model.selectProjectTab(project, target)
			case "quick":
				model.quickQuery, model.quickCount = "item-40.go", -1
				settleQuick(t, model)
				model.openQuickMatch(world, 0)
			case "language":
				uri := lsp.FileURI(filepath.Join(project.Path, filepath.FromSlash(target)))
				if !model.navigateLanguageLocation(world, lsp.Location{URI: uri}, nil) {
					t.Fatal("language navigation failed")
				}
			case "locate":
				model.revealActiveFile()
			}
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			row := editorContextBounds(t, model, model.projectTreeRowID(project, target))
			viewport := editorContextBounds(t, model, treeRowID(project.Path))
			scroll := world.Mut[scene.UIScroll](area)
			want := min(max(viewport.Y+scroll.ViewportSize.Y()/2-row.Y-row.Height/2, -max(scroll.ContentSize.Y()-scroll.ViewportSize.Y(), 0)), 0)
			if action == "tree" {
				want = -900
			}
			if math.Abs(float64(scroll.Position.Y()-want)) > .01 {
				t.Fatalf("target row is not centered within scroll limits: want=%v scroll=%+v", want, scroll)
			}
			if model.revealProject != nil {
				t.Fatal("completed reveal remained pending")
			}
			scroll.Position[1] = -48
			languageInteractionStep(t, app)
			if got := world.Ref[scene.UIScroll](area).Position.Y(); got != -48 {
				t.Fatalf("idle layout reset manual scrolling: %v", got)
			}
		})
	}
}
