package workbench

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	engineui "github.com/diiyw/onix/ui"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/remote"
)

type pencodePortsIdentity struct {
	target    remote.Target
	directory string
}
type pencodePortUI struct {
	owner                                     pencodePortsIdentity
	owned, resumeReady                        bool
	resume                                    pencodePortsState
	rows                                      []pencodePortRow
	revision, nextCommand, pending, completed uint64
	selected, message                         string
	failed                                    bool
	mode, editKey, value                      string
	focus                                     bool
}
type pencodePortProps struct {
	Model          *workspaceModel
	Row            pencodePortRow
	Selected       bool
	Editing, Value string
	Pending        bool
	Theme          *pencodeTheme
}

func newPortDefinition(content *module.Content) (*htmlui.Definition[pencodePortProps, struct{}], error) {
	return module.ContentDefine[pencodePortProps, struct{}](content, htmlui.NewTemplate(`<div data-ref="row" class="pencode-port-row" role="row">
  <div class="pencode-port-cell pencode-port-number" role="cell">
    <span data-ref="dot" class="pencode-port-dot"></span>
    <button data-ref="select" class="pencode-port-name" type="button"></button>
    <input data-ref="label-input" class="pencode-port-input" type="text" aria-label="端口标签" autocomplete="off" spellcheck="false" hidden>
    <span class="pencode-port-row-actions"><button data-ref="label" class="pencode-icon-button" type="button" title="设置端口标签 (F2)" aria-label="设置端口标签"><img data-ref="label-icon" alt=""></button></span>
  </div>
  <div class="pencode-port-cell pencode-port-address" role="cell">
    <button data-ref="address" class="pencode-port-link" type="button"></button>
    <input data-ref="local-input" class="pencode-port-input" type="text" aria-label="本地端口" autocomplete="off" spellcheck="false" hidden>
    <span class="pencode-port-row-actions">
      <button data-ref="local" class="pencode-icon-button" type="button" title="更改本地端口" aria-label="更改本地端口"><img data-ref="local-icon" alt=""></button>
      <button data-ref="open" class="pencode-icon-button" type="button" title="在浏览器中打开" aria-label="在浏览器中打开"><img data-ref="open-icon" alt=""></button>
      <button data-ref="copy" class="pencode-icon-button" type="button" title="复制本地地址" aria-label="复制本地地址"><img data-ref="copy-icon" alt=""></button>
    </span>
  </div>
  <div class="pencode-port-cell pencode-port-protocol" role="cell"><button data-ref="protocol" class="pencode-port-toggle" type="button" aria-label="切换端口协议"></button></div>
  <span data-ref="process" class="pencode-port-cell pencode-port-process" role="cell"></span>
  <div class="pencode-port-cell pencode-port-visibility" role="cell"><button data-ref="visibility" class="pencode-port-toggle" type="button" title="切换本机 / 局域网可见性" aria-label="切换端口可见性"></button></div>
  <div class="pencode-port-cell pencode-port-origin" role="cell">
    <span data-ref="source" class="pencode-port-source"></span>
    <span class="pencode-port-row-actions"><button data-ref="stop" class="pencode-icon-button" type="button" title="停止转发端口 (Delete)" aria-label="停止转发端口"><img data-ref="stop-icon" alt=""></button></span>
  </div>
</div>`), "", module.ContentBehavior[pencodePortProps, struct{}]{Render: renderPortRow, HandleEvent: handlePortRowEvent})
}

func renderPortRow(c module.ContentRenderContext[pencodePortProps, struct{}], view *htmlui.View) {
	p, row := c.Props, c.Props.Row
	ref := portRowRef(row)
	class := "pencode-port-row"
	if p.Selected {
		class += " selected"
	}
	if row.failed {
		class += " failed"
	}
	view.Attr("row", "class", class)
	view.Attr("row", "id", ref+"-row")
	view.Attr("row", "aria-selected", boolAttribute(p.Selected))
	view.Attr("row", "title", row.status)
	view.Text("select", row.remote)
	view.Text("address", row.local)
	view.Attr("address", "title", row.url)
	view.Text("process", row.process)
	view.Attr("process", "title", row.process)
	protocol, hint := "HTTP", "点击切换为 HTTPS"
	if row.protocol == "https" {
		protocol, hint = "HTTPS", "点击切换为 HTTP"
	}
	view.Text("protocol", protocol)
	view.Attr("protocol", "title", hint)
	view.Text("visibility", row.visibility)
	view.Text("source", row.source)
	view.Attr("source", "id", "ports-source:"+row.key)
	view.Attr("source", "title", row.source)
	view.Attr("select", "id", ref)
	view.Attr("select", "title", row.key)
	for _, action := range [...]string{"open", "copy", "stop", "label", "local", "protocol", "visibility", "address"} {
		view.Attr(action, "id", "ports-"+action+":"+row.key)
		view.Event(action, htmlui.EventClick)
	}
	view.Event("select", htmlui.EventClick)
	for _, pair := range [...][2]string{{"label-icon", "edit"}, {"local-icon", "edit"}, {"open-icon", "globe"}, {"copy-icon", "copy"}, {"stop-icon", "close"}} {
		view.Attr(pair[0], "src", p.Model.themedIcon("icons/"+pair[1]+".svg", "icon.foreground"))
	}
	for _, mode := range [...]string{"label", "local"} {
		editing := p.Editing == mode
		field := mode + "-input"
		setPanelVisible(view, field, editing)
		view.Attr(field, "id", "ports-"+mode+"-input:"+row.key)
		if editing {
			placeholder := row.remote
			if mode == "local" {
				_, placeholder, _ = strings.Cut(row.local, ":")
			}
			view.Attr(field, "placeholder", placeholder)
			view.Attr(field, "value", p.Value)
			view.Event(field, htmlui.EventInput)
			setContextMenuEnabled(view, field, !p.Pending)
		}
	}
	setPanelVisible(view, "select", p.Editing != "label")
	setPanelVisible(view, "address", p.Editing != "local")
	setContextMenuEnabled(view, "open", row.url != "")
	setContextMenuEnabled(view, "copy", row.url != "")
	setContextMenuEnabled(view, "protocol", !p.Pending)
	setContextMenuEnabled(view, "visibility", !p.Pending)
}

func portRowRef(row pencodePortRow) string { return "ports-row:" + row.key }

func handlePortRowEvent(c *module.ContentEventContext[pencodePortProps, struct{}]) {
	m, row := c.Props.Model, c.Props.Row
	u := &m.portUI
	u.selected = row.key
	switch c.Ref {
	case "label-input", "local-input":
		u.value = c.Event.Value
	case "select":
	case "protocol":
		protocol := "https"
		if row.protocol == "https" {
			protocol = "http"
		}
		m.sendPortCommand(pencodePortCommand{op: "protocol", key: row.key, value: protocol})
	case "visibility":
		m.sendPortCommand(pencodePortCommand{op: "visibility", key: row.key})
	case "address", "open":
		m.sendPortCommand(pencodePortCommand{op: "open", key: row.key})
	case "copy":
		m.copyPortAddress(c.World, row)
	case "label", "local":
		m.editPort(c.Ref, row)
	case "stop":
		m.sendPortCommand(pencodePortCommand{op: "stop", key: row.key})
	}
	markPencodeRootDirtyByModel(c.World, m)
}

func (m *workspaceModel) remoteWorkspace() bool {
	return m.session != nil && m.session.connection != nil && m.session.connection.workspaceProfile.Remote()
}

func (m *workspaceModel) closePorts() {
	if m.ports != nil {
		m.ports.cancel()
		<-m.ports.done
		m.ports.mu.Lock()
		m.portUI.resume = m.ports.state
		m.ports.mu.Unlock()
		m.portUI.resumeReady = true
		m.ports = nil
	}
	m.portUI.rows = m.portUI.rows[:0]
	m.portUI.revision, m.portUI.pending, m.portUI.completed = 0, 0, 0
}

func (m *workspaceModel) pollPorts() bool {
	u := &m.portUI
	if !m.remoteWorkspace() || !m.backendConnected() {
		changed := false
		if !m.remoteWorkspace() && m.bottom.active == pencodePanelPorts {
			m.bottom.active = pencodePanelClosed
			changed = true
		}
		if m.ports != nil {
			m.closePorts()
			changed = true
		}
		return changed
	}
	identity := pencodePortsIdentity{m.session.connection.workspaceProfile.SSH, m.projectPath}
	if m.ports == nil || m.ports.backend != m.backend || u.owner != identity {
		m.closePorts()
		if !u.owned || u.owner != identity {
			u.resumeReady = false
			u.mode, u.editKey, u.value, u.selected = "", "", "", ""
		}
		u.owner, u.owned = identity, true
		options := pencodePortsOptions{sshPort: identity.target.Port}
		if u.resumeReady {
			options.resume = &u.resume
		}
		u.message = ""
		m.ports = startPorts(m.backend, options, m.backgroundWake())
	}
	p := m.ports
	p.mu.Lock()
	defer p.mu.Unlock()
	if u.revision == p.revision {
		return false
	}
	u.revision = p.revision
	u.rows = append(u.rows[:0], p.rows...)
	u.message, u.failed = p.message, p.failed
	if p.completed != u.completed {
		u.completed = p.completed
		if p.selected != "" {
			u.selected = p.selected
		}
		if u.pending == p.completed {
			u.pending = 0
			if !p.failed {
				u.mode, u.value, u.editKey = "", "", ""
			}
			u.focus = true
		}
	}
	if u.selected != "" && m.portRow(u.selected) == nil {
		u.selected = ""
	}
	return true
}

func (m *workspaceModel) portRow(key string) *pencodePortRow {
	for i := range m.portUI.rows {
		if m.portUI.rows[i].key == key {
			return &m.portUI.rows[i]
		}
	}
	return nil
}

func (m *workspaceModel) sendPortCommand(command pencodePortCommand) bool {
	u := &m.portUI
	if u.pending != 0 {
		u.message, u.failed = "端口操作正在进行，请稍后重试", true
		return false
	}
	if m.ports == nil {
		u.message, u.failed = "远程连接不可用，请等待连接恢复", true
		return false
	}
	u.nextCommand++
	command.id = u.nextCommand
	select {
	case m.ports.commands <- command:
		u.message, u.failed = "", false
		return true
	default:
		u.message, u.failed = "端口操作正在进行，请稍后重试", true
		return false
	}
}

func (m *workspaceModel) editPort(mode string, row pencodePortRow) {
	u := &m.portUI
	u.mode, u.editKey, u.selected, u.focus, u.message, u.failed = mode, row.key, row.key, true, "", false
	switch mode {
	case "label":
		u.value = row.label
	case "local":
		u.value = strconv.Itoa(int(row.localPort))
	case "new":
		u.value = ""
	}
}

func (m *workspaceModel) commitPortEdit() {
	u := &m.portUI
	if u.pending != 0 {
		return
	}
	command := pencodePortCommand{key: u.editKey, op: u.mode, value: strings.TrimSpace(u.value)}
	switch u.mode {
	case "new":
		port, err := parsePortAddress(u.value)
		if err != nil {
			u.message, u.failed = err.Error(), true
			return
		}
		command.op, command.remote, command.local = "add", port, port.Number
	case "local":
		number, err := strconv.ParseUint(command.value, 10, 16)
		if err != nil {
			u.message, u.failed = "本地端口必须在 0–65535 之间，0 表示自动分配", true
			return
		}
		command.local = uint16(number)
	case "label":
		if utf8.RuneCountInString(command.value) > 64 {
			u.message, u.failed = "端口标签最多 64 个字符", true
			return
		}
	default:
		return
	}
	if m.sendPortCommand(command) {
		u.pending = u.nextCommand
	}
}

func (m *workspaceModel) copyPortAddress(world *module.Content, row pencodePortRow) {
	u := &m.portUI
	state, ok := module.ContentResource[*input.State](world)
	if !ok || !state.SetClipboardText(row.url) {
		u.message, u.failed = "无法复制本地地址", true
		return
	}
	u.message, u.failed = "已复制 "+row.url, false
}

func (m *workspaceModel) portEvent(ref, value string) bool {
	u := &m.portUI
	switch ref {
	case "status-ports":
		if !m.remoteWorkspace() {
			return true
		}
		if m.bottom.active == pencodePanelPorts {
			m.bottom.active = pencodePanelClosed
		} else {
			m.bottom.active = pencodePanelPorts
		}
	case "ports-new":
		m.editPort("new", pencodePortRow{})
	case "ports-input":
		u.value = value
	case "ports-confirm":
		m.commitPortEdit()
	case "ports-cancel":
		u.mode, u.value, u.editKey, u.message = "", "", "", ""
		u.pending = 0
		u.focus = true
	case "ports-refresh":
		m.sendPortCommand(pencodePortCommand{op: "refresh"})
	default:
		return false
	}
	return true
}

func (m *workspaceModel) focusPorts(world *module.Content) {
	u := &m.portUI
	if !u.focus || m.bottom.active != pencodePanelPorts {
		return
	}
	ref := "ports-input"
	if u.mode == "" {
		ref = "ports-new"
		if u.selected != "" {
			ref = "ports-row:" + u.selected
		}
	} else if u.mode != "new" {
		ref = "ports-" + u.mode + "-input:" + u.editKey
	}
	if control, ok := world.HTMLControl(m.surface, ref); ok {
		world.Focus(control)
		if field := module.ContentMut[engineui.TextField](world, control); u.mode != "" && field != nil {
			field.Caret, field.Anchor = utf8.RuneCountInString(field.Value), 0
		}
		u.focus = false
	}
}

func (m *workspaceModel) renderPorts(view *htmlui.View, definition *htmlui.Definition[pencodePortProps, struct{}]) {
	u := &m.portUI
	active := m.remoteWorkspace() && m.bottom.active == pencodePanelPorts
	setPanelVisible(view, "status-ports", m.remoteWorkspace())
	setPanelVisible(view, "ports-pane", active)
	setPanelVisible(view, "ports-tools", active)
	view.Attr("status-ports", "aria-expanded", boolAttribute(active))
	view.Event("status-ports", htmlui.EventClick)
	if !active {
		return
	}
	view.Attr("bottom-panel", "class", "pencode-bottom-panel pencode-ports-panel")
	paneClass := "pencode-ports-pane"
	if len(u.rows) == 0 && u.mode == "" {
		paneClass += " is-empty"
	}
	view.Attr("ports-pane", "class", paneClass)
	for _, ref := range [...]string{"ports-new", "ports-refresh", "ports-confirm", "ports-cancel"} {
		view.Event(ref, htmlui.EventClick)
	}
	for _, pair := range [...][2]string{{"ports-new-icon", "add"}, {"ports-refresh-icon", "refresh-cw"}, {"ports-confirm-icon", "check"}, {"ports-cancel-icon", "close"}} {
		view.Attr(pair[0], "src", m.themedIcon("icons/"+pair[1]+".svg", "icon.foreground"))
	}
	view.Attr("ports-input", "value", u.value)
	setContextMenuEnabled(view, "ports-input", u.pending == 0)
	view.Event("ports-input", htmlui.EventInput)
	setPanelVisible(view, "ports-new-input", u.mode == "new")
	setPanelVisible(view, "ports-footer", u.mode != "")
	setPanelVisible(view, "ports-table", len(u.rows) > 0)
	setPanelVisible(view, "ports-empty", len(u.rows) == 0 && u.mode != "new")
	setPanelVisible(view, "ports-edit-hint", u.mode != "")
	view.Text("ports-message", u.message)
	setPanelVisible(view, "ports-message", u.message != "")
	class := "pencode-ports-message"
	if u.failed {
		class += " error"
	}
	view.Attr("ports-message", "class", class)
	for _, row := range u.rows {
		mode, value := "", ""
		if row.key == u.editKey {
			mode, value = u.mode, u.value
		}
		htmlui.AppendComponentChild(view, "ports-list", row.id, definition, pencodePortProps{Model: m, Row: row, Selected: row.key == u.selected, Editing: mode, Value: value, Pending: u.pending != 0, Theme: m.workbenchTheme}, struct{}{})
	}
}
