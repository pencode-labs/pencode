package workbench

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/terminal"
)

type terminalTabBinding struct {
	handler htmlui.ClickHandler
	node    htmlui.Node
}

type pencodeTerminal struct {
	bindings            [2]terminalTabBinding
	err                 error
	session             *terminal.Session
	screen              *terminal.Screen
	id, root, label     string
	selectRef, closeRef string
	selection           terminalTextSelection
	scroll              int
	ready, exited       bool
}
type pencodeTerminals struct {
	active         *pencodeTerminal
	items          []*pencodeTerminal
	nextID         uint64
	focus, visible bool
}

func (m *workspaceModel) terminalSlot(project *pencodeProject) **pencodeTerminals {
	if project != nil {
		return &project.terminals
	}
	return &m.standaloneTerminals
}

func (m *workspaceModel) terminalState() *pencodeTerminals {
	if m.terminals != nil {
		return m.terminals
	}
	return m.ensureTerminalGroup(m.activeProjectState())
}

func (m *workspaceModel) ensureTerminalGroup(project *pencodeProject) *pencodeTerminals {
	slot := m.terminalSlot(project)
	if *slot == nil {
		*slot = &pencodeTerminals{}
	}
	if project == m.activeProjectState() {
		m.terminals = *slot
	}
	return *slot
}

func (m *workspaceModel) switchTerminalProject(project *pencodeProject) {
	visible := m.bottom.active == pencodePanelTerminal
	if t := m.terminals; t != nil {
		t.visible, t.focus = visible, false
		if t.active != nil {
			t.active.selection.dragging = false
		}
	}
	m.terminals = *m.terminalSlot(project)
	v := &m.terminalView
	v.context = pencodeTerminalContext{}
	v.ime = terminalIMEAnchor{}
	v.keys = v.keys[:0]
	v.repeatKey = 0
	v.tabScroll = pencodeTabScroll{}
	if t := m.terminals; t != nil && t.visible && t.active != nil {
		m.bottom.active = pencodePanelTerminal
		t.focus = true
	} else if visible {
		m.bottom.active = pencodePanelClosed
		if m.fontResources != nil {
			m.fontResources.Focus(m.editor.control)
		}
	}
}

func (m *workspaceModel) terminalGroups(yield func(*pencodeProject, *pencodeTerminals) bool) {
	active := m.activeProjectState()
	if m.terminals != nil && !yield(active, m.terminals) {
		return
	}
	if t := m.standaloneTerminals; active != nil && t != nil && !yield(nil, t) {
		return
	}
	for _, project := range m.projects {
		if t := project.terminals; project != active && t != nil && !yield(project, t) {
			return
		}
	}
}

func (m *workspaceModel) addTerminal(project *pencodeProject, root, label string, session *terminal.Session) *pencodeTerminal {
	t := m.ensureTerminalGroup(project)
	t.nextID++
	m.nextTerminalID++
	id := strconv.FormatUint(m.nextTerminalID, 10)
	if label == "" {
		label = t.nextLabel()
	}
	item := &pencodeTerminal{id: id, root: root, label: label, selectRef: "terminal-tab:" + id, closeRef: "terminal-close:" + id, screen: terminal.NewScreen(80, 12), session: session}
	t.items = append(t.items, item)
	t.active = item
	return item
}

func (m *workspaceModel) newTerminal() *pencodeTerminal {
	return m.openTerminal("")
}

func (t *pencodeTerminals) nextLabel() string {
	for number := 1; ; number++ {
		suffix := strconv.Itoa(number)
		if !slices.ContainsFunc(t.items, func(item *pencodeTerminal) bool {
			return item.label == "Terminal "+suffix
		}) {
			return "Terminal " + suffix
		}
	}
}

func (m *workspaceModel) openTerminal(cwd string) *pencodeTerminal {
	if !m.backendConnected() {
		m.setActiveStatus(m.tr("工作区服务未连接，请等待连接恢复"))
		return nil
	}
	m.terminalView.context.open = false
	root := m.projectPath
	if root == "" {
		root = defaultDirectory()
		if m.session != nil && m.session.connection != nil && m.session.connection.workspaceProfile.Remote() {
			root = "~"
		}
	}
	backend := m.backend
	session := terminal.Attach(80, 12, m.backgroundWake(), func() (terminal.Process, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workspace, err := backend.OpenWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		return backend.OpenTerminal(ctx, workspace.Path, cwd, 80, 12)
	})
	item := m.addTerminal(m.activeProjectState(), root, "", session)
	m.terminals.focus = true
	m.bottom.active = pencodePanelTerminal
	return item
}
func (t *pencodeTerminals) close() {
	if t == nil {
		return
	}
	for _, item := range t.items {
		item.session.Close()
	}
	clear(t.items)
	t.items, t.active = t.items[:0], nil
	t.focus, t.visible = false, false
}

func (m *workspaceModel) closeTerminal(root string) {
	if root == "" {
		for _, t := range m.terminalGroups {
			t.close()
		}
	} else if index := m.projectIndex(root); index >= 0 {
		m.projects[index].terminals.close()
	}
	m.closeEmptyTerminalPanel()
}

func (m *workspaceModel) runExtensionTerminalCommand(command, cwd string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	if item := m.openTerminal(cwd); item != nil {
		m.writeTerminal(item, []byte(command+"\n"))
	}
}

func (m *workspaceModel) openExtensionSettings(id string) {
	m.ensureSettings()
	m.settingsOpen = true
	m.settingsShortcuts = false
	m.settingsAbout, m.settingsLanguageServers = false, false
	m.settingsExtensions = true
	m.settingsExtensionsExpanded = true
	m.settingsExtension = id
	m.settingsFeedback = ""
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
	if m.terminals == nil || len(m.terminals.items) != 0 || m.bottom.active != pencodePanelTerminal {
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
		if m.terminals == nil {
			return true
		}
		for i, item := range m.terminals.items {
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
	changed := false
	var buffer [32768]byte
	for project, t := range m.terminalGroups {
		updated := m.pollTerminalGroup(project, t, buffer[:])
		changed = changed || t == m.terminals && updated
	}
	return m.closeEmptyTerminalPanel() || changed && m.bottom.active == pencodePanelTerminal
}

func (m *workspaceModel) pollTerminalGroup(project *pencodeProject, t *pencodeTerminals, buffer []byte) bool {
	changed := false
	kept := t.items[:0]
	nextActive := 0
	for _, item := range t.items {
		// A retired connection leaves a read-only screen until its tab is closed.
		if item.exited {
			kept = append(kept, item)
			continue
		}
		if n := item.session.ReadOutput(buffer); n > 0 {
			oldHistory := item.screen.Scrolled
			item.screen.Feed(buffer[:n])
			if item.scroll > 0 {
				item.scroll += int(item.screen.Scrolled - oldHistory)
			}
			if reply := item.screen.Replies(); len(reply) > 0 {
				m.writeProjectTerminal(project, item, reply)
			}
			changed = changed || item == t.active
		}
		ready, exited, err := item.session.Status()
		if exited {
			if err != nil {
				m.setProjectStatus(project, err.Error())
			}
			item.session.Close()
			if t.active == item {
				t.active = nil
				nextActive = len(kept)
				if t == m.terminals {
					m.terminalView.context.open = false
				}
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
	if len(kept) == 0 {
		t.visible = false
	}
	return changed
}
func (m *workspaceModel) writeTerminal(item *pencodeTerminal, data []byte) {
	m.writeProjectTerminal(m.activeProjectState(), item, data)
}

func (m *workspaceModel) writeProjectTerminal(project *pencodeProject, item *pencodeTerminal, data []byte) {
	if item.exited {
		return
	}
	if _, err := item.session.Write(data); err != nil {
		m.setProjectStatus(project, err.Error())
	}
	item.scroll = 0
}
