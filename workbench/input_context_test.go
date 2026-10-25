package workbench

import (
	"testing"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/peb"
)

func openInputContext(t *testing.T, app *onix.App, model *workspaceModel, state *input.State, ref string, caret, anchor int) {
	t.Helper()
	languageInteractionStep(t, app)
	world := model.fontResources
	entity, ok := world.HTMLControl(model.surface, ref)
	if !ok {
		entity, ok = model.extensionFocusControl(world, ref)
	}
	if !ok || !world.Focus(entity) {
		t.Fatalf("cannot focus %s", ref)
	}
	field := module.ContentMut[engineui.TextField](world, entity)
	if ref == "find-query" && field.Value != model.findQuery {
		world.SetControlValue(entity, model.findQuery)
	}
	field.Caret, field.Anchor = caret, anchor
	r := editorContextBounds(t, model, ref)
	scale := float64(model.settings.Zoom) / 100
	x, y := float64(r.X+r.Width/2)*scale, float64(r.Y+r.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	if !model.inputContext.open || model.inputContext.target != entity {
		t.Fatalf("right click did not open text menu on %s: %+v", ref, model.inputContext)
	}
}

func TestInputContextExtensionOverlaysAndMultiline(t *testing.T) {
	for _, test := range []struct {
		name string
		kind peb.UIKind
	}{{"dialog", peb.Dialog}, {"popup", peb.Popup}} {
		t.Run(test.name, func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			err := model.applyExtensionUI("context-test", &extensions.ActionSession{}, peb.UI{
				ID: "form", Kind: test.kind, CloseOnOutsideClick: true,
				View: &peb.EditorView{HTML: `<textarea id="pencode-extension-context-test-input" data-ref="query" maxlength="6" style="width:240px;height:80px">甲乙🙂尾</textarea>`,
					Events: []peb.EditorBinding{{Ref: "query", Kind: "input"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			overlay := model.actions.ui.overlay
			markPencodeRootDirtyByModel(model.fontResources, model)
			for range 3 {
				languageInteractionStep(t, app)
			}
			state.SetClipboard(func(string) bool { return true }, func() (string, bool) { return "中\r\n🙂", true })
			openInputContext(t, app, model, state, "pencode-extension-context-test-input", 3, 1)
			editorContextClick(t, app, model, state, "input-context-paste")
			languageInteractionStep(t, app)
			entity, _ := model.fontResources.HTMLControl(model.surface, "pencode-extension-context-test-input")
			field := module.ContentRef[engineui.TextField](model.fontResources, entity)
			control := module.ContentRef[htmlui.Control](model.fontResources, entity)
			if field == nil || field.Value != "甲中\n🙂尾" || control.Value != field.Value || !field.Focused {
				t.Fatalf("multiline paste did not synchronize native control: field=%+v control=%+v", field, control)
			}
			if model.actions.ui.overlay != overlay {
				t.Fatal("text menu action dismissed its containing extension overlay")
			}
		})
	}
}

func TestInputContextClipboardAndInputEvents(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	model.findOpen, model.replaceOpen = true, true
	model.findQuery = "甲乙🙂尾"
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	clipboard, writeOK := "", true
	state.SetClipboard(func(value string) bool {
		if writeOK {
			clipboard = value
		}
		return writeOK
	}, func() (string, bool) { return clipboard, true })

	openInputContext(t, app, model, state, "find-query", 3, 1)
	editorContextClick(t, app, model, state, "input-context-copy")
	if clipboard != "乙🙂" || model.findQuery != "甲乙🙂尾" {
		t.Fatalf("copy: clipboard=%q query=%q", clipboard, model.findQuery)
	}
	openInputContext(t, app, model, state, "find-query", 1, 3)
	editorContextClick(t, app, model, state, "input-context-cut")
	languageInteractionStep(t, app)
	if clipboard != "乙🙂" || model.findQuery != "甲尾" {
		t.Fatalf("cut did not dispatch input: clipboard=%q query=%q", clipboard, model.findQuery)
	}
	clipboard = "中\r\n🙂"
	openInputContext(t, app, model, state, "find-query", 1, 1)
	editorContextClick(t, app, model, state, "input-context-paste")
	languageInteractionStep(t, app)
	if model.findQuery != "甲中🙂尾" {
		t.Fatalf("single line paste: %q", model.findQuery)
	}
	openInputContext(t, app, model, state, "find-query", 1, 3)
	writeOK = false
	editorContextClick(t, app, model, state, "input-context-cut")
	languageInteractionStep(t, app)
	if model.findQuery != "甲中🙂尾" {
		t.Fatal("failed clipboard write deleted text")
	}
	openInputContext(t, app, model, state, "find-query", 1, 1)
	editorContextClick(t, app, model, state, "input-context-select-all")
	entity, _ := model.fontResources.HTMLControl(model.surface, "find-query")
	field := module.ContentRef[engineui.TextField](model.fontResources, entity)
	if field.Caret != 4 || field.Anchor != 0 || !field.Focused {
		t.Fatalf("select all did not restore focus: %+v", field)
	}
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "新的查询"})
	languageInteractionStep(t, app)
	if model.findQuery != "新的查询" {
		t.Fatalf("typing after select all: %q", model.findQuery)
	}
	openInputContext(t, app, model, state, "find-query", 3, 1)
	editorContextClick(t, app, model, state, "input-context-delete")
	languageInteractionStep(t, app)
	if model.findQuery != "新询" {
		t.Fatalf("delete: %q", model.findQuery)
	}
}

func TestInputContextKeyboardPlacementAndIdle(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	model.findOpen, model.findQuery = true, "query"
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	for _, zoom := range []int{70, 100, 150} {
		model.setZoom(model.fontResources, zoom)
		languageInteractionStep(t, app)
		openInputContext(t, app, model, state, "find-query", 1, 1)
		menu := editorContextBounds(t, model, "input-context")
		root := editorContextBounds(t, model, "pencode-root")
		if menu.X < root.X || menu.Y < root.Y || menu.X+menu.Width > root.X+root.Width || menu.Y+menu.Height > root.Y+root.Height {
			t.Fatalf("menu exceeds viewport at zoom %d: %+v, root=%+v", zoom, menu, root)
		}
		if allocations := testing.AllocsPerRun(100, func() { model.updateInputContext(model.fontResources, state) }); allocations != 0 {
			t.Fatalf("idle menu allocated %g times", allocations)
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyDown})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyDown})
		languageInteractionStep(t, app)
		if model.inputContext.index != 4 {
			t.Fatalf("keyboard did not skip disabled delete: %+v", model.inputContext)
		}
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
		if model.inputContext.open || !model.findOpen || model.findQuery != "query" {
			t.Fatalf("Enter did not activate select all cleanly: menu=%+v find=%v query=%q focus=%v", model.inputContext, model.findOpen, model.findQuery, model.fontResources.FocusedControl())
		}
		openInputContext(t, app, model, state, "find-query", 3, 1)
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
		languageInteractionStep(t, app)
		if model.inputContext.open || !model.findOpen {
			t.Fatal("Escape closed the containing find popup")
		}
		entity, _ := model.fontResources.HTMLControl(model.surface, "find-query")
		field := module.ContentRef[engineui.TextField](model.fontResources, entity)
		if !field.Focused || field.Caret != 3 || field.Anchor != 1 {
			t.Fatalf("Escape lost focus or selection: %+v", field)
		}
	}
}

func TestInputContextReadOnlyPasswordAndStaleTarget(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	model.findOpen, model.findQuery = true, "private"
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	openInputContext(t, app, model, state, "find-query", 3, 1)
	entity := model.inputContext.target
	control := module.ContentMut[htmlui.Control](model.fontResources, entity)
	control.ReadOnly = true
	model.runInputContextEdit(app.World, "input-context-paste")
	if !model.inputContext.open || model.findQuery != "private" {
		t.Fatal("readonly paste was enabled")
	}
	control.ReadOnly = false
	field := module.ContentMut[engineui.TextField](model.fontResources, entity)
	field.Password = true
	model.runInputContextEdit(app.World, "input-context-copy")
	if !model.inputContext.open {
		t.Fatal("password copy was enabled")
	}
	field.Password, field.Value = false, "changed"
	model.runInputContextEdit(app.World, "input-context-delete")
	if field.Value != "changed" {
		t.Fatal("stale menu changed the input")
	}
	model.updateInputContext(model.fontResources, state)
	if model.inputContext.open {
		t.Fatal("stale menu remained open")
	}
}
