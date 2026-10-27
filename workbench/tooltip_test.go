package workbench

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/actions"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestIconTooltipStaysInsideViewportAtEveryZoom(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	model.app.GetViewport = func() (int, int) { return 2400, 900 }
	ecs.InsertResource(app.World, scene.ViewportSize{Width: 2400, Height: 900, ScaleFactor: 1})
	for _, camera := range ecs.Query[scene.Camera](app.World).All {
		camera.Mut().UpdateViewport(2400, 900)
	}
	a := peb.Action{ID: "tooltip-edge", Text: "Tip", Tooltip: strings.Repeat("说明按钮作用，", 12), Icon: "icons/add.svg", Location: peb.StatusBar}
	if err := model.session.Actions.Register(actions.Definition{Action: a, Run: func(peb.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	for _, zoom := range []int{70, 100, 150} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			model.setZoom(model.fontResources, zoom)
			languageInteractionStep(t, app)
			for _, id := range []string{"settings-button", "action-tooltip-edge"} {
				r := editorContextBounds(t, model, id)
				scale := float64(zoom) / 100
				state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(r.X+r.Width/2) * scale, Y: float64(r.Y+r.Height/2) * scale})
				languageInteractionStep(t, app)
				model.syncTooltip(model.fontResources, model.tooltip.due)
				languageInteractionStep(t, app)
				tip := &model.tooltip
				if !tip.open || !tip.positioned {
					t.Fatalf("%s tooltip was not positioned: %+v", id, tip)
				}
				viewport := editorContextBounds(t, model, "pencode-root")
				size := editorContextBounds(t, model, "pencode-tooltip")
				if tip.x < 8 || tip.y < 8 || tip.x+size.Width > viewport.Width-7 || tip.y+size.Height > viewport.Height-7 {
					t.Fatalf("%s tooltip outside viewport: tip=%+v size=%+v viewport=%+v", id, tip, size, viewport)
				}
				if id == "settings-button" && math.Abs(float64(tip.y-r.Y-r.Height-6)) > 1 {
					t.Fatalf("tooltip lost its anchor: y=%g button=%+v", tip.y, r)
				}
				if id == "action-tooltip-edge" && tip.y+size.Height > r.Y {
					t.Fatalf("bottom tooltip did not flip above its button: tip=%+v button=%+v", tip, r)
				}
			}
		})
	}
}

func TestIconTooltipHoverLifecycle(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	world := model.fontResources
	r := editorContextBounds(t, model, "settings-button")
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(r.X + r.Width/2), Y: float64(r.Y + r.Height/2)})
	languageInteractionStep(t, app)
	if model.tooltip.open || model.tooltip.text != "设置" || model.tooltip.due.IsZero() {
		t.Fatalf("missing delayed tooltip: %+v", model.tooltip)
	}
	model.syncTooltip(world, model.tooltip.due)
	if !model.tooltip.open {
		t.Fatal("stationary hover did not show tooltip")
	}
	languageInteractionStep(t, app)
	node, _ := world.HTMLNode(model.surface, "pencode-tooltip")
	source, _ := node.Serialize()
	if strings.Contains(source, " hidden=") || !strings.Contains(source, ">设置</div>") {
		t.Fatalf("tooltip was not rendered: %s", source)
	}
	// Opening the floating widget must not take hover away from the button.
	languageInteractionStep(t, app)
	if !model.tooltip.open || model.tooltip.id != "settings-button" {
		t.Fatal("tooltip intercepted its anchor's pointer")
	}
	if n := testing.AllocsPerRun(100, func() { model.syncTooltip(world, time.Now()) }); n != 0 {
		t.Fatalf("stationary tooltip allocated %g times", n)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	model.syncTooltip(world, time.Now().Add(time.Second))
	if model.tooltip.open {
		t.Fatal("Escape did not dismiss tooltip")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	model.syncTooltip(world, time.Now().Add(time.Second))
	if model.tooltip.open {
		t.Fatal("dismissed tooltip reopened without leaving the button")
	}
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: 5, Y: 300})
	languageInteractionStep(t, app)
	if model.tooltip.open || model.tooltip.control != 0 {
		t.Fatal("leaving button retained tooltip")
	}
}

func TestIconTooltipWindowControlsExcluded(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	for _, id := range []string{"window-minimize", "window-maximize", "window-close"} {
		r := editorContextBounds(t, model, id)
		state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(r.X + r.Width/2), Y: float64(r.Y + r.Height/2)})
		languageInteractionStep(t, app)
		model.syncTooltip(model.fontResources, time.Now().Add(time.Second))
		if model.tooltip.open || model.tooltip.text != "" {
			t.Fatalf("%s showed tooltip: %+v", id, model.tooltip)
		}
	}
}

func TestIconTooltipExtensionActionUpdates(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	a := peb.Action{ID: "extensions/test/run", Icon: "icons/add.svg", Tooltip: "运行插件任务", Location: peb.TitleBarRight}
	if err := model.session.Actions.Register(actions.Definition{Action: a, Run: func(peb.ActionContext) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	languageInteractionStep(t, app)
	r := editorContextBounds(t, model, "action-"+a.ID)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(r.X + r.Width/2), Y: float64(r.Y + r.Height/2)})
	languageInteractionStep(t, app)
	model.syncTooltip(model.fontResources, model.tooltip.due)
	if !model.tooltip.open || model.tooltip.text != a.Tooltip {
		t.Fatalf("missing extension tooltip: %+v", model.tooltip)
	}
	a.Tooltip = "更新后的插件任务"
	if err := model.session.Actions.Update(a); err != nil {
		t.Fatal(err)
	}
	languageInteractionStep(t, app)
	model.syncTooltip(model.fontResources, model.tooltip.due)
	if !model.tooltip.open || model.tooltip.text != a.Tooltip {
		t.Fatalf("extension tooltip retained old description: %+v", model.tooltip)
	}
	model.session.Actions.Remove(a.ID)
	languageInteractionStep(t, app)
	if model.tooltip.open {
		t.Fatal("removed action retained tooltip")
	}
}

func TestTooltipTemplateAnonymousExtensionButtons(t *testing.T) {
	template := tooltipTemplate(`<button data-ref="send" title="发送 &amp; 保存"><img alt=""></button><button aria-label="刷新"><img alt=""></button><button id="authored">Keep</button>`, "pencode-extension-test/")
	for _, want := range []string{`id="pencode-extension-test/send"`, `id="pencode-extension-test/button-1"`, `id="authored"`, `title="发送 &amp; 保存"`} {
		if !strings.Contains(template.Source, want) {
			t.Fatalf("missing %s in %s", want, template.Source)
		}
	}
	app, model, _, state := languageInteractionFixture(t)
	if err := model.applyExtensionUI("test", &extensions.ActionSession{}, peb.UI{ID: "tips", Title: "Tooltip test", Kind: peb.Panel, View: &peb.EditorView{HTML: `<button data-ref="send" title="发送任务"><img src="icons/add.svg" alt=""></button>`, CSS: `button { width:32px; height:32px; }`}}); err != nil {
		t.Fatal(err)
	}
	s := model.actions.ui.panel
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	// Focusable extension controls receive a host ID during rendering.
	id := s.prefix + "control-send"
	control, found := model.fontResources.HTMLControl(model.surface, id)
	if !found {
		t.Fatal("missing extension button")
	}
	position := module.ContentRef[scene.UIGlobalTransform](model.fontResources, control)
	size := module.ContentRef[scene.Size2D](model.fontResources, control)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(position.Matrix[12] + size.Width/2), Y: float64(position.Matrix[13] + size.Height/2)})
	languageInteractionStep(t, app)
	model.syncTooltip(model.fontResources, model.tooltip.due)
	if !model.tooltip.open || model.tooltip.text != "发送任务" {
		t.Fatalf("missing plugin view tooltip: %+v", model.tooltip)
	}
}
