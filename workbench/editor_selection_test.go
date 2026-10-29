package workbench

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
)

func TestPencodeDoubleClickSelectsToken(t *testing.T) {
	app, session := newPencodeTestApp(t)
	model := session.active.model
	world := model.fontResources
	state, _ := app.World.GetResource[*input.State]()
	model.app.GetViewport = func() (int, int) { return 1440, 900 }
	app.World.InsertResource(scene.ViewportSize{Width: 1440, Height: 900, ScaleFactor: 1})
	for _, camera := range app.World.Query[scene.Camera]().All {
		camera.Mut().UpdateViewport(1440, 900)
	}
	value := "package demo\nvar foo_bar42 = 中文变量_2\n" + strings.Repeat("// wrapped text ", 40) + strings.Repeat("\n// scroll", 50) + "\nvar path = \"github.com/diiyw/onix/scene\"\nvar message = \"你好 世界\"\nvar result = foo_bar42 + 中文变量_2\n"
	model.updateActiveContent(value)
	model.setEditorValueAndCaret(world, value, 0)
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	for _, zoom := range []int{100, 150, 70} {
		model.setZoom(world, zoom)
		languageInteractionStep(t, app)
		for _, token := range []string{"foo_bar42", "中文变量_2", "github.com/diiyw/onix/scene", "你好 世界", "+"} {
			startByte := strings.LastIndex(value, token)
			start := utf8.RuneCountInString(value[:startByte])
			end := start + utf8.RuneCountInString(token)
			model.setEditorValueAndCaret(world, value, start)
			model.revealEditorCaret(world, start)
			languageInteractionStep(t, app)
			editorNode, _ := world.HTMLNode(model.surface, "editor")
			rect, _ := editorNode.Bounds()
			text, ox, oy := model.languageEditorGeometry(world, rect)
			// Hit the right half of the last glyph, where nearest-caret hit
			// testing points at the following delimiter or whitespace.
			x, y, _, height, ok := scene.TextCaretRect2D(text, value, end-1)
			nextX, _, _, _, nextOK := scene.TextCaretRect2D(text, value, end)
			if !ok || !nextOK || nextX <= x {
				t.Fatal("missing token glyph geometry")
			}
			scale := float64(zoom) / 100
			px, py := float64(ox+x+(nextX-x)*.8)*scale, float64(oy+y+height*.5)*scale
			for click := range 2 {
				state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: px, Y: py})
				languageInteractionStep(t, app)
				field, _ := world.Get[engineui.TextField](model.editor.control)
				if click == 0 && field.Anchor != field.Caret {
					t.Fatalf("single click selected text: %+v", field)
				}
				if click == 1 && (field.Anchor != start || field.Caret != end || field.Selecting) {
					t.Fatalf("zoom %d, token %q: selection = %d:%d, selecting=%v, want %d:%d", zoom, token, field.Anchor, field.Caret, field.Selecting, start, end)
				}
				state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: px, Y: py})
				languageInteractionStep(t, app)
			}
			field, _ := world.Get[engineui.TextField](model.editor.control)
			config := state.TextInputConfig()
			if field.Anchor != start || field.Caret != end || config.SelectionStart != start || config.SelectionEnd != end {
				t.Fatalf("release/platform lost token selection: field=%d:%d platform=%d:%d", field.Anchor, field.Caret, config.SelectionStart, config.SelectionEnd)
			}
		}
	}
	copied := ""
	state.SetClipboard(func(value string) bool { copied = value; return true }, func() (string, bool) { return copied, true })
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
	languageInteractionStep(t, app)
	if copied != "+" {
		t.Fatalf("copied token = %q, want +", copied)
	}
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "-"})
	languageInteractionStep(t, app)
	if field, _ := world.Get[engineui.TextField](model.editor.control); field.Value != strings.Replace(value, " + ", " - ", 1) {
		t.Fatal("typing did not replace the selected token")
	}
}
