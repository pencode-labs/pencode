package workbench

import (
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	engineui "github.com/diiyw/onix/ui"
	"github.com/pencode-labs/pencode/terminal"
)

func TestPencodeTerminalNativeInputAndLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX shell interaction")
	}
	app, model, _, state := languageInteractionFixture(t)
	world := model.fontResources
	editorContent := model.activeContent()
	editorContextClick(t, app, model, state, "status-terminal")
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	if model.bottom.active != pencodePanelTerminal || model.terminals == nil || model.terminals.active == nil {
		t.Fatal("terminal did not open")
	}
	item := model.terminals.active
	defer func() {
		model.closeTerminal("")
		select {
		case <-item.session.Done():
		case <-time.After(5 * time.Second):
			t.Error("session leaked")
		}
	}()
	control, ok := world.HTMLControl(model.surface, "terminal-content")
	if !ok || !model.terminalFocused(world) {
		t.Fatal("terminal did not receive focus")
	}
	if field, ok := world.Get[engineui.TextField](control); !ok || field.Value == "" {
		t.Fatal("native terminal screen missing")
	}
	panel := editorContextBounds(t, model, "terminal-pane")
	toolbar := editorContextBounds(t, model, "terminal-tabs")
	if panel.Y < toolbar.Y+toolbar.Height-1 {
		t.Fatalf("toolbar overlaps terminal: %+v %+v", panel, toolbar)
	}
	// The committed IME string reaches the shell; it must never edit the source.
	state.Enqueue(input.Event{Kind: input.EventTextInput, Text: "printf '\\120\\105\\116_中文'"})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		languageInteractionStep(t, app)
		if strings.Contains(model.terminalView.value, "PEN_中文") {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if !strings.Contains(model.terminalView.value, "PEN_中文") {
		t.Fatalf("shell output missing: %q", model.terminalView.value)
	}
	if model.activeContent() != editorContent {
		t.Fatal("terminal edited the source")
	}
	if x, y := item.screen.Cursor(); x < 0 || x >= item.screen.Cols || y < 0 || y >= item.screen.Rows {
		t.Fatal("invalid cursor")
	}
	if item.screen.Cols == 80 && item.screen.Rows == 12 {
		t.Fatal("terminal was not sized to the native control")
	}
	// Escape belongs to applications in the terminal, and must not hide the panel.
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if model.bottom.active != pencodePanelTerminal {
		t.Fatal("Escape hid the terminal")
	}
	// Copy uses the native selection; Ctrl+C remains a PTY interrupt.
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, func() (string, bool) { return "", false })
	field := world.Mut[engineui.TextField](control)
	start := strings.Index(field.Value, "PEN_中文")
	field.Anchor = utf8.RuneCountInString(field.Value[:start])
	field.Caret = field.Anchor + utf8.RuneCountInString("PEN_中文")
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftShift})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyC})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyC})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftShift})
	languageInteractionStep(t, app)
	if copied != "PEN_中文" {
		t.Fatalf("terminal selection copy %q", copied)
	}
	// The separator changes both the native pane and PTY geometry.
	before := editorContextBounds(t, model, "bottom-panel")
	handle := editorContextBounds(t, model, "panel-resize")
	x, y := float64(handle.X+handle.Width/2), float64(handle.Y+2)
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseLeft, X: x, Y: y})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseMove, X: x, Y: y - 50})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseLeft, X: x, Y: y - 50})
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	after := editorContextBounds(t, model, "bottom-panel")
	if after.Height < before.Height+40 {
		t.Fatalf("panel drag did not resize: %g -> %g", before.Height, after.Height)
	}

	editorContextClick(t, app, model, state, "status-output")
	if model.bottom.active != pencodePanelOutput {
		t.Fatal("Output did not replace terminal")
	}
	_, exited, _ := item.session.Status()
	if exited {
		t.Fatal("hiding terminal killed the shell")
	}
	editorContextClick(t, app, model, state, "status-terminal")
	languageInteractionStep(t, app)
	if model.terminals.active != item || !model.terminalFocused(world) {
		t.Fatal("reopening lost the session/focus")
	}
	editorContextClick(t, app, model, state, "terminal-new")
	languageInteractionStep(t, app)
	other := model.terminals.active
	if other == item || len(model.terminals.items) != 2 {
		t.Fatal("new terminal did not create an independent session")
	}
	editorContextClick(t, app, model, state, item.selectRef)
	languageInteractionStep(t, app)
	if model.terminals.active != item || !model.terminalFocused(world) {
		t.Fatal("session switch did not restore focus")
	}
	model.closeTerminal(model.projectPath)
	if len(model.terminals.items) != 0 || model.terminals.active != nil {
		t.Fatal("closing project retained terminals")
	}
	if model.bottom.active != pencodePanelClosed {
		t.Fatal("closing the project's last terminal left the panel open")
	}
	select {
	case <-other.session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("second session leaked")
	}
}
func TestPencodeTerminalPaintAndIdleAllocation(t *testing.T) {
	model := &workspaceModel{}
	item := &pencodeTerminal{screen: terminal.NewScreen(8, 3)}
	model.terminals = &pencodeTerminals{active: item, items: []*pencodeTerminal{item}}
	item.screen.Feed([]byte("\x1b[31;44;4m中X\x1b[0m"))
	model.prepareTerminalView()
	if !strings.HasPrefix(model.terminalView.value, "中X") || len(model.terminalView.spans) < 2 || len(model.terminalView.backgrounds) != 1 {
		t.Fatal("terminal paint missing styles")
	}
	if len(model.terminalView.underlines) != 1 || model.terminalView.underlines[0].width != 3 {
		t.Fatal("terminal underline missing")
	}
	if model.terminalView.backgrounds[0].width != 3 {
		t.Fatal("wide glyph background did not span two cells")
	}
	if n := testing.AllocsPerRun(100, func() { model.prepareTerminalView() }); n != 0 {
		t.Fatalf("idle terminal allocated %g", n)
	}
}

func TestPencodeTerminalTabsContextAndExit(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX shell interaction")
	}
	app, model, _, state := languageInteractionFixture(t)
	world := model.fontResources
	defer model.closeTerminal("")
	editorContextClick(t, app, model, state, "status-terminal")
	languageInteractionStep(t, app)
	first := model.terminals.active
	editorContextClick(t, app, model, state, "terminal-new")
	second := model.terminals.active
	editorContextClick(t, app, model, state, first.selectRef)
	if model.terminals.active != first {
		t.Fatal("tab click did not switch sessions")
	}
	editorContextClick(t, app, model, state, second.closeRef)
	if model.terminals.active != first || len(model.terminals.items) != 1 {
		t.Fatal("closing an inactive tab changed the active terminal")
	}

	waitFor := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			languageInteractionStep(t, app)
			if check() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("terminal condition timed out")
	}
	waitFor(func() bool { return first.ready })
	first.screen.Feed([]byte("\r\nCONTEXT_COPY"))
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	languageInteractionStep(t, app)
	field := world.Mut[engineui.TextField](model.terminalView.control)
	start := strings.Index(field.Value, "CONTEXT_COPY")
	if start < 0 {
		t.Fatal("selection fixture missing")
	}
	field.Anchor = utf8.RuneCountInString(field.Value[:start])
	field.Caret = field.Anchor + len("CONTEXT_COPY")
	var copied string
	state.SetClipboard(func(value string) bool { copied = value; return true }, func() (string, bool) {
		return "printf '\\120\\101\\123\\124\\105_OK'\n", true
	})
	openMenu := func() {
		t.Helper()
		bounds := editorContextBounds(t, model, "terminal-content")
		x, y := float64(bounds.X+30), float64(bounds.Y+20)
		state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
		state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
		languageInteractionStep(t, app)
		if !model.terminalView.context.open {
			t.Fatal("terminal context menu did not open")
		}
		menu := editorContextBounds(t, model, "terminal-context")
		root := editorContextBounds(t, model, "pencode-root")
		if menu.X < root.X || menu.Y < root.Y || menu.X+menu.Width > root.X+root.Width || menu.Y+menu.Height > root.Y+root.Height {
			t.Fatalf("context menu escaped root: %+v %+v", menu, root)
		}
	}
	openMenu()
	editorContextClick(t, app, model, state, "terminal-copy")
	if copied != "CONTEXT_COPY" || model.terminalView.context.open {
		t.Fatalf("context copy failed: %q", copied)
	}
	openMenu()
	editorContextClick(t, app, model, state, "terminal-paste")
	// A shell with bracketed paste enabled inserts the newline as input text;
	// execute the pasted command explicitly after its prompt is ready.
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEnter})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEnter})
	languageInteractionStep(t, app)
	waitFor(func() bool { return strings.Contains(model.terminalView.value, "PASTE_OK") })

	openMenu()
	editorContextClick(t, app, model, state, "terminal-clear")
	languageInteractionStep(t, app)
	if strings.Contains(model.terminalView.value, "PASTE_OK") || len(first.screen.History) != 0 {
		t.Fatal("context clear retained screen/history")
	}
	first.screen.Feed([]byte("CLEAR_SHORTCUT"))
	markPencodeRootDirtyByModel(world, model)
	languageInteractionStep(t, app)
	openMenu()
	// Escape closes the menu without sending Escape into the shell.
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyEscape})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyEscape})
	languageInteractionStep(t, app)
	if model.terminalView.context.open || model.bottom.active != pencodePanelTerminal {
		t.Fatal("Escape did not dismiss only the terminal menu")
	}
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyLeftShift})
	state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: input.KeyK})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyK})
	languageInteractionStep(t, app)
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftControl})
	state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: input.KeyLeftShift})
	languageInteractionStep(t, app)
	if strings.Contains(model.terminalView.value, "CLEAR_SHORTCUT") || len(first.screen.History) != 0 {
		t.Fatal("clear shortcut retained screen/history")
	}

	editorContextClick(t, app, model, state, "terminal-new")
	third := model.terminals.active
	waitFor(func() bool { return third.ready })
	editorContextClick(t, app, model, state, "terminal-new")
	fourth := model.terminals.active
	waitFor(func() bool { return fourth.ready })
	model.writeTerminal(fourth, []byte("exit\r"))
	waitFor(func() bool { return len(model.terminals.items) == 2 })
	if model.terminals.active != third {
		t.Fatal("active shell exit did not select the adjacent tab")
	}
	// Exiting an inactive shell must remove only its own tab.
	model.writeTerminal(first, []byte("exit\r"))
	waitFor(func() bool { return len(model.terminals.items) == 1 })
	if model.terminals.active != third {
		t.Fatal("inactive shell exit changed active tab")
	}
	model.writeTerminal(third, []byte("exit\r"))
	waitFor(func() bool { return len(model.terminals.items) == 0 })
	if model.terminals.active != nil || model.terminalView.value != "" {
		t.Fatal("last shell exit retained terminal content")
	}
	if model.bottom.active != pencodePanelClosed {
		t.Fatal("last shell exit left the terminal panel open")
	}
	editorContextClick(t, app, model, state, "status-terminal")
	if model.terminals.active == nil || len(model.terminals.items) != 1 {
		t.Fatal("could not create a terminal after the last tab exited")
	}
	editorContextClick(t, app, model, state, model.terminals.active.closeRef)
	if model.bottom.active != pencodePanelClosed || len(model.terminals.items) != 0 {
		t.Fatal("closing the last tab left the terminal panel open")
	}
	editorContextClick(t, app, model, state, "status-terminal")
	editorContextClick(t, app, model, state, "status-output")
	model.closeTerminal("")
	if model.bottom.active != pencodePanelOutput {
		t.Fatal("closing background terminals hid the output panel")
	}
}
