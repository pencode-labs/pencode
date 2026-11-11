package workbench

import (
	"testing"

	"github.com/onix-engine/onix/input"
)

func TestEditorPointerCursor(t *testing.T) {
	for _, zoom := range []int{75, 100, 150} {
		app, model, _, state := languageInteractionFixture(t)
		model.languages = nil
		model.settings.Zoom = zoom
		model.fontResources.Focus(0)
		markPencodeRootDirtyByModel(model.fontResources, model)
		languageInteractionStep(t, app)
		languageInteractionStep(t, app)
		move := func(x, y float32, want input.Cursor) {
			t.Helper()
			scale := float64(zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(x) * scale, Y: float64(y) * scale})
			languageInteractionStep(t, app)
			if got := state.Cursor(); got != want {
				t.Fatalf("zoom=%d cursor at (%v,%v) = %s, want %s", zoom, x, y, got, want)
			}
		}
		editor := editorContextBounds(t, model, "editor")
		move(editor.X+editor.Width/2, editor.Y+editor.Height/2, input.CursorText)
		// The line-number gutter keeps its normal pointer.
		body := editorContextBounds(t, model, "editor-body")
		move(body.X+8, body.Y+body.Height/2, input.CursorDefault)
		move(editor.X+30, editor.Y+30, input.CursorText)
		editorContextOpen(t, app, model, 0, 0, editor.X+30, editor.Y+30)
		menu := editorContextBounds(t, model, "editor-context-copy")
		move(menu.X+menu.Width/2, menu.Y+menu.Height/2, input.CursorPointer)
	}
}
