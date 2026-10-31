package workbench

import (
	"testing"

	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestWireRegionPatchRetainsNativeDraftAndSelection(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	var receiver peb.ViewReceiver
	process := &extensions.ActionSession{}
	show := func(ui peb.UI) {
		t.Helper()
		data, err := peb.EncodeUI(nil, ui)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := peb.DecodeUI(data)
		if err != nil {
			t.Fatal(err)
		}
		full, err := receiver.Apply(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.applyExtensionUI("chat", process, full); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show(peb.UI{ID: "chat", Title: "Chat", Kind: peb.EditorTab, ViewVersion: 1, View: streamingView(30, "first")})
	surface := m.actions.ui.editor
	definition, history := surface.definition, surface.regions[0].definition
	promptID := surface.prefix + "control-prompt"
	prompt, ok := w.HTMLControl(m.surface, promptID)
	if !ok || !w.Focus(prompt) {
		t.Fatal("missing prompt")
	}
	w.SetControlValue(prompt, "draft being typed")
	field := w.Mut[engineui.TextField](prompt)
	field.Anchor, field.Caret = 2, 7
	show(peb.UI{ID: "chat", Title: "Chat", Kind: peb.EditorTab, ViewVersion: 2, Patch: &peb.ViewPatch{Base: 1, Regions: []peb.ViewRegion{{Ref: "stream", HTML: "<p>second中😀</p>"}}}})
	if next, ok := w.HTMLControl(m.surface, promptID); !ok || next != prompt || w.FocusedControl() != prompt {
		t.Fatal("patch replaced or unfocused input")
	}
	field = w.Mut[engineui.TextField](prompt)
	if field.Text().String() != "draft being typed" || field.Anchor != 2 || field.Caret != 7 || surface.definition != definition || surface.regions[0].definition != history {
		t.Fatal("patch lost retained content or local input")
	}
}
