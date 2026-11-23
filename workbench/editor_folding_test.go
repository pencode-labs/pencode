package workbench

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
)

func clickEditorFold(t *testing.T, app *onix.App, m *workspaceModel, state *input.State, row int, sameFrame bool) {
	t.Helper()
	b := editorContextBounds(t, m, "editor-gutter")
	text := m.fontResources.Ref[scene.Text2D](m.editor.text)
	transform, _ := m.fontResources.Get[scene.UITransform](m.editor.text)
	zoom := float64(m.settings.Zoom) / 100
	x := float64(b.X+b.Width-foldGutterWidth+foldIconSize/2) * zoom
	y := float64(b.Y+transform.Translation.Y.Value+(float32(row)+.5)*text.LineHeight) * zoom
	for _, kind := range []input.EventKind{input.EventMouseMove, input.EventMouseDown, input.EventMouseUp} {
		state.Enqueue(input.Event{Kind: kind, Button: input.MouseLeft, X: x, Y: y})
		if !sameFrame || kind != input.EventMouseDown {
			languageInteractionStep(t, app)
		}
	}
}

func TestEditorFoldIndentAndEdit(t *testing.T) {
	const source = "class Example\r\n{\r\n\tfunction run()\r\n\t{\r\n\t\tif (ready) {\r\n\t\t\t工作();\r\n\t\t}\r\n\t}\r\n}\r\n"
	file := &openFile{contentSnapshot: snapshotText(source)}
	buffer := file.snapshotBuffer()
	var result indentFoldResult
	computeIndentFolds(indentFoldJob{file, file.snapshot()}, &result)
	want := []editorFoldRange{{0, 7}, {2, 6}, {4, 5}}
	if !slices.Equal(result.ranges, want) {
		t.Fatalf("ranges %v", result.ranges)
	}
	folds := file.foldState()
	folds.setRanges(result.ranges, buffer)
	folds.toggle(4, buffer)
	folds.toggle(0, buffer)
	folds.toggle(0, buffer)
	_, inside := folds.hiddenAt(buffer.LineStart(5))
	_, header := folds.hiddenAt(buffer.LineStart(4))
	if !inside || header {
		t.Fatal("lost nested fold")
	}
	next := file.snapshot().Replace(0, 0, "// 中文\n")
	buffer.SyncSnapshot(next)
	folds.edit(next, 0, 0, buffer)
	_, inside = folds.hiddenAt(buffer.LineStart(6))
	if !slices.Equal(folds.collapsed, []editorFoldRange{{5, 6}}) || !inside {
		t.Fatalf("shifted folds %v", folds.collapsed)
	}
	if n := testing.AllocsPerRun(100, func() { folds.toggle(5, buffer); folds.toggle(5, buffer) }); n != 0 {
		t.Fatalf("warm toggle allocs %g", n)
	}
}

func TestEditorFoldLSPGutterAndNavigation(t *testing.T) {
	app, m, provider, state := languageInteractionFixture(t)
	world := m.fontResources
	const source = "class C {\n  function f() {\n    if (x) {\n      中文();\n    }\n  }\n}\nafter"
	m.updateActiveContent(source)
	m.setEditorValueAndCaret(world, source, 0)
	m.highlightPending = false
	provider.client.Capabilities["foldingRangeProvider"] = json.RawMessage("true")
	m.pollLanguageServers(world)
	job := languageInteractionJob(t, provider, foldingMethod)
	deliver := func(result string) {
		m.languages.events <- pencodeLanguageEvent{session: provider, method: "result", job: job, result: json.RawMessage(result)}
		m.pollLanguageServers(world)
	}
	deliver(`[{"startLine":2,"endLine":4},{"startLine":0,"endLine":6},{"startLine":1,"endLine":5},{"startLine":2,"endLine":7},{"startLine":0,"endLine":1},{"startLine":3,"endLine":99}]`)
	languageInteractionStep(t, app)
	file := m.activeFile()
	if !slices.Equal(file.folds.ranges, []editorFoldRange{{0, 6}, {1, 5}, {2, 4}}) {
		t.Fatalf("LSP ranges %v", file.folds.ranges)
	}
	before := file.snapshot()
	field := world.Mut[engineui.TextField](m.editor.control)
	field.Caret, field.Anchor = before.RuneOffset(before.LineStart(3))+6, 2
	clickEditorFold(t, app, m, state, 2, true)
	wantCaret := before.RuneOffset(before.LineStart(3)) - 1
	if field.Caret != wantCaret || field.Anchor != 2 {
		t.Fatalf("inner fold selection (%d, %d), want (%d, 2)", field.Caret, field.Anchor, wantCaret)
	}
	if config := state.TextInputConfig(); !config.Active || config.SelectionStart != 2 || config.SelectionEnd != wantCaret {
		t.Fatalf("native fold selection (%d, %d), want (2, %d), active=%v", config.SelectionStart, config.SelectionEnd, wantCaret, config.Active)
	}
	clickEditorFold(t, app, m, state, 0, false)
	wantCaret = before.RuneOffset(before.LineStart(1)) - 1
	if field.Caret != wantCaret || field.Anchor != 2 {
		t.Fatalf("outer fold selection (%d, %d), want (%d, 2)", field.Caret, field.Anchor, wantCaret)
	}
	if got := m.editor.sourceLines; !slices.Equal(got, []int{1, 8}) {
		t.Fatalf("outer fold gutter %v, collapsed %v", got, file.folds.collapsed)
	}
	if len(m.editor.foldIcons) == 0 {
		t.Fatal("no folding icon was painted")
	}
	gutter := editorContextBounds(t, m, "editor-gutter")
	icon := m.editor.foldIcons[0]
	transform, _ := world.Get[scene.UITransform](icon)
	sprite, _ := world.Get[scene.Sprite2D](icon)
	if gap := gutter.Width - transform.Translation.X.Value - sprite.Width; gap < 6 {
		t.Fatalf("fold icon right padding %g, want at least 6px", gap)
	}
	clickEditorFold(t, app, m, state, 0, false)
	if got := m.editor.sourceLines; !slices.Equal(got, []int{1, 2, 3, 6, 7, 8}) {
		t.Fatalf("inner fold gutter %v", got)
	}
	if field.Caret != wantCaret || field.Anchor != 2 {
		t.Fatalf("unfold selection (%d, %d), want (%d, 2)", field.Caret, field.Anchor, wantCaret)
	}
	header := before.RuneOffset(before.LineStart(2)) + 2
	field.Caret, field.Anchor = header, header
	for _, key := range []input.KeyCode{input.KeyDown, input.KeyUp} {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		languageInteractionStep(t, app)
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
		expected := header
		if key == input.KeyDown {
			expected = before.RuneOffset(before.LineStart(5)) + 2
		}
		if field.Caret != expected || len(file.folds.collapsed) != 1 {
			t.Fatalf("key %v landed at %d, expected %d; collapsed %v", key, field.Caret, expected, file.folds.collapsed)
		}
	}
	if file.snapshot() != before {
		t.Fatal("fold changed native document")
	}
	text := world.Ref[scene.Text2D](m.editor.text)
	after := before.RuneOffset(before.LineStart(7))
	x, y, _, _, ok := scene.TextCaretRect2D(text, before, after)
	if !ok || y != 5*text.LineHeight || scene.TextCaretIndex2D(text, before, x, y+1) != after {
		t.Fatalf("caret mapping %v %v", x, y)
	}
	inside := before.RuneOffset(before.LineStart(3))
	field = world.Mut[engineui.TextField](m.editor.control)
	field.Caret, field.Anchor = inside, inside
	m.revealEditorCaret(world, inside)
	languageInteractionStep(t, app)
	if len(file.folds.collapsed) != 0 || scene.TextLineCount2D(text) != 8 {
		t.Fatal("target inside hidden region was not revealed")
	}
	m.updateActiveContent("new\ncontent")
	deliver(`[{"startLine":0,"endLine":7}]`)
	if len(file.folds.ranges) != 0 {
		t.Fatal("obsolete response installed")
	}
}

func TestEditorFoldKeepsViewport(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	world := m.fontResources
	source := strings.Repeat("line\n", 40) + "if (ready) {\n    work();\n}\n" + strings.Repeat("line\n", 160)
	m.updateActiveContent(source)
	m.setEditorValueAndCaret(world, source, len(source))
	file := m.activeFile()
	file.foldState().setRanges([]editorFoldRange{{40, 42}}, file.snapshotBuffer())
	languageInteractionStep(t, app)
	text := world.Ref[scene.Text2D](m.editor.text)
	scroll := 35 * text.LineHeight
	area := world.Mut[engineui.Textarea](m.editor.control)
	area.RestoreScroll(0, scroll)
	languageInteractionStep(t, app)
	for _, wantCollapsed := range []int{1, 0} {
		clickEditorFold(t, app, m, state, 40, wantCollapsed == 0)
		languageInteractionStep(t, app)
		field := world.Ref[engineui.TextField](m.editor.control)
		if len(file.folds.collapsed) != wantCollapsed || !field.Focused || field.Caret != len(source) || area.ScrollY != scroll {
			t.Fatalf("folded %v, focus %v, caret %d, scroll %v, want %v", file.folds.collapsed, field.Focused, field.Caret, area.ScrollY, scroll)
		}
	}
}

func TestEditorFoldFallbackWithoutProvider(t *testing.T) {
	app, m, _, _ := languageInteractionFixture(t)
	const source = "<?php\nclass Example\n{\n    function run()\n    {\n        if ($ready) {\n            return true;\n        }\n    }\n}\n"
	m.updateActiveContent(source)
	m.setEditorValueAndCaret(m.fontResources, source, 0)
	file := m.activeFile()
	deadline := time.Now().Add(3 * time.Second)
	for file.folds == nil || !file.folds.ready {
		if time.Now().After(deadline) {
			t.Fatal("indentation worker did not publish folds")
		}
		languageInteractionStep(t, app)
	}
	if !slices.Equal(file.folds.ranges, []editorFoldRange{{1, 8}, {3, 7}, {5, 6}}) {
		t.Fatalf("PHP folds %v", file.folds.ranges)
	}
	m.toggleEditorFold(m.fontResources, file, 1)
	languageInteractionStep(t, app)
	text := m.fontResources.Mut[scene.Text2D](m.editor.text)
	if got := scene.TextLineCount2D(text); got != 4 {
		t.Fatalf("PHP folded rows %d", got)
	}
	// Composing Chinese above a fold shifts its painted byte offsets.
	text.Source = file.snapshot().Replace(0, 0, "中")
	m.editor.syncFolds(m, text, file, 0, 0, true)
	start, _, _, _ := scene.TextLineRange2D(text, 2)
	if got := text.Text().Slice(start, start+1); got != "}" {
		t.Fatalf("IME shifted visible closing brace to %q", got)
	}
}
