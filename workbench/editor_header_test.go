package workbench

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/onix-engine/onix/scene"
	htmlui "github.com/onix-engine/onix/ui/html"
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
		panel := editorContextBounds(t, model, "editor-panel")
		tabs := editorContextBounds(t, model, "tabs")
		body := editorContextBounds(t, model, "editor-body")
		if tabs.X != body.X || tabs.Width != body.Width ||
			tabs.Y != panel.Y+1 || body.Y+body.Height != panel.Y+panel.Height-1 {
			t.Fatalf("tabs and body do not share the editor panel's edges: panel=%+v tabs=%+v body=%+v", panel, tabs, body)
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
					if bounds.Y < body.Y || bounds.X+bounds.Width > body.X+body.Width || bounds.Y+bounds.Height > body.Y+body.Height {
						t.Fatalf("toolbar outside code area: %+v", bounds)
					}
				}
			}
		}
		contentTop := tabs.Y + tabs.Height
		if wantHeader {
			if headerBounds.Y != contentTop || headerBounds.X != tabs.X || headerBounds.Width != tabs.Width {
				t.Fatalf("breadcrumbs do not meet the tab strip: tabs=%+v header=%+v", tabs, headerBounds)
			}
			contentTop += headerBounds.Height
		}
		if body.Y != contentTop {
			t.Fatalf("gap above editor body: got y=%g, want %g", body.Y, contentTop)
		}
	}
	step(true, false)
	editors := installPencodeEditorFixture(t, session.backend).Editors
	model.importEditors(editors)
	step(true, true)
	if model.actions.openWithCount != 1 {
		t.Fatal("missing open-with action")
	}
	editorContextBounds(t, model, "action-"+editors[0].ID)
	model.removeEditors(editors[0].ID)
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
