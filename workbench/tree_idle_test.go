package workbench

import (
	"reflect"
	"testing"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/graphics"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	htmlui "github.com/diiyw/onix/ui/html"
)

func TestPencodeTreeHoverKeepsWorkbenchStable(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	project := model.activeProjectState()
	resourceTestWrite(t, project, "folder/child.txt", "child")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	markPencodeRootDirtyByModel(model.fontResources, model)
	for range 4 {
		languageInteractionStep(t, app)
	}
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	state, _ := app.World.GetResource[*input.State]()
	root, _ := model.fontResources.Get[htmlui.Component[appProps, appState]](model.root)
	editor, _ := model.fontResources.Get[htmlui.Control](model.editor.control)
	for range 3 {
		for _, path := range []string{"folder", "main.go", "folder"} {
			bounds := editorContextBounds(t, model, model.projectTreeRowID(project, path))
			for _, offset := range []float32{40, 60, 80} {
				scale := float64(model.settings.Zoom) / 100
				state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(bounds.X+offset) * scale, Y: float64(bounds.Y+bounds.Height/2) * scale})
				languageInteractionStep(t, app)
				after, _ := model.fontResources.Get[htmlui.Component[appProps, appState]](model.root)
				control, _ := model.fontResources.Get[htmlui.Control](model.editor.control)
				if after.State.Revision != root.State.Revision || control.RenderRevision != editor.RenderRevision {
					t.Fatalf("tree hover refreshed workbench: revision %d -> %d, editor %d -> %d", root.State.Revision, after.State.Revision, editor.RenderRevision, control.RenderRevision)
				}
			}
		}
	}
}

func TestPencodeTreeClickPublishesWithoutMouseMove(t *testing.T) {
	app, session := newPencodeTestApp(t)
	// Capture render extraction without a native window or GPU submission.
	app.Schedule(onix.StageRender).Remove("render_debug_info")
	app.Schedule(onix.StageRender).Remove("render_cameras")
	graphics.BindFrame(app.World, graphics.NewCanvas2D(nil, 1440, 900))
	model := session.active.model
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	project := model.activeProjectState()
	resourceTestWrite(t, project, "folder/child.txt", "child")
	model.startProjectScan(project, false)
	waitPencodeProjectScan(t, project, func() { languageInteractionStep(t, app) })
	project.ExpandedDirs = map[string]bool{"folder": false}
	markPencodeRootDirtyByModel(model.fontResources, model)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	step := func() bool {
		t.Helper()
		rendered, err := app.Step()
		if err != nil {
			t.Fatal(err)
		}
		return rendered
	}
	for range 4 {
		step()
	}
	app.WakeAt(time.Now().Add(5 * time.Second))
	state, _ := app.World.GetResource[*input.State]()
	r := editorContextBounds(t, model, model.projectTreeRowID(project, "folder"))
	scale := float64(model.settings.Zoom) / 100
	x, y := float64(r.X+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
	step()
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	step()
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	if !step() {
		t.Fatal("click did not render")
	}
	if !project.ExpandedDirs["folder"] {
		t.Fatal("click did not expand folder")
	}
	child := ecs.Entity(0)
	for entity, access := range app.World.Query[scene.Text2D]().All {
		if access.Ref().Text().String() == "child.txt" {
			child = entity
			break
		}
	}
	if child == 0 || !scene.IsVisible(app.World, child) {
		t.Fatal("child text was not created and made visible")
	}
	output, _ := app.World.GetResource[*scene.RenderWorld]()
	painted := false
	for _, item := range output.Items2D {
		if reflect.ValueOf(item).FieldByName("entity").Uint() == uint64(child) {
			painted = true
			break
		}
	}
	if !painted {
		t.Fatal("child text was not extracted in click frame")
	}
	if step() {
		t.Fatal("settled tree did not idle")
	}
	editorContextBounds(t, model, model.projectTreeRowID(project, "folder/child.txt"))
}
