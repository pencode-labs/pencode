package workbench

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/module"
	engineui "github.com/onix-engine/onix/ui"

	"github.com/pencode-labs/pencode/debugger"
	"github.com/pencode-labs/pencode/peb"
)

const pencodeDebugConfigPath = ".pencode/debug.json"

type pencodeDebugTemplate struct {
	owner, label, content string
}

var builtinDebugTemplates = appendDebugTemplates(nil, "", "", debugger.BuiltinTemplates)

func appendDebugTemplates(dst []pencodeDebugTemplate, owner, adapter, templates string) []pencodeDebugTemplate {
	if templates == "" {
		return dst
	}
	configs, err := debugger.ParseConfigs([]byte(templates), "${workspaceFolder}")
	if err != nil {
		return dst
	}
	var raw []json.RawMessage
	if json.Unmarshal([]byte(templates), &raw) != nil {
		return dst
	}
	for i, c := range configs {
		if adapter == "" || c.Adapter == adapter {
			dst = append(dst, pencodeDebugTemplate{owner, c.Label, string(raw[i])})
		}
	}
	return dst
}

func (m *workspaceModel) importDebugTemplates(id string, adapters []peb.DebugAdapter) {
	owner := "extensions/" + id + "/"
	m.removeDebugTemplates(owner)
	for _, a := range adapters {
		m.extensionDebugTemplates = appendDebugTemplates(m.extensionDebugTemplates, owner, a.ID, a.Templates)
	}
	slices.SortStableFunc(m.extensionDebugTemplates, func(a, b pencodeDebugTemplate) int {
		if n := strings.Compare(a.owner, b.owner); n != 0 {
			return n
		}
		return strings.Compare(a.label, b.label)
	})
	m.rebuildDebugTemplateRows()
}

func (m *workspaceModel) removeDebugTemplates(prefix string) {
	m.extensionDebugTemplates = slices.DeleteFunc(m.extensionDebugTemplates, func(t pencodeDebugTemplate) bool { return strings.HasPrefix(t.owner, prefix) })
	m.debug.adding = false
	m.rebuildDebugTemplateRows()
}

func (m *workspaceModel) debugTemplate(index int) pencodeDebugTemplate {
	if index < len(builtinDebugTemplates) {
		return builtinDebugTemplates[index]
	}
	return m.extensionDebugTemplates[index-len(builtinDebugTemplates)]
}

func (m *workspaceModel) rebuildDebugTemplateRows() {
	clear(m.debugTemplateRows)
	m.debugTemplateRows = m.debugTemplateRows[:0]
	for i := range len(builtinDebugTemplates) + len(m.extensionDebugTemplates) {
		m.debugTemplateRows = append(m.debugTemplateRows, pencodeDebugRow{ref: "debug-template:" + strconv.Itoa(i), slot: "debug-template-menu", label: m.debugTemplate(i).label})
	}
	m.debugTemplateRows = append(m.debugTemplateRows, pencodeDebugRow{ref: "debug-template:edit", slot: "debug-template-menu", label: m.tr("手动编辑配置…")})
}

func (m *workspaceModel) toggleDebugTemplateMenu(world *module.Content) {
	u := &m.debug
	u.adding = !u.adding
	if !u.adding {
		return
	}
	if len(m.debugTemplateRows) == 0 {
		m.rebuildDebugTemplateRows()
	}
	u.templateIndex = 0
	if world == nil {
		return
	}
	if entity, ok := world.HTMLControl(m.surface, "debug-add"); ok && !world.Has[engineui.TextFieldKeyHandler](entity) {
		_ = world.Insert(entity, engineui.TextFieldKeyHandler{Handle: m.menuActivationHandled})
	}
	root, rootOK := world.HTMLNode(m.surface, "pencode-root")
	anchor, anchorOK := world.HTMLNode(m.surface, "debug-add")
	if !rootOK || !anchorOK {
		return
	}
	area, areaOK := root.Bounds()
	box, boxOK := anchor.Bounds()
	if !areaOK || !boxOK {
		return
	}
	const width, gap, margin = float32(240), float32(4), float32(8)
	height := float32(len(m.debugTemplateRows)*28 + 6)
	x := min(max(box.X-area.X, margin), max(margin, area.Width-width-margin))
	y := box.Y + box.Height + gap - area.Y
	if y+height > area.Height-margin {
		y = max(margin, box.Y-area.Y-height-gap)
	}
	u.templateStyle = languagePopupStyle(x, y)
}

func (m *workspaceModel) chooseDebugTemplate(value string) {
	if !m.debug.adding {
		return
	}
	if value == "edit" {
		m.debug.adding = false
		m.readDebugConfigs(true, "")
	} else if index, err := strconv.Atoi(value); err == nil {
		m.addDebugTemplate(index)
	}
}

func (m *workspaceModel) updateDebugTemplateMenu(world *module.Content, state *input.State) (handled, changed bool) {
	u := &m.debug
	if !u.adding {
		return false, false
	}
	if m.bottom.active != pencodePanelDebug || m.settingsOpen || m.quickOpen || m.fileContext.dialog != "" || m.closing.action != pencodeCloseNone || m.market != nil && m.market.open || !m.backendConnected() || u.session != nil && u.view.State < debugger.Ended {
		u.adding = false
		return false, true
	}
	p := state.Pointer()
	zoom := max(float32(m.settings.Zoom)/100, .01)
	x, y := p.X/zoom, p.Y/zoom
	if state.PointerPressed() || state.MousePressed(input.MouseRight) || state.MousePressed(input.MouseMiddle) {
		if !m.pointerInsideRefs(world, x, y, []string{"debug-add", "debug-template-menu"}) {
			u.adding = false
			return false, true
		}
	}
	if p.DeltaX != 0 || p.DeltaY != 0 {
		for i, row := range m.debugTemplateRows {
			if i != u.templateIndex && m.pointerInsideRefs(world, x, y, []string{row.ref}) {
				u.templateIndex, changed = i, true
				break
			}
		}
	}
	m.languageUI.keysHandled = true
	switch {
	case m.uiKey(state, input.KeyEscape):
		u.adding = false
		changed = true
	case m.uiKey(state, input.KeyDown), m.uiKey(state, input.KeyUp), m.uiKey(state, input.KeyTab):
		step := 1
		if m.uiKey(state, input.KeyUp) || (state.KeyHeld(input.KeyLeftShift) || state.KeyHeld(input.KeyRightShift)) {
			step = -1
		}
		u.templateIndex = (u.templateIndex + step + len(m.debugTemplateRows)) % len(m.debugTemplateRows)
		changed = true
	case m.uiKey(state, input.KeyEnter), m.uiKey(state, input.KeySpace):
		m.debugEvent(world, m.debugTemplateRows[u.templateIndex].ref, "")
		changed = true
	}
	return true, changed
}

// Keep raw entries and placeholders intact; resolving workspace paths belongs to
// launching the configuration, never to generating its editable source file.
func appendDebugTemplate(data []byte, template string) (string, error) {
	var configs []json.RawMessage
	if data != nil {
		if _, err := debugger.ParseConfigs(data, "${workspaceFolder}"); err != nil {
			return "", err
		}
		if err := json.Unmarshal(data, &configs); err != nil {
			return "", err
		}
	}
	configs = append(configs, json.RawMessage(template))
	content, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return "", err
	}
	return string(append(content, '\n')), nil
}

func (m *workspaceModel) addDebugTemplate(index int) {
	if index < 0 || index >= len(builtinDebugTemplates)+len(m.extensionDebugTemplates) || m.debug.loading || !m.backendConnected() || m.fileIOMutating(m.activeProjectState()) || m.debug.session != nil && m.debug.view.State < debugger.Ended {
		return
	}
	p := m.activeProjectState()
	if p == nil {
		return
	}
	m.debug.adding = false
	m.debug.startPending = false
	template := m.debugTemplate(index).content
	if f := p.Files[pencodeDebugConfigPath]; f != nil {
		if err := m.requestFileOpen(p, pencodeDebugConfigPath, false, true, nil); err != nil {
			m.debug.message = err.Error()
			return
		}
		if f.Dirty {
			m.debug.message = m.tr("请先保存调试配置，再添加启动器")
			return
		}
		content, err := appendDebugTemplate([]byte(f.content()), template)
		if err == nil {
			m.updateActiveContent(content)
			_, err = m.requestFileSave(p, f)
		}
		if err != nil {
			m.debug.message = err.Error()
		} else {
			m.debug.message = ""
		}
		return
	}
	m.readDebugConfigs(true, template)
}
