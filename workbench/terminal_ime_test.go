package workbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

func TestPencodeTerminalIMECaretDuringRedraw(t *testing.T) {
	for _, zoom := range []int{70, 100, 150} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			world := model.fontResources
			model.setZoom(world, zoom)
			item := &pencodeTerminal{id: "1", label: "Terminal", selectRef: "terminal-tab:1", closeRef: "terminal-close:1", screen: terminal.NewScreen(80, 12), session: &terminal.Session{}, exited: true}
			model.terminals = &pencodeTerminals{active: item, items: []*pencodeTerminal{item}, focus: true}
			t.Cleanup(func() { model.terminals = nil })
			model.bottom.active = pencodePanelTerminal
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			redraw := func(value string) {
				t.Helper()
				item.screen.Feed([]byte(value))
				model.refreshRegion(world, pencodeRefreshTerminal)
				languageInteractionStep(t, app)
			}
			redraw("\x1b[2;4H")
			assertCaret := func(col, row int) {
				t.Helper()
				text := world.Ref[scene.Text2D](model.terminalView.text)
				width, height, ok := scene.MeasureTextValue2D(text, "M")
				if !ok {
					t.Fatal("missing terminal metrics")
				}
				bounds := editorContextBounds(t, model, "terminal-content")
				link := world.Ref[htmlui.ControlText](model.terminalView.text)
				scale := float32(zoom) / 100
				want := input.TextInputRect{X: (bounds.X + link.Content.X + float32(col)*width) * scale, Y: (bounds.Y + link.Content.Y + float32(row)*height) * scale, Width: width * scale, Height: height * scale}
				got := state.TextInputConfig().Caret
				if math.Abs(float64(got.X-want.X)) > .1 || math.Abs(float64(got.Y-want.Y)) > .1 || math.Abs(float64(got.Width-want.Width)) > .1 || math.Abs(float64(got.Height-want.Height)) > .1 {
					t.Fatalf("IME caret = %+v, want shell caret %+v", got, want)
				}
			}
			assertCaret(3, 1)
			revision := state.TextInputConfig().Revision
			for i, preedit := range []string{"z", "zh", "中"} {
				state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Text: preedit, Active: true}})
				// Full-screen applications move their cursor while painting. The
				// candidate window must stay at the composition's original cell.
				redraw(fmt.Sprintf("\x1b[Hframe %d\x1b[3;9H", i))
				assertCaret(3, 1)
				field := world.Mut[engineui.TextField](model.terminalView.control)
				field.Anchor, field.Caret = 0, 5
				languageInteractionStep(t, app)
				assertCaret(3, 1)
				if !state.Composing() || state.TextInputConfig().Revision != revision {
					t.Fatal("redraw restarted the input session")
				}
			}
			state.Enqueue(input.Event{Kind: input.EventTextComposition})
			languageInteractionStep(t, app)
			assertCaret(8, 2)
			// An active composition can have empty preedit text. Switching tabs
			// must still replace the previous terminal's frozen anchor.
			state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Active: true}})
			languageInteractionStep(t, app)
			redraw("\x1b[H")
			assertCaret(8, 2)
			other := &pencodeTerminal{id: "2", label: "Other", selectRef: "terminal-tab:2", closeRef: "terminal-close:2", screen: terminal.NewScreen(item.screen.Cols, item.screen.Rows), session: &terminal.Session{}, exited: true}
			other.screen.Feed([]byte("\x1b[1;2H"))
			model.terminals.active = other
			model.terminals.items = append(model.terminals.items, other)
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			assertCaret(1, 0)
			if n := testing.AllocsPerRun(100, func() { model.syncTerminalIME(world, state) }); n != 0 {
				t.Fatalf("warm IME positioning allocated %g times", n)
			}
			world.Focus(model.editor.control)
			languageInteractionStep(t, app)
			if model.terminalView.ime.terminal != nil {
				t.Fatal("blur retained the composition anchor")
			}
			caret := state.TextInputConfig().Caret
			model.syncTerminalIME(world, state)
			if state.TextInputConfig().Caret != caret {
				t.Fatal("unfocused terminal replaced the editor's IME caret")
			}
			world.Focus(model.terminalView.control)
			languageInteractionStep(t, app)
			assertCaret(1, 0)
			if n := testing.AllocsPerRun(100, func() { model.syncTerminalIME(world, state) }); n != 0 {
				t.Fatalf("idle IME positioning allocated %g times", n)
			}
		})
	}
}
