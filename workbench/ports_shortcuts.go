package workbench

import (
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"runtime"
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
		case m.uiKey(state, input.KeyEscape):
			m.portEvent("ports-cancel", "")
		case m.uiKey(state, input.KeyEnter):
			m.commitPortEdit()
		default:
			return true, false
		}
		return true, true
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
	case m.uiKey(state, input.KeyUp), m.uiKey(state, input.KeyDown):
		index := 0
		for i := range u.rows {
			if u.rows[i].key == u.selected {
				index = i
				break
			}
		}
		if m.uiKey(state, input.KeyUp) {
			index = max(0, index-1)
		} else {
			index = min(len(u.rows)-1, index+1)
		}
		u.selected = u.rows[index].key
		if control, ok := world.HTMLControl(m.surface, portRowRef(u.rows[index])); ok {
			world.Focus(control)
		}
	case m.commandMatches("ports-rename", state, runtime.GOOS):
		m.editPort("label", *row)
	case m.commandMatches("ports-stop", state, runtime.GOOS), m.commandMatches("ports-stop-alternate", state, runtime.GOOS):
		m.sendPortCommand(pencodePortCommand{op: "stop", key: row.key})
	case m.commandMatches("ports-open", state, runtime.GOOS):
		m.sendPortCommand(pencodePortCommand{op: "open", key: row.key})
	case m.commandMatches("ports-copy", state, runtime.GOOS):
		m.copyPortAddress(world, *row)
	default:
		return false, false
	}
	return true, true
}
