package workbench

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

func (m *workspaceModel) terminalFocused(world *module.Content) bool {
	if m.bottom.active != pencodePanelTerminal {
		return false
	}
	field, ok := world.Get[engineui.TextField](m.terminalView.control)
	return ok && field.Focused
}

// The native text field owns pointer selection and IME; shell editing happens in
// the PTY. Its keyboard interceptor runs exactly once in the native input phase.
func (m *workspaceModel) terminalKeys(state *input.State) bool {
	t := m.terminals
	if m.terminalView.context.open || m.terminalView.context.keysHandled {
		return true
	}
	if t == nil || t.active == nil {
		return true
	}
	item := t.active
	s := item.screen
	ctrl := state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl)
	shift := state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
	super := state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper)
	alt := state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt)
	if m.commandMatches("terminal-toggle", state, runtime.GOOS) || m.commandMatches("terminal-new", state, runtime.GOOS) {
		return true
	}
	copyKey := m.commandMatches("terminal-copy", state, runtime.GOOS)
	pasteKey := m.commandMatches("terminal-paste", state, runtime.GOOS) || m.commandMatches("terminal-paste-alternate", state, runtime.GOOS)
	if copyKey {
		m.terminalAction(state, "terminal-copy", m.terminalSelection())
		return true
	}
	if pasteKey {
		m.terminalAction(state, "terminal-paste", "")
		return true
	}
	if m.commandMatches("terminal-clear", state, runtime.GOOS) {
		m.terminalAction(state, "terminal-clear", "")
		return true
	}
	if m.commandMatches("terminal-scroll-up", state, runtime.GOOS) || m.commandMatches("terminal-scroll-down", state, runtime.GOOS) {
		step := s.Rows - 1
		if m.commandMatches("terminal-scroll-down", state, runtime.GOOS) {
			step = -step
		}
		item.scroll = max(0, min(len(s.History)/s.Cols, item.scroll+step))
		markPencodeRootDirtyByModel(m.fontResources, m)
		return true
	}
	if super {
		return true
	}
	if ctrl {
		var b byte
		found := false
		for key := input.KeyA; key <= input.KeyZ; key++ {
			if state.KeyPressed(key) {
				b = byte(key-input.KeyA) + 1
				found = true
				break
			}
		}
		if state.KeyPressed(input.KeySpace) {
			b = 0
			found = true
		}
		if state.KeyPressed(input.KeyLeftBracket) {
			b = 27
			found = true
		}
		if state.KeyPressed(input.KeyBackslash) {
			b = 28
			found = true
		}
		if state.KeyPressed(input.KeyRightBracket) {
			b = 29
			found = true
		}
		if found {
			data := [2]byte{27, b}
			if alt {
				m.writeTerminal(item, data[:])
			} else {
				m.writeTerminal(item, data[1:])
			}
			return true
		}
	}
	modifier := 1
	if shift {
		modifier++
	}
	if alt {
		modifier += 2
	}
	if ctrl {
		modifier += 4
	}
	for _, key := range [...]struct {
		sequence, final string
		code            input.KeyCode
	}{
		{code: input.KeyUp, sequence: "A", final: "A"}, {code: input.KeyDown, sequence: "B", final: "B"}, {code: input.KeyRight, sequence: "C", final: "C"}, {code: input.KeyLeft, sequence: "D", final: "D"}, {code: input.KeyHome, sequence: "H", final: "H"}, {code: input.KeyEnd, sequence: "F", final: "F"},
		{code: input.KeyInsert, sequence: "2~", final: ""}, {code: input.KeyDelete, sequence: "3~", final: ""}, {code: input.KeyPageUp, sequence: "5~", final: ""}, {code: input.KeyPageDown, sequence: "6~", final: ""},
		{code: input.KeyF1, sequence: "P", final: "P"}, {code: input.KeyF2, sequence: "Q", final: "Q"}, {code: input.KeyF3, sequence: "R", final: "R"}, {code: input.KeyF4, sequence: "S", final: "S"}, {code: input.KeyF5, sequence: "15~", final: ""}, {code: input.KeyF6, sequence: "17~", final: ""}, {code: input.KeyF7, sequence: "18~", final: ""}, {code: input.KeyF8, sequence: "19~", final: ""}, {code: input.KeyF9, sequence: "20~", final: ""}, {code: input.KeyF10, sequence: "21~", final: ""}, {code: input.KeyF11, sequence: "23~", final: ""}, {code: input.KeyF12, sequence: "24~", final: ""},
	} {
		if state.KeyPressed(key.code) {
			prefix := "\x1b["
			sequence := key.sequence
			if key.final != "" {
				if modifier > 1 {
					sequence = "1;" + strconv.Itoa(modifier) + key.final
				} else if s.ApplicationCursor || key.code >= input.KeyF1 && key.code <= input.KeyF4 {
					prefix = "\x1bO"
				}
			} else if modifier > 1 {
				sequence = strings.TrimSuffix(sequence, "~") + ";" + strconv.Itoa(modifier) + "~"
			}
			m.writeTerminal(item, []byte(prefix+sequence))
			return true
		}
	}
	if state.KeyPressed(input.KeyEscape) && !state.Composing() {
		m.writeTerminal(item, []byte{27})
		return true
	}
	if state.KeyPressed(input.KeyTab) && !state.Composing() {
		value := "\t"
		if shift {
			value = "\x1b[Z"
		}
		m.writeTerminal(item, []byte(value))
		return true
	}
	v := &m.terminalView
	v.edits = state.AppendTextEdits(v.edits[:0])
	for _, edit := range v.edits {
		if edit.DuringComposition {
			continue
		}
		value := edit.Text
		switch edit.Command {
		case input.TextCommandBackspace:
			value = "\x7f"
		case input.TextCommandEnter:
			value = "\r"
		}
		if value != "" {
			if alt {
				value = "\x1b" + value
			}
			m.writeTerminal(item, []byte(strings.ReplaceAll(value, "\n", "\r")))
		}
	}
	return true
}
func (m *workspaceModel) terminalShortcut(world *module.Content, state *input.State) (bool, bool) {
	if m.terminalView.context.open {
		return true, m.terminalContextKeys(world, state)
	}
	if m.settingsOpen || m.quickOpen || m.findOpen || m.closing.action != pencodeCloseNone || m.market != nil && m.market.open {
		return false, false
	}
	if m.commandMatches("terminal-new", state, runtime.GOOS) {
		m.newTerminal()
		return true, true
	}
	if m.commandMatches("terminal-toggle", state, runtime.GOOS) {
		m.terminalEvent("status-terminal")
		return true, true
	}
	if m.terminalFocused(world) {
		return true, m.handleActionShortcuts(world, state, runtime.GOOS)
	}
	return false, false
}
func (m *workspaceModel) updateTerminalPointer(world *module.Content, state *input.State) bool {
	m.terminalView.context.keysHandled = false
	if m.bottom.active != pencodePanelTerminal || m.screen != pencodeWorkspace || m.terminals == nil || m.terminals.active == nil || m.settingsOpen || m.quickOpen || m.findOpen || m.closing.action != pencodeCloseNone || m.market != nil && m.market.open {
		c := &m.terminalView.context
		changed := c.open
		c.open, c.selection = false, ""
		return changed
	}
	p := state.Pointer()
	zoom := float32(m.settings.Zoom) / 100
	if zoom <= 0 {
		zoom = 1
	}
	x, y := p.X/zoom, p.Y/zoom
	if m.updateTerminalContext(world, state, x, y) {
		return true
	}
	if m.terminalView.context.open {
		return false
	}
	if !m.pointerInsideRefs(world, x, y, []string{"terminal-content"}) {
		return false
	}
	item := m.terminals.active
	s := item.screen
	shift := state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)
	if s.MouseTracking != 0 && !shift {
		text := world.Mut[scene.Text2D](m.terminalView.text)
		node, _ := world.HTMLNode(m.surface, "terminal-content")
		bounds, _ := node.Bounds()
		width, height, ok := scene.MeasureTextValue2D(text, "M")
		if !ok || width <= 0 || height <= 0 {
			return false
		}
		link, _ := world.Get[htmlui.ControlText](m.terminalView.text)
		col := max(1, min(s.Cols, int((x-float32(bounds.X)-link.Content.X)/width)+1))
		row := max(1, min(s.Rows, int((y-float32(bounds.Y)-link.Content.Y)/height)+1))
		code := -1
		release := false
		for i, button := range [...]input.MouseButton{input.MouseLeft, input.MouseMiddle, input.MouseRight} {
			if state.MousePressed(button) {
				code = i
			}
			if state.MouseReleased(button) {
				code = i
				release = true
			}
		}
		if code < 0 && (p.DeltaX != 0 || p.DeltaY != 0) && (s.MouseTracking == 1003 || s.MouseTracking == 1002 && p.Down) {
			code = 35
			for i, button := range [...]input.MouseButton{input.MouseLeft, input.MouseMiddle, input.MouseRight} {
				if state.MouseHeld(button) {
					code = 32 + i
					break
				}
			}
		}
		if p.WheelY != 0 {
			code = 64
			if p.WheelY < 0 {
				code = 65
			}
			state.ConsumePointerWheel()
		}
		if code >= 0 {
			if state.KeyHeld(input.KeyLeftAlt) || state.KeyHeld(input.KeyRightAlt) {
				code += 8
			}
			if state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl) {
				code += 16
			}
			var buffer [48]byte
			data := buffer[:0]
			if s.MouseSGR {
				data = append(data, "\x1b[<"...)
				data = strconv.AppendInt(data, int64(code), 10)
				data = append(data, ';')
				data = strconv.AppendInt(data, int64(col), 10)
				data = append(data, ';')
				data = strconv.AppendInt(data, int64(row), 10)
				if release {
					data = append(data, 'm')
				} else {
					data = append(data, 'M')
				}
			} else {
				if release {
					code = 3
				}
				data = append(data, 27, '[', 'M', byte(code+32), byte(min(223, col)+32), byte(min(223, row)+32))
			}
			m.writeTerminal(item, data)
		}
		return false
	}
	if p.WheelY == 0 {
		return false
	}
	if s.Alternate() {
		sequence := "\x1b[A"
		if p.WheelY < 0 {
			sequence = "\x1b[B"
		}
		if s.ApplicationCursor {
			sequence = strings.Replace(sequence, "[", "O", 1)
		}
		for range 3 {
			m.writeTerminal(item, []byte(sequence))
		}
	} else {
		delta := int(p.WheelY * 3)
		if delta == 0 {
			delta = 1
			if p.WheelY < 0 {
				delta = -1
			}
		}
		item.scroll = max(0, min(len(s.History)/s.Cols, item.scroll+delta))
	}
	state.ConsumePointerWheel()
	return true
}
