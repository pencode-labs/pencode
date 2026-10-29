package workbench

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeEditorHeaderPathCache(t *testing.T) {
	var header pencodeEditorHeader
	model := &workspaceModel{}
	check := func(root, name, path string, want []string) {
		t.Helper()
		model.projectPath, model.projectName = root, name
		header.sync(model, path)
		var labels []string
		for _, segment := range header.segments {
			icon := segment.Children[len(segment.Children)-2]
			if icon.Tag != "img" || icon.Attributes[0].Value != "pencode-path-icon" {
				t.Fatalf("segment missing theme icon: %+v", segment)
			}
			labels = append(labels, segment.Children[len(segment.Children)-1].Text)
		}
		if !slices.Equal(labels, want) || header.fullPath != filepath.Join(root, filepath.FromSlash(path)) {
			t.Fatalf("wrong file path: labels=%v, fullPath=%q", labels, header.fullPath)
		}
	}
	check("/workspace/项目", "项目", "app/logic/源文件.go", []string{"项目", "app", "logic", "源文件.go"})
	if allocs := testing.AllocsPerRun(100, func() { header.sync(model, "app/logic/源文件.go") }); allocs != 0 {
		t.Fatalf("unchanged path allocated %g times", allocs)
	}
	check("/workspace/项目", "项目", "main.go", []string{"项目", "main.go"})
	check("/workspace/other", "other", "main.go", []string{"other", "main.go"})
}

func TestPencodeEditorOpenWithToolbarVisibility(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	step := func(wantHeader, wantTools bool) {
		t.Helper()
		markPencodeRootDirtyByModel(model.fontResources, model)
		if _, err := app.Step(); err != nil {
			t.Fatal(err)
		}
		var headerBounds htmlui.Rect
		for _, item := range []struct {
			id      string
			visible bool
		}{{"editor-header", wantHeader}, {"editor-tools", wantTools}, {"editor-text", wantTools}, {"editor-split", wantTools}} {
			node, ok := model.fontResources.HTMLNode(model.surface, item.id)
			if !ok {
				t.Fatalf("missing %s", item.id)
			}
			bounds, visible := node.Bounds()
			if visible != item.visible {
				t.Fatalf("%s visible=%v, want %v", item.id, visible, item.visible)
			}
			if item.id == "editor-header" {
				headerBounds = bounds
			} else if visible {
				if item.id == "editor-split" {
					if bounds.Y < headerBounds.Y || bounds.Y+bounds.Height > headerBounds.Y+headerBounds.Height {
						t.Fatalf("split button outside header: %+v", bounds)
					}
				} else {
					body := editorContextBounds(t, model, "editor-body")
					if bounds.Y < body.Y || bounds.X+bounds.Width > body.X+body.Width || bounds.Y+bounds.Height > body.Y+body.Height {
						t.Fatalf("toolbar outside code area: %+v", bounds)
					}
				}
			}
		}
	}
	step(true, false)
	model.importEditors([]extensions.Editor{{Action: peb.Action{
		ID: "test/source", Text: "Source preview", Location: peb.OpenWith,
		Selectors: []peb.DocumentSelector{{Match: peb.FileSuffix, Pattern: ".go"}},
	}}})
	step(true, true)
	if model.actions.openWithCount != 1 {
		t.Fatal("missing open-with action")
	}
	editorContextBounds(t, model, "action-test/source")
	model.removeEditors("test/")
	step(true, false)
	project := model.activeProjectState()
	active := project.ActivePath
	project.ActivePath = ""
	model.syncActiveProjectFields()
	step(false, false)
	project.ActivePath = active
	model.syncActiveProjectFields()
	step(true, false)
}
