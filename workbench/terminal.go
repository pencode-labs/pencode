package workbench

import (
	"context"
	"strconv"
	"strings"
	"time"

	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

type terminalTabBinding struct {
	node    htmlui.Node
	handler htmlui.ClickHandler
}

type pencodeTerminal struct {
	bindings            [2]terminalTabBinding
	id, root, label     string
	selectRef, closeRef string
	session             *terminal.Session
	screen              *terminal.Screen
	scroll              int
	ready, exited       bool
	err                 error
}
type pencodeTerminals struct {
	items  []*pencodeTerminal
	active *pencodeTerminal
	nextID uint64
	focus  bool
}

func (m *workspaceModel) terminalState() *pencodeTerminals {
	if m.terminals == nil {
		m.terminals = &pencodeTerminals{}
	}
	return m.terminals
}
func (m *workspaceModel) newTerminal() {
	if !m.backendConnected() {
		m.setActiveStatus("工作区服务未连接，请等待连接恢复")
		return
	}
	t := m.terminalState()
	m.terminalView.context.open = false
	t.nextID++
	root := m.projectPath
	if root == "" {
		root = defaultDirectory()
		if m.session != nil && m.session.connection != nil && m.session.connection.workspaceProfile.Remote() {
			root = "~"
		}
	}
	id := strconv.FormatUint(t.nextID, 10)
	item := &pencodeTerminal{id: id, root: root, label: "Terminal " + id, selectRef: "terminal-tab:" + id, closeRef: "terminal-close:" + id, screen: terminal.NewScreen(80, 12)}
	backend := m.backend
	item.session = terminal.Attach(80, 12, m.backgroundWake(), func() (terminal.Process, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workspace, err := backend.OpenWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		return backend.OpenTerminal(ctx, workspace.Path, 80, 12)
	})
	t.items = append(t.items, item)
	t.active = item
	t.focus = true
	m.bottom.active = pencodePanelTerminal
}
func (m *workspaceModel) closeTerminal(root string) {
	t := m.terminals
	if t == nil {
		return
	}
	kept := t.items[:0]
	for _, item := range t.items {
		if root == "" || item.root == root {
			item.session.Close()
			if t.active == item {
				t.active = nil
				m.terminalView.context.open = false
			}
		} else {
			kept = append(kept, item)
		}
	}
	clear(t.items[len(kept):])
	t.items = kept
	if t.active == nil && len(kept) > 0 {
		t.active = kept[len(kept)-1]
		t.focus = true
	}
	m.closeEmptyTerminalPanel()
}
func (m *workspaceModel) removeTerminal(index int) {
	t := m.terminals
	item := t.items[index]
	item.session.Close()
	copy(t.items[index:], t.items[index+1:])
	t.items[len(t.items)-1] = nil
	t.items = t.items[:len(t.items)-1]
	if t.active == item {
		t.active = nil
		if len(t.items) > 0 {
			t.active, t.focus = t.items[min(index, len(t.items)-1)], true
		}
		m.terminalView.context.open = false
	}
	m.closeEmptyTerminalPanel()
}

func (m *workspaceModel) closeEmptyTerminalPanel() bool {
	if len(m.terminals.items) != 0 || m.bottom.active != pencodePanelTerminal {
		return false
	}
	m.bottom.active = pencodePanelClosed
	m.terminals.focus = false
	m.terminalView.context = pencodeTerminalContext{}
	m.prepareTerminalView()
	if m.fontResources != nil {
		m.fontResources.Focus(m.editor.control)
	}
	return true
}

func (m *workspaceModel) terminalEvent(ref string) bool {
	switch ref {
	case "status-terminal":
		if m.bottom.active == pencodePanelTerminal {
			m.bottom.active = pencodePanelClosed
			return true
		}
		m.bottom.active = pencodePanelTerminal
		if t := m.terminalState(); t.active == nil {
			m.newTerminal()
		} else {
			t.focus = true
		}
	case "terminal-new":
		m.newTerminal()
	default:
		id, closeTab := strings.CutPrefix(ref, "terminal-close:")
		if !closeTab {
			var ok bool
			id, ok = strings.CutPrefix(ref, "terminal-tab:")
			if !ok {
				return false
			}
		}
		for i, item := range m.terminalState().items {
			if item.id != id {
				continue
			}
			if closeTab {
				m.removeTerminal(i)
			} else {
				m.terminals.active, m.terminals.focus = item, true
				m.terminalView.context.open = false
			}
			break
		}
	}
	return true
}
func (m *workspaceModel) pollTerminals() bool {
	if m.terminals == nil {
		return false
	}
	changed := false
	t := m.terminals
	kept := t.items[:0]
	nextActive := 0
	var buffer [32768]byte
	for _, item := range m.terminals.items {
		// A retired connection leaves a read-only screen until its tab is closed.
		if item.exited {
			kept = append(kept, item)
			continue
		}
		if n := item.session.ReadOutput(buffer[:]); n > 0 {
			oldHistory := item.screen.Scrolled
			item.screen.Feed(buffer[:n])
			if item.scroll > 0 {
				item.scroll += int(item.screen.Scrolled - oldHistory)
			}
			if reply := item.screen.Replies(); len(reply) > 0 {
				m.writeTerminal(item, reply)
			}
			changed = changed || item == m.terminals.active
		}
		ready, exited, err := item.session.Status()
		if exited {
			if err != nil {
				m.setActiveStatus(err.Error())
			}
			item.session.Close()
			if t.active == item {
				t.active = nil
				nextActive = len(kept)
				m.terminalView.context.open = false
			}
			changed = true
			continue
		}
		kept = append(kept, item)
		if ready != item.ready || exited != item.exited || err != item.err {
			item.ready, item.exited, item.err = ready, exited, err
			changed = true
		}
	}
	clear(t.items[len(kept):])
	t.items = kept
	if t.active == nil && len(kept) > 0 {
		t.active, t.focus = kept[min(nextActive, len(kept)-1)], true
	}
	return m.closeEmptyTerminalPanel() || changed && m.bottom.active == pencodePanelTerminal
}
func (m *workspaceModel) writeTerminal(item *pencodeTerminal, data []byte) {
	if item.exited {
		return
	}
	if _, err := item.session.Write(data); err != nil {
		m.setActiveStatus(err.Error())
	}
	item.scroll = 0
}
