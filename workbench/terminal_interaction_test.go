package workbench

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/scene"
	engineui "github.com/onix-engine/onix/ui"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

type terminalInputProcess struct {
	closed chan struct{}
	writes chan string
	once   sync.Once
}

func (p *terminalInputProcess) Read([]byte) (int, error) { <-p.closed; return 0, io.EOF }
func (p *terminalInputProcess) Write(value []byte) (int, error) {
	p.writes <- string(value)
	return len(value), nil
}
func (p *terminalInputProcess) Close() error        { p.once.Do(func() { close(p.closed) }); return nil }
func (p *terminalInputProcess) Wait() error         { <-p.closed; return nil }
func (*terminalInputProcess) Resize(int, int) error { return nil }

func TestPencodeTerminalRepeatedKeys(t *testing.T) {
	app, model, _, state := languageInteractionFixture(t)
	process := &terminalInputProcess{closed: make(chan struct{}), writes: make(chan string, 1024)}
	item := &pencodeTerminal{id: "1", label: "Terminal", selectRef: "terminal-tab:1", closeRef: "terminal-close:1", screen: terminal.NewScreen(80, 12)}
	item.session = terminal.Attach(80, 12, nil, func() (terminal.Process, error) { return process, nil })
	t.Cleanup(func() { item.session.Close(); <-item.session.Done(); model.terminals = nil })
	model.terminals = &pencodeTerminals{active: item, items: []*pencodeTerminal{item}, focus: true}
	model.bottom.active = pencodePanelTerminal
	markPencodeRootDirtyByModel(model.fontResources, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	read := func(want string) {
		t.Helper()
		var got string
		deadline := time.After(3 * time.Second)
		for len(got) < len(want) {
			select {
			case value := <-process.writes:
				got += value
			case <-deadline:
				t.Fatalf("terminal input %q, want %q", got, want)
			}
		}
		if got != want {
			t.Fatalf("terminal input %q, want %q", got, want)
		}
	}
	for _, test := range []struct {
		name, sequence string
		key            input.KeyCode
	}{
		{"left", "\x1b[D", input.KeyLeft}, {"delete", "\x1b[3~", input.KeyDelete},
		{"backspace", "\x7f", input.KeyBackspace}, {"enter", "\r", input.KeyEnter},
		{"tab", "\t", input.KeyTab},
	} {
		t.Run(test.name, func(t *testing.T) {
			state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: test.key})
			languageInteractionStep(t, app)
			read(test.sequence)
			// Native repeat events keep the key down, without another press edge.
			for range 2 {
				state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: test.key})
			}
			languageInteractionStep(t, app)
			read(strings.Repeat(test.sequence, 2))
			// A held key also repeats when the backend emits no repeated down.
			model.terminalView.repeatAt = time.Unix(1, 0)
			languageInteractionStep(t, app)
			read(test.sequence)
			state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: test.key})
			languageInteractionStep(t, app)
			select {
			case extra := <-process.writes:
				t.Fatalf("key release wrote %q", extra)
			default:
			}
		})
	}
	// Repeating a copy shortcut must never turn into a shell interrupt.
	for _, key := range []input.KeyCode{input.KeyLeftControl, input.KeyLeftShift, input.KeyC} {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
	}
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
	languageInteractionStep(t, app)
	select {
	case extra := <-process.writes:
		t.Fatalf("copy shortcut wrote %q", extra)
	default:
	}
	for _, key := range []input.KeyCode{input.KeyC, input.KeyLeftShift, input.KeyLeftControl} {
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
	}
	languageInteractionStep(t, app)
	// Composing backspace/enter repeats belong to the IME.
	state.Enqueue(input.Event{Kind: input.EventTextComposition, Composition: input.TextComposition{Text: "中", Active: true}})
	for range 2 {
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyBackspace})
	}
	languageInteractionStep(t, app)
	select {
	case extra := <-process.writes:
		t.Fatalf("composition wrote %q", extra)
	default:
	}
}

func TestPencodeTerminalScrollSelection(t *testing.T) {
	for _, zoom := range []int{100, 150} {
		t.Run(fmt.Sprint(zoom), func(t *testing.T) {
			app, model, _, state := languageInteractionFixture(t)
			world := model.fontResources
			model.settings.Zoom = zoom
			item := &pencodeTerminal{id: "1", label: "Terminal", selectRef: "terminal-tab:1", closeRef: "terminal-close:1", screen: terminal.NewScreen(80, 12), session: &terminal.Session{}, exited: true}
			model.terminals = &pencodeTerminals{active: item, items: []*pencodeTerminal{item}, focus: true}
			t.Cleanup(func() { model.terminals = nil })
			model.bottom.active = pencodePanelTerminal
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			for i := range 50 {
				item.screen.Feed(fmt.Appendf(nil, "line%02d 中e\u0301\r\n", i))
			}
			markPencodeRootDirtyByModel(world, model)
			languageInteractionStep(t, app)
			languageInteractionStep(t, app)
			bounds := editorContextBounds(t, model, "terminal-content")
			link, _ := world.Get[htmlui.ControlText](model.terminalView.text)
			text := world.Mut[scene.Text2D](model.terminalView.text)
			width, height, ok := scene.MeasureTextValue2D(text, "M")
			if !ok {
				t.Fatal("missing terminal font")
			}
			scale := float64(zoom) / 100
			x, y := float64(bounds.X+link.Content.X+width*.1)*scale, float64(bounds.Y+link.Content.Y+height*.5)*scale
			state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
			languageInteractionStep(t, app)
			anchor := item.selection.anchor
			if !item.selection.dragging || anchor.rune != 0 {
				t.Fatalf("drag did not start: %+v", item.selection)
			}
			state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: 2})
			languageInteractionStep(t, app)
			if item.scroll != 6 || item.selection.anchor != anchor || item.selection.caret.row != anchor.row-6 {
				t.Fatalf("wheel lost selection: scroll=%d, %+v", item.scroll, item.selection)
			}
			selected := model.terminalSelection()
			if strings.Count(selected, "\n") != 6 || !strings.Contains(selected, "中e\u0301") {
				t.Fatalf("scroll selection missing rows or unicode: %q", selected)
			}
			// Dragging beyond the edge scrolls even while the pointer is stationary.
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: float64(bounds.Y-10) * scale})
			languageInteractionStep(t, app)
			if item.scroll != 7 {
				t.Fatalf("edge drag did not scroll: %d", item.scroll)
			}
			// Keep the pending timer set, but due, without another move event.
			item.selection.nextScroll = time.Unix(1, 0)
			languageInteractionStep(t, app)
			if item.scroll != 8 {
				t.Fatalf("stationary edge drag did not scroll: %d", item.scroll)
			}
			state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: float64(bounds.Y-10) * scale})
			languageInteractionStep(t, app)
			if item.selection.dragging {
				t.Fatal("released drag stayed active")
			}
			selected = model.terminalSelection()
			if strings.Count(selected, "\n") != 8 {
				t.Fatalf("edge selection lost history: %q", selected)
			}
			var copied string
			state.SetClipboard(func(value string) bool { copied = value; return true }, nil)
			model.terminalAction(state, "terminal-copy", selected)
			if copied != selected {
				t.Fatal("copy lost off-screen rows")
			}
			// Scrolling after release preserves the selected historical content.
			state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y})
			state.Enqueue(input.Event{Kind: input.EventMouseWheel, WheelY: -1000})
			languageInteractionStep(t, app)
			if model.terminalSelection() != selected {
				t.Fatal("scrolling replaced the selection")
			}
			field := world.Ref[engineui.TextField](model.terminalView.control)
			if field.Anchor != 0 || field.Caret != 0 {
				t.Fatalf("off-screen selection painted unrelated text: %+v", field)
			}
			for entity, access := range world.Query2[engineui.Relation[engineui.SurfaceStyle], engineui.Indexed[engineui.SelectionPart]]() {
				if access.A.Get().Owner != model.terminalView.control {
					continue
				}
				if size, ok := world.Get[scene.Size2D](entity); ok && size.Width > 0 && size.Height > 0 {
					t.Fatalf("off-screen selection still painted: %+v", size)
				}
			}
			local, _ := world.Resource[*input.State]()
			if n := testing.AllocsPerRun(50, func() {
				model.syncTerminalSelection(world)
				model.updateTerminalSelection(world, local, float32(x)/float32(scale), float32(y)/float32(scale), true)
			}); n != 0 {
				t.Fatalf("warm terminal selection allocated %g", n)
			}

		})
	}
}
