package workbench

import (
	"maps"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

// Scopes allow the same chord in mutually exclusive parts of the IDE.
type pencodeCommandBinding struct{ id, name, scope, chord string }

var pencodeExtraShortcuts = []pencodeCommandBinding{
	{"ui-cancel", "取消 / 关闭菜单或对话框", "navigation", "Escape"},
	{"ui-confirm", "确认 / 打开选中项", "navigation", "Enter"},
	{"ui-confirm-space", "激活选中菜单项", "navigation", "Space"},
	{"ui-next", "选择下一项", "navigation", "Down"},
	{"ui-previous", "选择上一项", "navigation", "Up"},
	{"ui-first", "选择第一项", "navigation", "Home"},
	{"ui-last", "选择最后一项", "navigation", "End"},
	{"ui-page-next", "列表下一页", "navigation", "PageDown"},
	{"ui-page-previous", "列表上一页", "navigation", "PageUp"},
	{"ui-left", "返回上级菜单", "navigation", "Left"},
	{"ui-right", "展开子菜单", "navigation", "Right"},
	{"ui-focus-next", "下一个焦点", "navigation", "Tab"},
	{"ui-focus-previous", "上一个焦点", "navigation", "Shift+Tab"},
	{"search-previous", "搜索框上一个匹配", "search", "Shift+Enter"},
	{"search-replace-all", "搜索框全部替换", "search", "Primary+Enter"},

	{"folder-find", "在文件夹中查找", "global", "Primary+Shift+F"},
	{"folder-replace", "在文件夹中替换", "global", "Primary+Shift+H"},
	{"find-next", "下一个匹配", "editor", "F3"},
	{"find-previous", "上一个匹配", "editor", "Shift+F3"},
	{"editor-indent", "增加缩进", "editor", "Tab"},
	{"editor-outdent", "减少缩进", "editor", "Shift+Tab"},
	{"editor-cut", "剪切", "editor", "Primary+X"},
	{"editor-copy", "复制", "editor", "Primary+C"},
	{"editor-paste", "粘贴", "editor", "Primary+V"},
	{"editor-select-all", "全选", "editor", "Primary+A"},
	{"debug-start", "启动调试 / 继续", "global", "F5"},
	{"debug-stop", "停止调试", "global", "Shift+F5"},
	{"debug-breakpoint", "切换断点", "global", "F9"},
	{"debug-next", "单步跳过", "global", "F10"},
	{"debug-stepIn", "单步进入", "global", "F11"},
	{"debug-stepOut", "单步跳出", "global", "Shift+F11"},
	{"debug-evaluate", "计算调试表达式", "debug", "Enter"},
	{"language-completion", "触发补全", "editor", "Ctrl+Space"},
	{"language-signature", "参数提示", "editor", "Ctrl+Shift+Space"},
	{"language-definition", "转到定义", "editor", "F12"},
	{"language-references", "查找引用", "editor", "Shift+F12"},
	{"language-implementation", "转到实现", "editor", "Ctrl+F12"},
	{"language-rename", "重命名符号", "editor", "F2"},
	{"language-format", "格式化文档", "editor", "Alt+Shift+F"},
	{"language-code-action", "代码操作", "editor", "Ctrl+."},
	{"terminal-toggle", "切换终端", "global", "Ctrl+`"},
	{"terminal-new", "新建终端", "global", "Ctrl+Shift+`"},
	{"terminal-copy", "终端复制", "terminal", "Ctrl+Shift+C"},
	{"terminal-paste", "终端粘贴", "terminal", "Ctrl+Shift+V"},
	{"terminal-paste-alternate", "终端粘贴（备用）", "terminal", "Shift+Insert"},
	{"terminal-clear", "清空终端", "terminal", "Ctrl+Shift+K"},
	{"terminal-scroll-up", "终端向上翻页", "terminal", "Shift+PageUp"},
	{"terminal-scroll-down", "终端向下翻页", "terminal", "Shift+PageDown"},
	{"file-context-open", "打开文件", "files", "Enter"},
	{"file-context-new-file", "新建文件", "files", "Primary+N"},
	{"file-context-new-folder", "新建文件夹", "files", "Primary+Shift+N"},
	{"file-context-find", "在此文件夹中查找", "files", "Primary+Shift+F"},
	{"file-context-cut", "剪切文件", "files", "Primary+X"},
	{"file-context-copy", "复制文件", "files", "Primary+C"},
	{"file-context-paste", "粘贴文件", "files", "Primary+V"},
	{"file-context-copy-path", "复制绝对路径", "files", "Primary+Shift+C"},
	{"file-context-copy-relative", "复制相对路径", "files", "Primary+Alt+C"},
	{"file-context-rename", "重命名文件", "files", "F2"},
	{"file-context-delete", "删除文件", "files", "Delete"},
	{"file-context-refresh", "刷新文件树", "files", "F5"},
	{"ports-rename", "重命名端口", "ports", "F2"},
	{"ports-stop", "停止端口转发", "ports", "Delete"},
	{"ports-stop-alternate", "停止端口转发（备用）", "ports", "Backspace"},
	{"ports-open", "在浏览器打开端口", "ports", "Enter"},
	{"ports-copy", "复制端口地址", "ports", "Primary+C"},
	{"source-newline", "提交说明插入换行", "source", "Shift+Enter"},
	{"search-case", "搜索区分大小写", "search", "Alt+C"},
	{"search-word", "搜索全字匹配", "search", "Alt+W"},
	{"search-regex", "搜索正则表达式", "search", "Alt+R"},
	{"search-ignore", "搜索使用忽略文件", "search", "Alt+P"},
	{"panel-close", "关闭底部面板", "global", "Escape"},
	{"extension-submit", "提交扩展输入框", "extension", "Enter"},
}

func (d pencodeCommandBinding) defaultBinding() pencodeKeybinding {
	chord := d.chord
	if runtime.GOOS == "darwin" && (d.id == "terminal-copy" || d.id == "terminal-paste") {
		chord = "Super+" + chord[len("Ctrl+Shift+"):]
	}
	return pencodeBindingChord(chord)
}
func pencodeBindingChord(chord string) pencodeKeybinding {
	if chord == "" {
		return pencodeKeybinding{}
	}
	if key, ok := pencodeShortcutKeys[chord]; ok {
		return pencodeKeybinding{Key: key}
	}
	s, err := peb.ParseShortcut(chord)
	if err != nil {
		return pencodeKeybinding{}
	}
	return pencodeShortcutBinding(s)
}

func resolvePencodeBindings(saved map[string]pencodeKeybinding) map[string]pencodeKeybinding {
	bindings := defaultPencodeSettings().Bindings
	for id, binding := range saved {
		if validPencodeKeybinding(binding) {
			bindings[id] = binding
		}
	}
	return bindings
}

func (m *workspaceModel) commandMatches(id string, state *input.State, goos string) bool {
	m.ensureSettings()
	return m.settings.Bindings[id].matches(state, goos)
}

func pencodeShortcutScope(id string) string {
	for _, d := range pencodeExtraShortcuts {
		if d.id == id {
			return d.scope
		}
	}
	return "global"
}

func (m *workspaceModel) shortcutConflict(id string, binding pencodeKeybinding, goos string) string {
	if binding.Key == 0 {
		return ""
	}
	m.syncActions()
	scope := pencodeShortcutScope(id)
	for _, a := range m.actions.snapshot {
		if a.ID == id {
			scope = pencodeActionScope(a)
			break
		}
	}
	check := func(other, name, otherScope string, b pencodeKeybinding) string {
		if other != id && pencodeScopesOverlap(scope, otherScope) && b.Key != 0 && samePencodeBinding(binding, b, goos) {
			return name
		}
		return ""
	}
	for _, d := range pencodeShortcutDefinitions {
		if name := check(d.id, d.name, "global", m.settings.Bindings[d.id]); name != "" {
			return name
		}
	}
	for _, d := range pencodeExtraShortcuts {
		if name := check(d.id, d.name, d.scope, m.settings.Bindings[d.id]); name != "" {
			return name
		}
	}
	for _, a := range m.actions.snapshot {
		b := m.actionBinding(a)
		if name := check(a.ID, a.Text, pencodeActionScope(a), b); name != "" {
			return name
		}
	}
	return ""
}
func pencodeActionScope(a peb.Action) string {
	if a.Location == peb.LeftContextMenu {
		return "files"
	}
	if a.Location == peb.RightContextMenu || a.Location == peb.OpenWith {
		return "editor"
	}
	return "global"
}
func (m *workspaceModel) actionBinding(a peb.Action) pencodeKeybinding {
	if b, ok := m.settings.Bindings[a.ID]; ok {
		return b
	}
	return pencodeBindingChord(a.Shortcut)
}

// Only inactive maps live in Keymaps. Bindings always holds the selected map,
// so existing command dispatch and remote saves share exactly one source.
func (m *workspaceModel) switchKeymap(name string) bool {
	if name == m.settings.Keymap {
		return false
	}
	bindings, ok := m.settings.Keymaps[name]
	if !ok {
		return false
	}
	delete(m.settings.Keymaps, name)
	m.settings.Keymaps[m.settings.Keymap] = m.settings.Bindings
	m.settings.Keymap, m.settings.Bindings = name, bindings
	m.capturingShortcut = false
	m.refreshSettingsLabels()
	return true
}
func (m *workspaceModel) handleKeymapEvent(c *module.ContentEventContext[appProps, appState]) bool {
	switch c.Ref {
	case "shortcut-query":
		m.shortcutQuery = strings.ToLower(strings.TrimSpace(c.Event.Value))
		return true
	case "keymap-name":
		m.keymapName = c.Event.Value
		return true
	case "keymap-select":
		m.switchKeymap(c.Event.Value)
	case "keymap-create", "keymap-rename":
		name := strings.TrimSpace(m.keymapName)
		if name == "" || name == "default" || utf8.RuneCountInString(name) > 80 {
			m.settingsFeedback = "请输入 1–80 字符的方案名称（不能为 default）"
			return true
		}
		if name == m.settings.Keymap || m.settings.Keymaps[name] != nil {
			m.settingsFeedback = "方案名称已存在"
			return true
		}
		if m.settings.Keymaps == nil {
			m.settings.Keymaps = make(map[string]map[string]pencodeKeybinding)
		}
		if c.Ref == "keymap-create" {
			m.settings.Keymaps[name] = maps.Clone(m.settings.Bindings)
			m.switchKeymap(name)
		} else {
			if m.settings.Keymap == "default" {
				m.settingsFeedback = "默认方案不能重命名"
				return true
			}
			m.settings.Keymap = name
		}
		m.keymapName = ""
	case "keymap-delete":
		if m.settings.Keymap == "default" {
			return true
		}
		name := m.settings.Keymap
		m.switchKeymap("default")
		delete(m.settings.Keymaps, name)
	default:
		return false
	}
	m.capturingShortcut = false
	m.refreshSettingsLabels()
	m.saveSettings()
	return true
}

type pencodeShortcutProps struct {
	Model           *workspaceModel
	ID, Name, Scope string
	Binding         pencodeKeybinding
}

func newShortcutDefinition(content *module.Content) (*htmlui.Definition[pencodeShortcutProps, struct{}], error) {
	return module.ContentDefine[pencodeShortcutProps, struct{}](content,
		htmlui.NewTemplate(`<div class="pencode-settings-row"><span data-ref="name"></span><button data-ref="binding" class="pencode-settings-binding" type="button"></button></div>`), "",
		module.ContentBehavior[pencodeShortcutProps, struct{}]{
			Render: func(c module.ContentRenderContext[pencodeShortcutProps, struct{}], v *htmlui.View) {
				p := c.Props
				v.Text("name", p.Name+" · "+pencodeScopeLabel(p.Scope))
				v.Attr("name", "title", p.ID)
				label, class := p.Binding.label(runtime.GOOS), "pencode-settings-binding"
				if p.Model.capturingShortcut && p.Model.captureAction == p.ID {
					label = "按下快捷键…"
					class += " recording"
				}
				v.Text("binding", label)
				v.Attr("binding", "class", class)
				v.Attr("binding", "aria-label", p.Name+"："+label)
				v.Attr("binding", "id", "shortcut-"+p.ID)
				v.Event("binding", htmlui.EventClick)
			},
			HandleEvent: func(c *module.ContentEventContext[pencodeShortcutProps, struct{}]) {
				if c.Ref != "binding" {
					return
				}
				m := c.Props.Model
				m.captureAction, m.capturingShortcut, m.settingsFeedback = c.Props.ID, true, ""
				markPencodeRootDirtyByModel(c.World, m)
			}})
}
func (m *workspaceModel) renderShortcutSettings(v *htmlui.View, d *htmlui.Definition[pencodeShortcutProps, struct{}]) {
	if !m.settingsOpen || !m.settingsShortcuts {
		return
	}
	names := make([]string, 0, len(m.settings.Keymaps)+1)
	names = append(names, m.settings.Keymap)
	for name := range m.settings.Keymaps {
		names = append(names, name)
	}
	slices.Sort(names)
	options := make([]htmlui.VNode, 0, len(names))
	for i, name := range names {
		label := name
		if name == "default" {
			label = "默认"
		}
		options = append(options, appearanceOption(uint64(i+1), name, label, name == m.settings.Keymap))
	}
	v.Keyed("keymap-select", options)
	v.Event("keymap-select", htmlui.EventChange)
	v.Event("shortcut-query", htmlui.EventInput)
	v.Attr("shortcut-query", "value", m.shortcutQuery)
	v.Event("keymap-name", htmlui.EventInput)
	v.Attr("keymap-name", "value", m.keymapName)
	for _, ref := range [...]string{"keymap-create", "keymap-rename", "keymap-delete"} {
		v.Event(ref, htmlui.EventClick)
	}
	for _, ref := range [...]string{"keymap-rename", "keymap-delete"} {
		if m.settings.Keymap == "default" {
			v.Attr(ref, "disabled", "")
		} else {
			v.RemoveAttr(ref, "disabled")
		}
	}
	appendRow := func(id, name, scope string, binding pencodeKeybinding) {
		if m.shortcutQuery != "" && !strings.Contains(strings.ToLower(name+" "+id+" "+pencodeScopeLabel(scope)), m.shortcutQuery) {
			return
		}
		htmlui.AppendComponentChild(v, "settings-shortcut-fields", componentKey(id), d, pencodeShortcutProps{m, id, name, scope, binding}, struct{}{})
	}
	for _, entry := range pencodeShortcutDefinitions {
		appendRow(entry.id, entry.name, "工作台", m.settings.Bindings[entry.id])
	}
	for _, entry := range pencodeExtraShortcuts {
		appendRow(entry.id, entry.name, entry.scope, m.settings.Bindings[entry.id])
	}
	m.syncActions()
	for _, a := range m.actions.snapshot {
		if !a.Hidden {
			name := a.Text
			if name == "" {
				name = a.Tooltip
			}
			appendRow(a.ID, name, pencodeActionScope(a), m.actionBinding(a))
		}
	}
}

// Navigation commands are shared by IDE menus, lists and dialogs. Raw editor
// typing and terminal escape sequences remain owned by their text input layer.
func (m *workspaceModel) uiKey(state *input.State, key input.KeyCode) bool {
	id := ""
	switch key {
	case input.KeyEscape:
		id = "ui-cancel"
	case input.KeyEnter:
		id = "ui-confirm"
	case input.KeySpace:
		id = "ui-confirm-space"
	case input.KeyDown:
		id = "ui-next"
	case input.KeyUp:
		id = "ui-previous"
	case input.KeyHome:
		id = "ui-first"
	case input.KeyEnd:
		id = "ui-last"
	case input.KeyPageDown:
		id = "ui-page-next"
	case input.KeyPageUp:
		id = "ui-page-previous"
	case input.KeyLeft:
		id = "ui-left"
	case input.KeyRight:
		id = "ui-right"
	case input.KeyTab:
		return m.commandMatches("ui-focus-next", state, runtime.GOOS) || m.commandMatches("ui-focus-previous", state, runtime.GOOS)
	}
	return m.commandMatches(id, state, runtime.GOOS)
}

func pencodeScopeLabel(scope string) string {
	switch scope {
	case "global":
		return "工作台"
	case "editor":
		return "编辑器"
	case "terminal":
		return "终端"
	case "files":
		return "文件树菜单"
	case "ports":
		return "端口"
	case "debug":
		return "调试"
	case "source":
		return "源代码管理"
	case "search":
		return "搜索"
	case "navigation":
		return "菜单与列表"
	case "extension":
		return "扩展界面"
	}
	return scope
}

var pencodeLanguageShortcuts = func() []struct{ id, method string } {
	commands := make([]struct{ id, method string }, 0, len(pencodeLanguageActions))
	aliases := map[string]string{
		"textDocument/completion": "language-completion", "textDocument/signatureHelp": "language-signature",
		"textDocument/definition": "language-definition", "textDocument/references": "language-references",
		"textDocument/implementation": "language-implementation", "textDocument/rename": "language-rename",
		"textDocument/formatting": "language-format", "textDocument/codeAction": "language-code-action",
	}
	for _, a := range pencodeLanguageActions {
		id := aliases[a.method]
		if id == "" {
			id = "language:" + a.method
		}
		commands = append(commands, struct{ id, method string }{id, a.method})
	}
	return commands
}()

func init() {
	for i, c := range pencodeLanguageShortcuts {
		if strings.HasPrefix(c.id, "language:") {
			pencodeExtraShortcuts = append(pencodeExtraShortcuts, pencodeCommandBinding{c.id, pencodeLanguageActions[i].label, "editor", ""})
		}
	}
}

func (m *workspaceModel) commandLabel(id string) string {
	return m.settings.Bindings[id].label(runtime.GOOS)
}
func (m *workspaceModel) shortcutTitle(name, id string) string {
	label := m.commandLabel(id)
	if label == "未设置" {
		return name
	}
	return name + " (" + label + ")"
}

func pencodeScopesOverlap(a, b string) bool {
	if a == b {
		return true
	}
	// Menus consume the event before workbench commands run.
	if a == "navigation" || b == "navigation" || a == "files" || b == "files" {
		return false
	}
	return a == "global" || b == "global"
}
