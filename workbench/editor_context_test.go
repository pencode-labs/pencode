package workbench

import (
	"math"
	"testing"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func editorContextBounds(t *testing.T, model *workspaceModel, ref string) htmlui.Rect {
	t.Helper()
	node, ok := model.fontResources.HTMLNode(model.surface, ref)
	if !ok {
		t.Fatalf("missing menu node %s", ref)
	}
	bounds, ok := node.Bounds()
	if !ok || bounds.Width <= 0 || bounds.Height <= 0 {
		t.Fatalf("menu node %s is not visible: %+v", ref, bounds)
	}
	return bounds
}

func editorContextClick(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string) {
	t.Helper()
	r := editorContextBounds(t, model, ref)
	scale := float64(model.settings.Zoom) / 100
	x, y := float64(r.X+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
}

func editorContextOpen(t *testing.T, app *onix.App, model *workspaceModel, caret, anchor int, x, y float32) {
	t.Helper()
	model.setEditorValueAndCaret(model.fontResources, model.activeContent(), caret)
	model.fontResources.Mut[engineui.TextField](model.editor.control).Anchor = anchor
	model.languageUI = pencodeLanguageInteraction{
		contextOpen: true, contextFile: model.activeFile(), contextContent: model.activeContent(),
		contextCaret: caret, contextAnchor: anchor, contextX: x, contextY: y,
	}
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
}

func TestEditorContextClipboardWithoutLanguageServer(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	model.languages = nil
	clipboard := ""
	writeOK := true
	state.SetClipboard(func(value string) bool {
		if writeOK {
			clipboard = value
		}
		return writeOK
	}, func() (string, bool) { return clipboard, true })
	const original = "甲乙🙂尾\n"
	model.updateActiveContent(original)

	editorContextOpen(t, app, model, 3, 1, 150, 20)
	editorContextClick(t, app, model, state, "editor-context-copy")
	field := model.fontResources.Ref[engineui.TextField](model.editor.control)
	if clipboard != "乙🙂" || model.activeContent() != original || field.Caret != 3 || field.Anchor != 1 || model.languageUI.contextOpen {
		t.Fatalf("copy did not preserve Unicode selection: clipboard=%q field=%+v", clipboard, field)
	}

	editorContextOpen(t, app, model, 1, 3, 150, 20)
	editorContextClick(t, app, model, state, "editor-context-cut")
	if clipboard != "乙🙂" || model.activeContent() != "甲尾\n" {
		t.Fatalf("cut: clipboard=%q content=%q", clipboard, model.activeContent())
	}
	if !model.undoActiveContent(model.fontResources) || model.activeContent() != original {
		t.Fatal("cut could not be undone")
	}

	clipboard = "中\r\n🙂"
	editorContextOpen(t, app, model, 3, 1, 150, 20)
	editorContextClick(t, app, model, state, "editor-context-paste")
	field = model.fontResources.Ref[engineui.TextField](model.editor.control)
	if model.activeContent() != "甲中\n🙂尾\n" || field.Caret != 4 || field.Anchor != 4 {
		t.Fatalf("paste did not use native text normalization: content=%q field=%+v", model.activeContent(), field)
	}
	if !model.undoActiveContent(model.fontResources) || model.activeContent() != original {
		t.Fatal("paste could not be undone")
	}

	writeOK = false
	editorContextOpen(t, app, model, 3, 1, 150, 20)
	editorContextClick(t, app, model, state, "editor-context-cut")
	if model.activeContent() != original {
		t.Fatal("failed clipboard write deleted the selection")
	}

	editorContextOpen(t, app, model, 2, 2, 150, 20)
	editorContextClick(t, app, model, state, "editor-context-select-all")
	field = model.fontResources.Ref[engineui.TextField](model.editor.control)
	if field.Anchor != 0 || field.Caret != 5 || !field.Focused {
		t.Fatalf("select all: %+v", field)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "继续输入"})
	languageInteractionStep(t, app)
	if model.activeContent() != "继续输入" {
		t.Fatal("menu did not return typing focus to the selected text")
	}

	editorContextOpen(t, app, model, 3, 1, 150, 20)
	model.updateActiveContent("changed")
	model.runEditorContextEdit(model.fontResources, "editor-context-paste")
	if model.activeContent() != "changed" || model.languageUI.contextOpen {
		t.Fatal("stale context menu edited a different document snapshot")
	}
}

func TestEditorContextSubmenusHoverKeyboardAndPlacement(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(model.fontResources, zoom)
		languageInteractionStep(t, app)
		body := editorContextBounds(t, model, "editor-body")
		for _, x := range []float32{20, body.Width - 245} {
			editorContextOpen(t, app, model, 0, 0, x, 10)
			main := editorContextBounds(t, model, "lsp-context")
			last := editorContextBounds(t, model, "editor-context-group-3")
			if main.Width > 240.1 || last.Y+last.Height > main.Y+main.Height || main.Height < 336 {
				t.Fatalf("main menu width or height: menu=%+v last=%+v", main, last)
			}
			group := editorContextBounds(t, model, "editor-context-group-2")
			scale := float64(zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(group.X+group.Width/2) * scale, Y: float64(group.Y+group.Height/2) * scale})
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			if model.languageUI.contextGroup != 2 {
				t.Fatalf("hover did not open code assistance: zoom=%d x=%v group=%+v submenu=%d pointer=%+v", zoom, x, group, model.languageUI.contextGroup, state.Pointer())
			}
			submenu := editorContextBounds(t, model, "lsp-context-submenu")
			last = editorContextBounds(t, model, "lsp-context-18")
			if last.Y+last.Height > submenu.Y+submenu.Height || submenu.X < body.X || submenu.X+submenu.Width > body.X+body.Width || submenu.Y+submenu.Height > body.Y+body.Height {
				t.Fatalf("submenu is clipped: body=%+v submenu=%+v last=%+v", body, submenu, last)
			}
			if math.Abs(float64(submenu.X-(main.X+main.Width))) > 1 && math.Abs(float64(submenu.X+submenu.Width-main.X)) > 1 {
				t.Fatalf("submenu does not touch the main menu: main=%+v submenu=%+v", main, submenu)
			}
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(submenu.X+submenu.Width/2) * scale, Y: float64(submenu.Y+20) * scale})
			languageInteractionStep(t, app)
			if model.languageUI.contextGroup != 2 {
				t.Fatal("crossing into submenu closed it")
			}
			editorContextClick(t, app, model, state, "editor-context-group-3")
			if model.languageUI.contextGroup != 3 {
				t.Fatal("click did not switch submenus")
			}
			submenu = editorContextBounds(t, model, "lsp-context-submenu")
			last = editorContextBounds(t, model, "lsp-context-22")
			if last.Y+last.Height > submenu.Y+submenu.Height {
				t.Fatal("advanced language actions are clipped")
			}
			for _, key := range []input.KeyCode{input.KeyLeft, input.KeyRight, input.KeyEscape, input.KeyEscape} {
				state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
				state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
				languageInteractionStep(t, app)
				if key == input.KeyLeft && model.languageUI.contextGroup != 0 || key == input.KeyRight && model.languageUI.contextGroup != 3 {
					t.Fatalf("submenu keyboard navigation failed for %v", key)
				}
			}
			if model.languageUI.contextOpen {
				t.Fatal("Escape did not close the main menu")
			}
		}
	}
}

func TestEditorContextIdleAndHoverDoNotAllocate(t *testing.T) {
	app, model, _, _ := languageInteractionFixture(t)
	editorContextOpen(t, app, model, 0, 0, 20, 10)
	state, _ := model.fontResources.Resource[*input.State]()
	if allocations := testing.AllocsPerRun(100, func() { model.updateLanguageInteractions(model.fontResources, state) }); allocations != 0 {
		t.Fatalf("open idle menu allocated %g times", allocations)
	}
	row := editorContextBounds(t, model, "editor-context-group-2")
	model.hoverEditorContext(model.fontResources, row.X+10, row.Y+10)
	if allocations := testing.AllocsPerRun(100, func() {
		model.hoverEditorContext(model.fontResources, row.X+10, row.Y+10)
		model.positionEditorSubmenu(model.fontResources)
	}); allocations != 0 {
		t.Fatalf("submenu hover allocated %g times", allocations)
	}
}
