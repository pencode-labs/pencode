package workbench

import (
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
)

func (m *workspaceModel) portsShortcut(world *module.Content, state *input.State) (bool, bool) {
	u := &m.portUI
	if world == nil || m.bottom.active != pencodePanelPorts || m.settingsOpen || m.quickOpen || m.findOpen || m.market != nil && m.market.open {
		return false, false
	}
	if u.mode != "" {
		ref := "ports-input"
		if u.mode != "new" {
			ref = "ports-" + u.mode + "-input:" + u.editKey
		}
		control, ok := world.HTMLControl(m.surface, ref)
		if !ok || world.FocusedControl() != control {
			return false, false
		}
		switch {
		case state.KeyPressed(input.KeyEscape):
			m.portEvent("ports-cancel", "")
		case state.KeyPressed(input.KeyEnter):
			m.commitPortEdit()
		default:
			return true, false
		}
		return true, true
	}
	if !state.KeyPressed(input.KeyUp) && !state.KeyPressed(input.KeyDown) && !state.KeyPressed(input.KeyDelete) && !state.KeyPressed(input.KeyBackspace) && !state.KeyPressed(input.KeyF2) && !state.KeyPressed(input.KeyEnter) && !state.KeyPressed(input.KeyC) {
		return false, false
	}
	row := m.portRow(u.selected)
	if row == nil {
		return false, false
	}
	control, ok := world.HTMLControl(m.surface, portRowRef(*row))
	if !ok || world.FocusedControl() != control {
		return false, false
	}
	switch {
	case state.KeyPressed(input.KeyUp), state.KeyPressed(input.KeyDown):
		index := 0
		for i := range u.rows {
			if u.rows[i].key == u.selected {
				index = i
				break
			}
		}
		if state.KeyPressed(input.KeyUp) {
			index = max(0, index-1)
		} else {
			index = min(len(u.rows)-1, index+1)
		}
		u.selected = u.rows[index].key
		if control, ok := world.HTMLControl(m.surface, portRowRef(u.rows[index])); ok {
			world.Focus(control)
		}
	case state.KeyPressed(input.KeyF2):
		m.editPort("label", *row)
	case state.KeyPressed(input.KeyDelete), state.KeyPressed(input.KeyBackspace):
		m.sendPortCommand(pencodePortCommand{op: "stop", key: row.key})
	case state.KeyPressed(input.KeyEnter):
		m.sendPortCommand(pencodePortCommand{op: "open", key: row.key})
	case state.KeyPressed(input.KeyC) && (state.KeyHeld(input.KeyLeftControl) || state.KeyHeld(input.KeyRightControl) || state.KeyHeld(input.KeyLeftSuper) || state.KeyHeld(input.KeyRightSuper)):
		m.copyPortAddress(world, *row)
	default:
		return false, false
	}
	return true, true
}
