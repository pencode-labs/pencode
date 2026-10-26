package workbench

import (
	"testing"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeExtensionOutputAfterSceneSwitch(t *testing.T) {
	app, session := newPencodeTestApp(t)
	session.active.model.app.GetViewport = func() (int, int) { return 1440, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	session.active.model.screen = pencodeLauncher
	if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
		t.Fatal(err)
	}
	if err := app.Modules().Dispatch(); err != nil {
		t.Fatal(err)
	}
	model := session.active.model
	if err := model.applyExtensionUI("go", &extensions.ActionSession{}, peb.UI{
		ID: "tools", Title: "Go：工具安装失败", Kind: peb.Notification,
		View: &peb.EditorView{HTML: `<button data-ref="logs" data-pencode-action="show-output" type="button">查看日志</button>`},
	}); err != nil {
		t.Fatal(err)
	}
	model.logOutput("Extension: go", "install dlv: exit status 1")
	notice := model.actions.ui.surfaces[0]
	session.active.model.screen = pencodeWorkspace
	if err := app.Modules().UpdateFrame(1.0 / 60); err != nil {
		t.Fatal(err)
	}
	if err := app.Modules().Dispatch(); err != nil {
		t.Fatal(err)
	}
	model = session.active.model
	model.bottom.active = pencodePanelClosed
	markPencodeRootDirtyByModel(model.fontResources, model)
	for range 3 {
		languageInteractionStep(t, app)
	}
	state, ok := ecs.GetResource[*input.State](app.World)
	if !ok {
		t.Fatal("missing input state")
	}
	r := editorContextBounds(t, model, notice.focus[0].id)
	x, y := float64(r.X+r.Width/2), float64(r.Y+r.Height/2)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	for range 3 {
		languageInteractionStep(t, app)
	}
	if model.bottom.active != pencodePanelOutput || model.bottom.source != model.outputStore().Channel("Extension: go") {
		t.Fatalf("view logs did not open output in the active workspace after a scene switch: active=%d source=%v current=%p owner=%p live=%v focus=%d bounds=%+v", model.bottom.active, model.bottom.source, model, notice.model, notice.live(), notice.focused, r)
	}
	editorContextBounds(t, model, "output-pane")
}
