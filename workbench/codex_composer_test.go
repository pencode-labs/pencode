package workbench

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexComposerGrowsUpwardBeforeScrolling(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	css, err := os.ReadFile("testdata/codex/chat.css")
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
	if !ok || !w.Focus(control) || initial.Height != 59 {
		t.Fatalf("invalid initial input: %+v", initial)
	}
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "first\nsecond\nthird\n"})
	languageInteractionStep(t, app)
	grown := bounds()
	if grown.Height <= initial.Height || grown.Y >= initial.Y || math.Abs(float64(grown.Y+grown.Height-initial.Y-initial.Height)) > 1 {
		t.Fatalf("input did not grow upward: initial=%+v grown=%+v", initial, grown)
	}
	caretVisual, ok := w.FindPart[engineui.CaretPart, engineui.SurfaceStyle](control)
	if !ok {
		t.Fatal("missing composer caret")
	}
	transform := w.Ref[scene.UIGlobalTransform](caretVisual)
	size := w.Ref[scene.Size2D](caretVisual)
	x, y, right, bottom, ok := scene.BoundsInSpace2D(transform.Matrix, scene.AnchorTopLeft, size.Width, size.Height, true)
	wantCaret := input.TextInputRect{X: x, Y: y, Width: right - x, Height: bottom - y}
	if got := state.TextInputConfig().Caret; !ok || got != wantCaret {
		t.Fatalf("composer resized with stale IME coordinates: got=%+v want=%+v", got, wantCaret)
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

func TestCodexComposerKeepsIMEThroughRunningUpdate(t *testing.T) {
	app, session := newPencodeTestApp(t)
	m := session.active.model
	w := m.fontResources
	process := &extensions.ActionSession{}
	show := func(transcript, activity string) {
		t.Helper()
		if err := m.applyExtensionUI("codex", process, peb.UI{ID: "composer", Kind: peb.EditorTab, Busy: true, View: &peb.EditorView{
			HTML: `<main data-pencode-layout="fill"><div data-ref="transcript" data-pencode-scroll="end" data-pencode-scroll-key="running">` + transcript + `</div><div data-ref="live-changes-region" data-pencode-region>` +
				activity + `</div><textarea id="pencode-extension-codex-prompt" data-ref="prompt" data-pencode-markdown-links data-pencode-autosize style="width:400px;min-height:38px">保留草稿</textarea></main>`,
			Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
		}}); err != nil {
			t.Fatal(err)
		}
		markPencodeRootDirtyByModel(w, m)
	}
	show("", "")
	for range 3 {
		languageInteractionStep(t, app)
	}
	const promptID = "pencode-extension-codex-prompt"
	prompt, ok := w.HTMLControl(m.surface, promptID)
	if !ok || !w.Focus(prompt) {
		t.Fatal("cannot focus composer")
	}
	state, _ := app.World.GetResource[*input.State]()
	state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Text: "ni", Active: true}})
	languageInteractionStep(t, app)
	before := state.TextInputConfig()
	const reply = `<p data-ref="reply" data-pencode-region>新输出</p>`
	var replacementFound bool
	for _, update := range []struct{ transcript, activity string }{
		{reply, `<p id="pencode-extension-running-change" style="height:60px">1 个文件已更改</p>`},
		{reply, ""},
	} {
		show(update.transcript, update.activity)
		mounted := false
		for range 3 {
			languageInteractionStep(t, app)
			if _, ok := w.HTMLNode(m.surface, "pencode-extension-running-change"); ok == (update.activity != "") {
				mounted = true
				break
			}
		}
		if !mounted {
			t.Fatalf("running activity did not update: %q", update.activity)
		}
		current, ok := w.HTMLControl(m.surface, promptID)
		field, _ := w.Get[engineui.TextField](current)
		after := state.TextInputConfig()
		if !ok || w.FocusedControl() != current || !state.Composing() || field.Composition != "ni" ||
			!after.Active || after.Revision != before.Revision || after.Caret.Height <= 0 || after.Caret.Y == before.Caret.Y {
			t.Fatalf("running update interrupted the IME: before=%+v after=%+v field=%+v", before, after, field)
		}
		replacementFound = replacementFound || prompt != current
		before = after
		prompt = current
	}
	if !replacementFound {
		t.Fatal("view update did not exercise native control replacement")
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "你"})
	languageInteractionStep(t, app)
	if field, _ := w.Get[engineui.TextField](prompt); field.Value != "保留草稿你" {
		t.Fatalf("IME commit lost the draft or composition: %q", field.Value)
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
	css, err := os.ReadFile("testdata/codex/chat.css")
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
		if caret := state.TextInputConfig().Caret; caret.X <= 0 || caret.Y <= 0 || caret.Height <= 0 {
			t.Fatalf("pointer %v left the IME without a screen anchor: %+v", point, caret)
		}
		state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "输入"})
		languageInteractionStep(t, app)
		field = w.Mut[engineui.TextField](control)
		if field.Text().String() != "输入" {
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
