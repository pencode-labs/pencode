package workbench

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
)

func TestEditorPastePreservesViewport(t *testing.T) {
	for _, entry := range []string{"shortcut", "context-menu"} {
		for _, tc := range []struct {
			name, line, paste string
			selection         int
		}{
			{name: "wrapped-line", line: strings.Repeat("活动规则 ", 30) + "\n", paste: "ActivityXXXConf"},
			{name: "multiline", line: "活动规则\n", paste: strings.Repeat("粘贴🙂\r\n", 60)},
			{name: "selection", line: "活动规则🙂尾\n", paste: "替换\n内容", selection: 3},
			{name: "horizontal", line: strings.Repeat("x", 256) + "\n", paste: strings.Repeat("LongToken", 80)},
		} {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				app, model, _, state := languageInteractionFixture(t)
				model.languages = nil
				world := model.fontResources
				value := strings.Repeat(tc.line, 160)
				caret := utf8.RuneCountInString(tc.line)*96 + utf8.RuneCountInString(tc.line)/2
				anchor := caret + tc.selection
				model.updateActiveContent(value)
				model.setEditorValueAndCaret(world, value, caret)
				world.Mut[engineui.TextField](model.editor.control).Anchor = anchor
				markPencodeRootDirtyByModel(world, model)
				languageInteractionStep(t, app)
				if entry == "context-menu" {
					editorContextOpen(t, app, model, caret, anchor, 150, 20)
				}
				text, _ := world.Get[scene.Text2D](model.editor.text)
				x, y, _, height, ok := scene.TextCaretRect2D(&text, model.activeSnapshot(), caret)
				if !ok {
					t.Fatal("missing caret geometry")
				}
				link, _ := world.Get[htmlui.ControlText](model.editor.text)
				// Keep the insertion near the bottom, like a paste into a scrolled file.
				area := world.Mut[engineui.Textarea](model.editor.control)
				area.RestoreScroll(max(0, x-link.Content.Width/2), y+height-link.Content.Height*.8)
				languageInteractionStep(t, app)
				before, _ := world.Get[engineui.Textarea](model.editor.control)
				if before.ScrollY <= 0 || tc.name == "horizontal" && before.ScrollX <= 0 {
					t.Fatal("fixture did not establish a scrolled viewport")
				}
				state.SetClipboard(nil, func() (string, bool) { return tc.paste, true })
				if entry == "shortcut" {
					primary := input.KeyLeftControl
					if runtime.GOOS == "darwin" {
						primary = input.KeyLeftSuper
					}
					for _, key := range []input.KeyCode{primary, input.KeyV} {
						state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
					}
					languageInteractionStep(t, app)
					for _, key := range []input.KeyCode{primary, input.KeyV} {
						state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
					}
				} else {
					editorContextClick(t, app, model, state, "editor-context-paste")
				}
				paste := strings.ReplaceAll(tc.paste, "\r\n", "\n")
				original := snapshotText(value)
				want := value[:original.ByteOffset(caret)] + paste + value[original.ByteOffset(anchor):]
				field, _ := world.Get[engineui.TextField](model.editor.control)
				if model.activeContent() != want || field.Caret != caret+utf8.RuneCountInString(paste) || field.Anchor != field.Caret {
					t.Fatal("paste did not insert text and update the selection")
				}
				for range 3 {
					languageInteractionStep(t, app)
					after, _ := world.Get[engineui.Textarea](model.editor.control)
					if after.ScrollX != before.ScrollX || after.ScrollY != before.ScrollY {
						t.Fatalf("paste moved viewport from (%g, %g) to (%g, %g)", before.ScrollX, before.ScrollY, after.ScrollX, after.ScrollY)
					}
					link, _ := world.Get[htmlui.ControlText](model.editor.text)
					transform, _ := world.Get[scene.UITransform](model.editor.text)
					if transform.Translation.X.Value != link.Content.X-before.ScrollX || transform.Translation.Y.Value != link.Content.Y-before.ScrollY {
						t.Fatal("painted text moved away from the preserved viewport")
					}
				}
				// Preserving a paste must not disable later caret navigation.
				cursor := world.Mut[engineui.TextField](model.editor.control)
				cursor.Caret, cursor.Anchor = 0, 0
				languageInteractionStep(t, app)
				if area, _ := world.Get[engineui.Textarea](model.editor.control); area.ScrollX != 0 || area.ScrollY != 0 {
					t.Fatal("caret navigation no longer scrolls into view")
				}
			})
		}
	}
}
