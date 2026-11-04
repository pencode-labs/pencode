package workbench

import (
	"runtime"
	"testing"

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
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
