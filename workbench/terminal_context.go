package workbench

import (
	"runtime"
	"strings"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
)

type pencodeTerminalContext struct {
	open, keysHandled bool
	x, y              float32
	index             int
	selection         string
}

var pencodeTerminalContextRefs = [...]string{"terminal-copy", "terminal-paste", "terminal-clear"}

func (m *workspaceModel) terminalSelection() string {
	if field, ok := module.ContentGet[engineui.TextField](m.fontResources, m.terminalView.control); ok {
		start, end := pencodeSelectionBytes(field.Value, min(field.Caret, field.Anchor), max(field.Caret, field.Anchor))
		return field.Value[start:end]
	}
	return ""
}

func (m *workspaceModel) terminalAction(state *input.State, ref, selection string) {
	if m.terminals == nil || m.terminals.active == nil {
		return
	}
	item := m.terminals.active
	switch ref {
	case "terminal-copy":
		if selection != "" {
			state.SetClipboardText(selection)
		}
	case "terminal-paste":
		if value, ok := state.ClipboardText(); ok && value != "" {
			value = strings.ReplaceAll(value, "\r\n", "\n")
			value = strings.ReplaceAll(value, "\r", "\n")
			value = strings.ReplaceAll(value, "\x1b", "")
			if item.screen.BracketedPaste {
				value = "\x1b[200~" + value + "\x1b[201~"
			}
			m.writeTerminal(item, []byte(value))
		}
	case "terminal-clear":
		item.screen.Clear()
		item.scroll = 0
		markPencodeRootDirtyByModel(m.fontResources, m)
	}
}

func (m *workspaceModel) renderTerminalContext(view *htmlui.View, visible bool) {
	c := &m.terminalView.context
	if !visible || m.terminals == nil || m.terminals.active == nil {
		c.open = false
	}
	if !c.open {
		view.Attr("terminal-context", "hidden", "")
		return
	}
	for _, ref := range pencodeTerminalContextRefs {
		view.Text(ref+"-shortcut", m.commandLabel(ref))
	}
	view.RemoveAttr("terminal-context", "hidden")
	view.Attr("terminal-context", "style", languagePopupStyle(c.x, c.y))
	for i, ref := range pencodeTerminalContextRefs {
		view.Event(ref, htmlui.EventClick)
		class := ""
		if i == c.index {
			class = "active"
		}
		view.Attr(ref, "class", class)
		setContextMenuEnabled(view, ref, i != 0 || c.selection != "")
	}
}

func (m *workspaceModel) handleTerminalContextEvent(world *module.Content, ref string) bool {
	for _, candidate := range pencodeTerminalContextRefs {
		if ref != candidate {
			continue
		}
		c := &m.terminalView.context
		if c.open {
			if state, ok := module.ContentResource[*input.State](world); ok {
				m.terminalAction(state, ref, c.selection)
			}
			c.open, c.selection = false, ""
			m.terminals.focus = true
		}
		return true
	}
	return false
}

func (m *workspaceModel) updateTerminalContext(world *module.Content, state *input.State, x, y float32) bool {
	c := &m.terminalView.context
	inside := m.pointerInsideRefs(world, x, y, []string{"terminal-context"})
	if c.open && inside {
		p := state.Pointer()
		if p.DeltaX != 0 || p.DeltaY != 0 {
			for i, ref := range pencodeTerminalContextRefs {
				if i != c.index && m.pointerInsideRefs(world, x, y, []string{ref}) {
					c.index = i
					return true
				}
			}
		}
		return false
	}
	changed := false
	if c.open && (state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle)) {
		c.open, c.selection, changed = false, "", true
	}
	if !state.MousePressed(input.MouseRight) || !m.pointerInsideRefs(world, x, y, []string{"terminal-content"}) {
		return changed
	}
	root, ok := world.HTMLNode(m.surface, "pencode-root")
	if !ok {
		return changed
	}
	bounds, _ := root.Bounds()
	c.x, c.y = clampLanguagePopup(x-bounds.X, y-bounds.Y, bounds, 240, 90)
	c.open, c.index, c.selection = true, 0, m.terminalSelection()
	if c.selection == "" {
		c.index = 1
	}
	world.Focus(m.terminalView.control)
	return true
}

func (m *workspaceModel) terminalContextKeys(world *module.Content, state *input.State) bool {
	c := &m.terminalView.context
	c.keysHandled = true
	switch {
	case m.commandMatches("terminal-copy", state, runtime.GOOS):
		m.handleTerminalContextEvent(world, "terminal-copy")
	case m.commandMatches("terminal-paste", state, runtime.GOOS), m.commandMatches("terminal-paste-alternate", state, runtime.GOOS):
		m.handleTerminalContextEvent(world, "terminal-paste")
	case m.commandMatches("terminal-clear", state, runtime.GOOS):
		m.handleTerminalContextEvent(world, "terminal-clear")
	case m.uiKey(state, input.KeyEscape):
		c.open, c.selection = false, ""
		m.terminals.focus = true
	case m.uiKey(state, input.KeyDown), m.uiKey(state, input.KeyUp), m.uiKey(state, input.KeyTab):
		step := 1
		if m.uiKey(state, input.KeyUp) || m.commandMatches("ui-focus-previous", state, runtime.GOOS) {
			step = -1
		}
		for range len(pencodeTerminalContextRefs) {
			c.index = (c.index + step + len(pencodeTerminalContextRefs)) % len(pencodeTerminalContextRefs)
			if c.index != 0 || c.selection != "" {
				break
			}
		}
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		m.handleTerminalContextEvent(world, pencodeTerminalContextRefs[c.index])
	default:
		return false
	}
	return true
}
