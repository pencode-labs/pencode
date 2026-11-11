package workbench

import (
	"runtime"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
)

func TestFindKeepsEditorClipboardAndShortcuts(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	w := m.fontResources
	const original = "甲乙🙂尾\n"
	m.updateActiveContent(original)
	m.setEditorValueAndCaret(w, original, 3)
	w.Mut[engineui.TextField](m.editor.control).Anchor = 1
	m.openFind(true)
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
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
		if !m.findOpen {
			t.Fatal("editor shortcut closed find")
		}
	}
	w.Focus(m.editor.control)
	w.Mut[engineui.TextField](m.editor.control).Caret = 3
	w.Mut[engineui.TextField](m.editor.control).Anchor = 1
	chord(primary, input.KeyC)
	if clipboard != "乙🙂" || m.activeContent() != original {
		t.Fatalf("copy: clipboard=%q content=%q", clipboard, m.activeContent())
	}
	chord(primary, input.KeyX)
	if clipboard != "乙🙂" || m.activeContent() != "甲尾\n" {
		t.Fatalf("cut: clipboard=%q content=%q", clipboard, m.activeContent())
	}
	chord(primary, input.KeyZ)
	if m.activeContent() != original {
		t.Fatal("undo did not restore the cut selection")
	}
	w.Mut[engineui.TextField](m.editor.control).Caret = 3
	w.Mut[engineui.TextField](m.editor.control).Anchor = 1
	clipboard = "中\r\n🙂"
	chord(primary, input.KeyV)
	if m.activeContent() != "甲中\n🙂尾\n" {
		t.Fatal("paste did not replace the Unicode selection", m.activeContent())
	}
	chord(primary, input.KeyZ)
	if runtime.GOOS == "darwin" {
		chord(primary, input.KeyLeftShift, input.KeyZ)
	} else {
		chord(primary, input.KeyY)
	}
	if m.activeContent() != "甲中\n🙂尾\n" {
		t.Fatal("redo did not restore paste", m.activeContent())
	}
	chord(primary, input.KeyA)
	field := w.Ref[engineui.TextField](m.editor.control)
	if field.Caret != 6 || field.Anchor != 0 {
		t.Fatalf("select all: %+v", field)
	}
	// A custom editor binding takes precedence over search options in the code.
	m.settings.Bindings["editor-copy"] = pencodeBindingChord("Alt+C")
	options := m.findOptions
	chord(input.KeyLeftAlt, input.KeyC)
	if clipboard != m.activeContent() || m.findOptions != options {
		t.Fatal("search options intercepted the editor binding")
	}
	for _, ref := range []string{"find-query", "replace-query"} {
		control, _ := w.HTMLControl(m.surface, ref)
		w.Focus(control)
		clipboard = "query"
		before := m.activeContent()
		chord(primary, input.KeyA)
		chord(primary, input.KeyV)
		if m.activeContent() != before || w.Ref[engineui.TextField](control).Value != "query" {
			t.Fatal("input clipboard shortcut affected the editor", ref)
		}
	}
	if allocations := testing.AllocsPerRun(100, func() { m.editorClipboardShortcut(w, state, runtime.GOOS) }); allocations != 0 {
		t.Fatal("idle clipboard shortcut allocated", allocations)
	}
}

func TestFindKeepsEditorContextAndLanguageShortcuts(t *testing.T) {
	app, m, provider, state := languageInteractionFixture(t)
	w := m.fontResources
	const content = "package demo\nvar value = 1\n"
	const anchor, caret = 17, 22
	m.updateActiveContent(content)
	m.setEditorValueAndCaret(w, content, caret)
	w.Mut[engineui.TextField](m.editor.control).Anchor = anchor
	m.syncLanguageDocuments()
	m.openFind(false)
	markPencodeRootDirtyByModel(w, m)
	for range 3 {
		languageInteractionStep(t, app)
	}
	clipboard := ""
	state.SetClipboard(func(value string) bool { clipboard = value; return true }, func() (string, bool) { return clipboard, true })
	toggle := editorContextBounds(t, m, "find-toggle-replace")
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: float64(toggle.X + toggle.Width/2), Y: float64(toggle.Y + toggle.Height/2)})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: float64(toggle.X + toggle.Width/2), Y: float64(toggle.Y + toggle.Height/2)})
	languageInteractionStep(t, app)
	if m.languageUI.contextOpen {
		t.Fatal("right click on the find bar opened the code menu beneath it")
	}
	editor, _ := w.HTMLNode(m.surface, "editor")
	rect, _ := editor.Bounds()
	text, ox, oy := m.languageEditorGeometry(w, rect)
	x, y, _, height, _ := scene.TextCaretRect2D(text, content, anchor+1)
	rightClick := func() {
		t.Helper()
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: float64(ox + x), Y: float64(oy + y + height/2)})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: float64(ox + x), Y: float64(oy + y + height/2)})
		languageInteractionStep(t, app)
		if !m.languageUI.contextOpen || m.languageUI.contextAnchor != anchor || m.languageUI.contextCaret != caret {
			t.Fatalf("right click lost the code selection: %+v", m.languageUI)
		}
	}
	key := func(code input.KeyCode) {
		t.Helper()
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: code})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: code})
		languageInteractionStep(t, app)
	}
	// Right-clicking the code must work directly from the focused search input.
	rightClick()
	editorContextClick(t, app, m, state, "editor-context-copy")
	if clipboard != "value" || !m.findOpen || m.activeContent() != content {
		t.Fatal("context copy changed find or editor content", clipboard)
	}
	rightClick()
	languageInteractionStep(t, app)
	if allocations := testing.AllocsPerRun(100, func() { m.updateLanguageInteractions(w, state) }); allocations != 0 {
		t.Fatal("idle context menu with find open allocated", allocations)
	}
	key(input.KeyEscape)
	if m.languageUI.contextOpen || !m.findOpen {
		t.Fatal("Escape must dismiss the menu before the find bar")
	}
	rightClick()
	key(input.KeyDown)
	key(input.KeyEnter)
	if m.languageUI.contextOpen || !m.findOpen || clipboard != "value" || m.activeContent() != content {
		t.Fatal("keyboard context copy did not preserve find and content")
	}
	key(input.KeyF12)
	languageInteractionJob(t, provider, "textDocument/definition")
	if !m.findOpen {
		t.Fatal("language shortcut closed find")
	}
	rightClick()
	clipboard = "other"
	editorContextClick(t, app, m, state, "editor-context-paste")
	if m.activeContent() != "package demo\nvar other = 1\n" || !m.findOpen {
		t.Fatal("context paste did not replace the code selection", m.activeContent())
	}
}
