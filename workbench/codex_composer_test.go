package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexComposerGrowsUpwardBeforeScrolling(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	width := 1024
	m.app.GetViewport = func() (int, int) { return width, 768 }
	app.World.InsertResource(scene.ViewportSize{Width: 1024, Height: 768, ScaleFactor: 1})
	show := func(draft string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"></div><section class="codex-composer"><form class="codex-input-card"><textarea data-ref="prompt" data-pencode-autosize class="codex-prompt">` + draft + `</textarea><div id="pencode-extension-composer-footer" class="codex-input-footer"><button type="button">发送</button></div></form></section></section></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show("")
	id := m.actions.ui.editor.prefix + "control-prompt"
	bounds := func() scene.TextRangeRect2D {
		b := editorContextBounds(t, m, id)
		return scene.TextRangeRect2D{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}
	}
	initial := bounds()
	footer := editorContextBounds(t, m, "pencode-extension-composer-footer")
	control, ok := w.HTMLControl(m.surface, id)
	if !ok || !w.Focus(control) || initial.Height != 38 {
		t.Fatalf("invalid initial input: %+v", initial)
	}
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "first\nsecond\nthird\n"})
	languageInteractionStep(t, app)
	grown := bounds()
	if grown.Height <= initial.Height || grown.Y >= initial.Y || math.Abs(float64(grown.Y+grown.Height-initial.Y-initial.Height)) > 1 {
		t.Fatalf("input did not grow upward: initial=%+v grown=%+v", initial, grown)
	}
	if current := editorContextBounds(t, m, "pencode-extension-composer-footer"); current != footer {
		t.Fatalf("growing input moved the footer: before=%+v after=%+v", footer, current)
	}
	textarea := w.Mut[engineui.Textarea](control)
	if textarea.ScrollY != 0 {
		t.Fatalf("input scrolled before reaching its limit: %v", textarea.ScrollY)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: strings.Repeat("more\n", 30)})
	languageInteractionStep(t, app)
	if b := bounds(); b.Height != 240 || w.Mut[engineui.Textarea](control).ScrollY <= 0 {
		t.Fatalf("long input did not scroll at its height limit: bounds=%+v scroll=%v", b, w.Mut[engineui.Textarea](control).ScrollY)
	}
	w.SetControlValue(control, "short")
	languageInteractionStep(t, app)
	if b := bounds(); b != initial || w.Mut[engineui.Textarea](control).ScrollY != 0 {
		t.Fatalf("shortened input did not shrink and reset scroll: %+v", b)
	}
	show(strings.Repeat("wide text ", 18))
	wide := bounds()
	width = 640
	app.World.InsertResource(scene.ViewportSize{Width: 640, Height: 768, ScaleFactor: 1})
	languageInteractionStep(t, app)
	if narrow := bounds(); narrow.Height <= wide.Height {
		t.Fatalf("soft wrapping did not resize the input: wide=%+v narrow=%+v", wide, narrow)
	}
	width = 1024
	app.World.InsertResource(scene.ViewportSize{Width: 1024, Height: 768, ScaleFactor: 1})
	languageInteractionStep(t, app)
	if b := bounds(); b.Height != wide.Height {
		t.Fatalf("widening the input did not shrink it: wide=%+v current=%+v", wide, b)
	}
	if allocs := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocs != 0 {
		t.Fatalf("unchanged autosizing input allocated %g", allocs)
	}
	show("")
	if b := bounds(); b.Height != initial.Height {
		t.Fatalf("clearing the draft did not reset input height: %+v", b)
	}
}

func TestCodexComposerPointerFocusAcceptsText(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	m.app.GetViewport = func() (int, int) { return 1024, 768 }
	app.World.InsertResource(scene.ViewportSize{Width: 1024, Height: 768, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1024, 768)
	}
	css, err := os.ReadFile("../extensions/codex/resources/chat.css")
	if err != nil {
		t.Fatal(err)
	}
	show := func(draft string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
			HTML: `<main class="codex-workspace" data-pencode-layout="fill"><section class="codex-chat-pane"><div class="codex-transcript"></div><section class="codex-composer"><form id="pencode-extension-test-card" data-ref="form" data-pencode-focus="prompt" class="codex-input-card"><textarea data-ref="prompt" data-pencode-autosize class="codex-prompt" placeholder="和 Codex 一起工作">` + draft + `</textarea><div class="codex-input-footer"><button data-ref="settings" type="button">设置</button></div></form></section></section></main>`,
			CSS:  string(css), Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
		for range 3 {
			languageInteractionStep(t, app)
		}
	}
	show("")
	state, _ := app.World.GetResource[*input.State]()
	id := m.actions.ui.editor.prefix + "control-prompt"
	settingsID := m.actions.ui.editor.prefix + "control-settings"
	for _, point := range []struct {
		id   string
		x, y float32
	}{{id, .02, .2}, {id, .5, .5}, {id, .98, .8}, {"pencode-extension-test-card", .5, .04}, {"pencode-extension-test-card", .005, .5}, {"pencode-extension-test-card", .5, .96}} {
		editorContextClick(t, app, m, state, settingsID)
		if button, ok := w.HTMLControl(m.surface, settingsID); !ok || w.FocusedControl() != button {
			t.Fatal("card stole focus from its settings button")
		}
		b := editorContextBounds(t, m, point.id)
		scale := float64(m.settings.Zoom) / 100
		x, y := float64(b.X+b.Width*point.x)*scale, float64(b.Y+b.Height*point.y)*scale
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
		languageInteractionStep(t, app)
		control, ok := w.HTMLControl(m.surface, id)
		field := w.Mut[engineui.TextField](control)
		if !ok || field == nil || w.FocusedControl() != control || !field.Focused || !state.TextInputConfig().Active {
			t.Fatalf("pointer %v did not focus the prompt: control=%v focused=%v field=%+v", point, control, w.FocusedControl(), field)
		}
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "输入"})
		languageInteractionStep(t, app)
		field = w.Mut[engineui.TextField](control)
		if field.Value != "输入" {
			t.Fatalf("pointer %v focused the prompt without accepting text: %+v", point, field)
		}
		w.SetControlValue(control, "")
		show("")
	}
	control, _ := w.HTMLControl(m.surface, id)
	w.SetControlValue(control, strings.Repeat("select this text ", 20))
	languageInteractionStep(t, app)
	b := editorContextBounds(t, m, id)
	scale := float64(m.settings.Zoom) / 100
	x, y := float64(b.X+5)*scale, float64(b.Y+10)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x + 80*scale, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x + 80*scale, Y: y})
	languageInteractionStep(t, app)
	field := w.Mut[engineui.TextField](control)
	if field.Anchor == field.Caret || w.FocusedControl() != control {
		t.Fatalf("card prevented text selection: %+v", field)
	}
	if allocs := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocs != 0 {
		t.Fatalf("unchanged focus delegation allocated %g", allocs)
	}
}
