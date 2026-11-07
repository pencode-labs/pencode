package workbench

import (
	"runtime"
	"testing"

	"github.com/onix-engine/onix/input"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func TestCodexComposerEditingShortcuts(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
		HTML: `<main><textarea data-ref="prompt" style="width:400px;height:100px">甲乙🙂
hello world</textarea></main>`,
		Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
	}}); err != nil {
		t.Fatal(err)
	}
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	control, ok := w.HTMLControl(m.surface, m.actions.ui.editor.prefix+"control-prompt")
	if !ok || !w.Focus(control) {
		t.Fatal("cannot focus composer")
	}
	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	chord := func(keys ...input.KeyCode) {
		t.Helper()
		for _, key := range keys {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		}
		languageInteractionStep(t, app)
		for _, key := range keys {
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		}
		languageInteractionStep(t, app)
	}
	check := func(value string, caret, anchor int) {
		t.Helper()
		field := w.Ref[engineui.TextField](control)
		native, _ := w.Get[htmlui.Control](control)
		if field.Value != value || native.Value != value || field.Caret != caret || field.Anchor != anchor || w.FocusedControl() != control {
			t.Fatalf("input mismatch: field=%+v native=%q, want %q %d:%d", field, native.Value, value, caret, anchor)
		}
	}
	const original = "甲乙🙂\nhello world"
	chord(primary, input.KeyA)
	check(original, 15, 0)
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "新草稿🙂"})
	languageInteractionStep(t, app)
	check("新草稿🙂", 4, 4)
	chord(primary, input.KeyZ)
	check(original, 15, 0)
	chord(primary, input.KeyLeftShift, input.KeyZ)
	check("新草稿🙂", 4, 4)
	chord(input.KeyHome)
	check("新草稿🙂", 0, 0)
	chord(input.KeyLeftShift, input.KeyEnd)
	check("新草稿🙂", 4, 0)
	chord(primary, input.KeyZ)
	chord(input.KeyLeftControl, input.KeyHome)
	check(original, 0, 0)
	chord(input.KeyEnd)
	check(original, 3, 3)
	chord(input.KeyLeftControl, input.KeyEnd)
	check(original, 15, 15)
	chord(input.KeyHome)
	check(original, 4, 4)
	chord(input.KeyDelete)
	check("甲乙🙂\nello world", 4, 4)
	chord(primary, input.KeyZ)
	check(original, 4, 4)
	if allocs := testing.AllocsPerRun(100, func() { m.syncExtensionUI(w) }); allocs != 0 {
		t.Fatalf("idle composer allocated %g", allocs)
	}
}

func TestInputUndoDoesNotChangeBackgroundFile(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	m.updateActiveContent("pending file edit")
	m.findOpen, m.findQuery = true, "甲乙🙂尾"
	markPencodeRootDirtyByModel(m.fontResources, m)
	languageInteractionStep(t, app)
	openInputContext(t, app, m, state, "find-query", 3, 1)
	m.runInputContextEdit(app.World, "input-context-delete")
	for range 3 {
		languageInteractionStep(t, app)
	}
	if m.findQuery != "甲尾" {
		t.Fatal("context delete did not edit query", m.findQuery)
	}
	primary := input.KeyLeftControl
	if runtime.GOOS == "darwin" {
		primary = input.KeyLeftSuper
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: primary})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyZ})
	languageInteractionStep(t, app)
	if m.findQuery != "甲乙🙂尾" || m.activeContent() != "pending file edit" {
		t.Fatalf("undo routed to wrong document: query=%q file=%q", m.findQuery, m.activeContent())
	}
}

func TestCodexComposerPasteAfterModifierRelease(t *testing.T) {
	for _, test := range []struct {
		name    string
		primary input.KeyCode
	}{{"left ctrl", input.KeyLeftControl}, {"right ctrl", input.KeyRightControl}, {"left cmd", input.KeyLeftSuper}, {"right cmd", input.KeyRightSuper}} {
		t.Run(test.name, func(t *testing.T) {
			app, m, _, state := languageInteractionFixture(t)
			w := m.fontResources
			if err := m.applyExtensionUI("codex", &extensions.ActionSession{}, peb.UI{ID: "composer", Kind: peb.EditorTab, View: &peb.EditorView{
				HTML:   `<textarea data-ref="prompt" data-pencode-markdown-links data-pencode-paste-images data-pencode-submit-on-enter="form" style="width:400px;height:100px">甲乙🙂尾</textarea>`,
				Events: []peb.EditorBinding{{Ref: "prompt", Kind: "input"}},
			}}); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				languageInteractionStep(t, app)
			}
			control, ok := w.HTMLControl(m.surface, m.actions.ui.editor.prefix+"control-prompt")
			if !ok || !w.Focus(control) {
				t.Fatal("cannot focus composer")
			}
			field := w.Mut[engineui.TextField](control)
			field.Anchor, field.Caret = 1, 3
			state.SetClipboard(nil, func() (string, bool) { return "中\r\ncode()", true })
			// A quick chord, or a busy frame, can queue both releases before UI editing.
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: test.primary})
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyV})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyV})
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: test.primary})
			languageInteractionStep(t, app)
			field = w.Mut[engineui.TextField](control)
			native, _ := w.Get[htmlui.Control](control)
			if field.Value != "甲中\ncode()尾" || native.Value != field.Value || field.Caret != 9 || field.Anchor != 9 || w.FocusedControl() != control {
				t.Fatalf("quick paste lost text or selection: field=%+v native=%q", field, native.Value)
			}
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: test.primary})
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyZ})
			languageInteractionStep(t, app)
			field = w.Mut[engineui.TextField](control)
			if field.Value != "甲乙🙂尾" || field.Anchor != 1 || field.Caret != 3 {
				t.Fatalf("paste lost undo history: %+v", field)
			}
		})
	}
}
