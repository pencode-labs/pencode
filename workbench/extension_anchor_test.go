package workbench

import (
	"math"
	"testing"

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
