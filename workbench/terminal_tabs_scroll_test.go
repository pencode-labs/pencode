package workbench

import (
	"fmt"
	"math"
	"testing"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/terminal"
)

func TestPencodeTerminalTabOverflowScroll(t *testing.T) {
	for _, zoom := range []int{100, 150} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			world := model.fontResources
			model.settings.Zoom = zoom
			model.bottom.active = pencodePanelTerminal
			terminals := model.terminalState()
			// Retired sessions exercise the UI without launching native shells.
			t.Cleanup(func() { model.terminals = nil })
			for i := range 20 {
				id := fmt.Sprint(i + 1)
				terminals.items = append(terminals.items, &pencodeTerminal{
					id: id, label: "Terminal " + id, selectRef: "terminal-tab:" + id, closeRef: "terminal-close:" + id,
					screen: terminal.NewScreen(80, 12), session: &terminal.Session{}, exited: true,
				})
			}
			selectTab := func(index int) {
				terminals.active = terminals.items[index]
				markPencodeRootDirtyByModel(world, model)
				languageInteractionStep(t, app)
			}
			selectTab(len(terminals.items) - 1)
			area := model.terminalView.tabScroll.area
			scroll := world.Mut[scene.UIScroll](area)
			if scroll == nil || !scroll.Horizontal || scroll.ScrollbarSize != 0 || scroll.Position.X() >= 0 {
				t.Fatalf("terminal tabs must overflow without a scrollbar and reveal the last tab: %+v", scroll)
			}
			assertVisible := func() {
				t.Helper()
				viewport := editorContextBounds(t, model, "terminal-tabs")
				tab := editorContextBounds(t, model, "active-terminal-tab")
				left := tab.X + world.Ref[scene.UIScroll](area).Position.X() - viewport.X
				if left < -.01 || left+tab.Width > viewport.Width+.01 {
					t.Fatalf("active terminal tab is clipped: left=%v tab=%+v viewport=%+v", left, tab, viewport)
				}
				button := editorContextBounds(t, model, "terminal-new")
				tools := editorContextBounds(t, model, "bottom-panel")
				if button.Width < 28 || button.X < viewport.X+viewport.Width || button.X+button.Width > tools.X+tools.Width+.01 {
					t.Fatalf("new terminal button is clipped: %+v, tools=%+v", button, tools)
				}
			}
			assertVisible()
			first := editorContextBounds(t, model, terminals.items[0].selectRef)
			close := editorContextBounds(t, model, terminals.items[0].closeRef)
			second := editorContextBounds(t, model, terminals.items[1].selectRef)
			if second.Y != first.Y || second.X-(close.X+close.Width) < 6 {
				t.Fatalf("terminal tabs must stay in one row with spacing: first=%+v close=%+v second=%+v", first, close, second)
			}
			viewport := editorContextBounds(t, model, "terminal-tabs")
			scale := float64(zoom) / 100
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: float64(viewport.X+viewport.Width/2) * scale, Y: float64(viewport.Y+viewport.Height/2) * scale})
			wheel := func(x, y float64) {
				state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelX: x, WheelY: y})
				languageInteractionStep(t, app)
			}
			before := scroll.Position.X()
			wheel(0, 1)
			languageInteractionStep(t, app)
			if got := world.Ref[scene.UIScroll](area).Position.X(); math.Abs(float64(got-before-48)) > .01 {
				t.Fatalf("wheel scrolling did not persist: got %v, want %v", got, before+48)
			}
			wheel(0, 10000)
			if got := world.Ref[scene.UIScroll](area).Position.X(); got != 0 {
				t.Fatalf("left boundary=%v", got)
			}
			wheel(-1, 0)
			if got := world.Ref[scene.UIScroll](area).Position.X(); got != -48 {
				t.Fatalf("horizontal wheel position=%v, want -48", got)
			}
			wheel(0, -10000)
			selectTab(0)
			assertVisible()
			local, _ := world.Resource[*input.State]()
			local.ScrollMouse(0, -1)
			if allocations := testing.AllocsPerRun(50, func() {
				model.syncTerminalTabScroll(world)
				model.updateTabWheel(world, local)
			}); allocations != 0 {
				t.Fatalf("terminal tab scrolling allocated %g times", allocations)
			}
			before = world.Ref[scene.UIScroll](area).Position.X()
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: 5, Y: 5})
			wheel(0, 1)
			if got := world.Ref[scene.UIScroll](area).Position.X(); got != before {
				t.Fatal("wheel outside the terminal tab strip moved tabs")
			}
			terminals.items = terminals.items[:1]
			selectTab(0)
			scroll = world.Mut[scene.UIScroll](area)
			if scroll == nil || scroll.Horizontal || scroll.Position.X() != 0 {
				t.Fatalf("removing overflow tabs did not reset scrolling: %+v", scroll)
			}
		})
	}
}
