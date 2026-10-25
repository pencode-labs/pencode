package workbench

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
)

func TestPencodeTabContextTargetClipboardAndKeyboard(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	project, world := model.activeProjectState(), model.fontResources
	for i := range 20 {
		name := fmt.Sprintf("document-%02d.txt", i)
		project.Files[name] = &openFile{Path: name, Name: name}
		project.Tabs = append(project.Tabs, name)
	}
	model.selectProjectTab(project, "document-19.txt")
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	rightClick := func(ref string) {
		t.Helper()
		scroll := module.ContentRef[scene.UIScroll](world, model.tabScroll.area)
		r := editorContextBounds(t, model, ref)
		scale := float64(model.settings.Zoom) / 100
		x, y := float64(r.X+scroll.Position.X()+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
	}
	for _, zoom := range []int{100, 150, 70} {
		model.settings.Zoom = zoom
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		rightClick("file-tab:document-18.txt")
		if !model.tabContext.open || model.tabContext.path != "document-18.txt" || model.activePath != "document-19.txt" {
			t.Fatalf("right click targeted wrong tab at zoom %d: %+v", zoom, model.tabContext)
		}
		menu, root := editorContextBounds(t, model, "tab-context"), editorContextBounds(t, model, "pencode-root")
		last := editorContextBounds(t, model, "tab-context-copy-relative")
		if menu.X < root.X || menu.X+menu.Width > root.X+root.Width || menu.Y+menu.Height > root.Y+root.Height || last.Y+last.Height > menu.Y+menu.Height {
			t.Fatalf("tab menu is clipped: menu=%+v root=%+v last=%+v", menu, root, last)
		}
		local, _ := module.ContentResource[*input.State](world)
		if n := testing.AllocsPerRun(100, func() { model.updateTabContext(world, local) }); n != 0 {
			t.Fatalf("idle tab context allocated %g times", n)
		}
		editorContextClick(t, app, model, state, "tab-context-copy-relative")
		if clipboard != "document-18.txt" || model.tabContext.open {
			t.Fatalf("relative path copy failed: %q", clipboard)
		}
	}
	rightClick("active-tab")
	editorContextClick(t, app, model, state, "tab-context-copy-path")
	if clipboard != filepath.Join(project.Path, "document-19.txt") {
		t.Fatalf("absolute path copy failed: %q", clipboard)
	}
	rightClick("active-tab")
	if model.tabContextEnabled(2) {
		t.Fatal("close right is enabled on the last tab")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyUp})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyUp})
	languageInteractionStep(t, app)
	if model.tabContext.index != 6 {
		t.Fatal("keyboard did not select the last menu item")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if model.tabContext.open {
		t.Fatal("Escape did not dismiss tab context")
	}
	rightClick("file-tab:document-18.txt")
	editorContextClick(t, app, model, state, "tab-context-right")
	if project.Files["document-19.txt"] != nil || project.Files["document-18.txt"] == nil || model.activePath != "document-18.txt" {
		t.Fatal("close right did not act on the clicked tab")
	}
	local, _ := module.ContentResource[*input.State](world)
	if n := testing.AllocsPerRun(100, func() { model.updateTabContext(world, local) }); n != 0 {
		t.Fatalf("closed idle tab context allocated %g times", n)
	}
}

func TestPencodeTabContextCloseActions(t *testing.T) {
	for _, tc := range []struct {
		action pencodeCloseAction
		want   []string
	}{
		{pencodeCloseTab, []string{"a.go", "c.go", "d.go"}},
		{pencodeCloseOtherTabs, []string{"b.go"}},
		{pencodeCloseRightTabs, []string{"a.go", "b.go"}},
		{pencodeCloseSavedTabs, []string{"a.go", "d.go"}},
		{pencodeCloseAllTabs, nil},
	} {
		t.Run(fmt.Sprint(tc.action), func(t *testing.T) {
			project := &pencodeProject{Path: t.TempDir(), Files: make(map[string]*openFile), Tabs: []string{"a.go", "b.go", "c.go", "d.go"}, ActivePath: "c.go"}
			for _, path := range project.Tabs {
				project.Files[path] = &openFile{Path: path, Name: path, Content: "unsaved", Dirty: path == "a.go" || path == "d.go"}
			}
			model := &workspaceModel{projects: []*pencodeProject{project}}
			model.syncActiveProjectFields()
			original := slices.Clone(project.Tabs)
			model.closeProjectTabs(tc.action, project, "b.go")
			if model.closing.action != pencodeCloseNone {
				if len(model.closing.targets) > 1 {
					model.resolveClose(false)
				}
				model.clearCloseRequest()
				if !slices.Equal(project.Tabs, original) || !project.Files["a.go"].Dirty || !project.Files["d.go"].Dirty {
					t.Fatal("cancelling batch close changed the open buffers")
				}
				model.closeProjectTabs(tc.action, project, "b.go")
				for model.closing.action != pencodeCloseNone {
					model.resolveClose(false)
				}
			}
			if !slices.Equal(project.Tabs, tc.want) || len(project.Files) != len(tc.want) {
				t.Fatalf("remaining tabs=%v want=%v", project.Tabs, tc.want)
			}
		})
	}
}
