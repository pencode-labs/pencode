package workbench

import (
	"math"
	"testing"

	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestPencodeExtensionControlAnchor(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	process := &extensions.ActionSession{}
	apply := func(ui peb.UI) {
		t.Helper()
		if err := model.applyExtensionUI("picker", process, ui); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(model.fontResources, model)
		for range 4 {
			languageInteractionStep(t, app)
		}
	}
	apply(peb.UI{ID: "composer", Title: "Composer", Kind: peb.Panel, View: &peb.EditorView{
		HTML: `<button data-ref="model" id="pencode-extension-picker-model" type="button">Select model</button>`,
	}})
	menu := peb.UI{ID: "models", Title: "Models", Kind: peb.ContextMenu, Anchor: "ui:composer#model",
		Placement: peb.UIPlacement{Mode: peb.PositionAnchorAbove, Y: -6, Width: 256, Height: 160},
		View:      &peb.EditorView{HTML: `<button data-ref="default" role="menuitemradio" aria-checked="true" type="button">Default</button>`},
	}
	apply(menu)
	s := model.actions.ui.overlay
	if s.invocation.anchor != "pencode-extension-picker-model" {
		t.Fatal("anchor did not resolve explicit control ID", s.invocation.anchor)
	}
	bounds := editorContextBounds(t, model, s.rootID)
	anchor := editorContextBounds(t, model, s.invocation.anchor)
	if math.Abs(float64(bounds.Y+bounds.Height+6-anchor.Y)) > .2 {
		t.Fatalf("menu does not sit above composer: menu=%+v anchor=%+v", bounds, anchor)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		model.syncExtensionUI(model.fontResources)
		model.updateExtensionUI(model.fontResources, state)
	}); allocs != 0 {
		t.Fatalf("idle anchored menu allocated %g", allocs)
	}
	// A surface cannot resolve controls from another extension.
	if err := model.applyExtensionUI("other", process, menu); err == nil {
		t.Fatal("accepted another extension's anchor")
	}
	apply(peb.UI{ID: "models", Close: true})
	// Near the top edge, the same placement falls below the control instead.
	apply(peb.UI{ID: "chat", Title: "Chat", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<button data-ref="model" type="button">Select model</button>`,
	}})
	menu.Anchor = "ui:chat#model"
	apply(menu)
	s = model.actions.ui.overlay
	bounds = editorContextBounds(t, model, s.rootID)
	anchor = editorContextBounds(t, model, s.invocation.anchor)
	if math.Abs(float64(bounds.Y-anchor.Y-anchor.Height+6)) > .2 {
		t.Fatalf("menu did not fall below top-edge anchor: menu=%+v anchor=%+v", bounds, anchor)
	}
}

func TestPencodeExtensionAnchorTracksScrollAndZoom(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	world := model.fontResources
	apply := func(ui peb.UI) {
		t.Helper()
		if err := model.applyExtensionUI("picker", &extensions.ActionSession{}, ui); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(world, model)
		for range 4 {
			languageInteractionStep(t, app)
		}
	}
	apply(peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<div id="pencode-extension-scroll" style="width:400px;height:400px;overflow:auto"><div style="width:800px;height:800px;padding:240px 0 0 160px"><button data-ref="anchor" type="button">Permissions</button></div></div>`,
	}})
	apply(peb.UI{ID: "menu", Kind: peb.ContextMenu, Anchor: "ui:composer#anchor",
		Placement: peb.UIPlacement{Mode: peb.PositionAnchorAbove, Y: -6, Width: 180, Height: 60},
		View:      &peb.EditorView{HTML: `<button type="button">Allow</button>`},
	})
	for _, zoom := range []int{100, 150, 70} {
		model.settings.Zoom = zoom
		markPencodeRootDirtyByModel(world, model)
		languageInteractionStep(t, app)
		node, _ := world.HTMLNode(model.surface, "pencode-extension-scroll")
		area, ok := node.ScrollContainer()
		if !ok {
			t.Fatal("missing anchor scroll container")
		}
		scroll := world.Mut[scene.UIScroll](area)
		scroll.Position[0], scroll.Position[1] = -80, -120
		for range 4 {
			languageInteractionStep(t, app)
		}
		menu := model.actions.ui.overlay
		control, _ := world.HTMLControl(model.surface, menu.invocation.anchor)
		position := world.Ref[scene.UIGlobalTransform](control)
		bounds := editorContextBounds(t, model, menu.rootID)
		scale := float32(zoom) / 100
		x, y := position.Matrix[12]/scale, position.Matrix[13]/scale
		if math.Abs(float64(bounds.X-x)) > .2 || math.Abs(float64(bounds.Y+bounds.Height+6-y)) > .2 {
			t.Fatalf("zoom %d: menu=%+v, visible anchor=(%g,%g)", zoom, bounds, x, y)
		}
	}
}
