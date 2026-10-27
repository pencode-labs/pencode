package workbench

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/diiyw/onix/module"
	htmlui "github.com/diiyw/onix/ui/html"
	"github.com/pencode-labs/pencode/peb"
)

type pencodeExtensionSettings struct {
	id, label, navRef string
	schema            []peb.Setting
	values            peb.SettingValues
	rows              []pencodeSettingProps
}

type pencodeSettingNavProps struct {
	Model    *workspaceModel
	Group    *pencodeExtensionSettings
	Selected bool
}

type pencodeSettingProps struct {
	Model               *workspaceModel
	Group               *pencodeExtensionSettings
	Index               int
	Ref, DescriptionRef string
	Value               string
	Options             []htmlui.VNode
}

func (m *workspaceModel) importExtensionSettings(id, label string, schema []peb.Setting) {
	m.removeExtensionSettings(id)
	if len(schema) == 0 {
		return
	}
	group := &pencodeExtensionSettings{id: id, label: label, navRef: "settings-extension:" + id, schema: schema}
	m.extensionSettings = append(m.extensionSettings, group)
	sort.Slice(m.extensionSettings, func(i, j int) bool { return m.extensionSettings[i].id < m.extensionSettings[j].id })
	m.rebuildExtensionSettings(group)
}

func (m *workspaceModel) removeExtensionSettings(id string) {
	for i, group := range m.extensionSettings {
		if group.id == id {
			copy(m.extensionSettings[i:], m.extensionSettings[i+1:])
			m.extensionSettings[len(m.extensionSettings)-1] = nil
			m.extensionSettings = m.extensionSettings[:len(m.extensionSettings)-1]
			break
		}
	}
	if m.settingsExtension == id {
		m.settingsExtension = ""
	}
}

func (m *workspaceModel) extensionSettingValues(id string) peb.SettingValues {
	for _, group := range m.extensionSettings {
		if group.id == id {
			return group.values
		}
	}
	return nil
}

func extensionOwner(id string) string {
	id, _ = strings.CutPrefix(id, "extensions/")
	owner, _, _ := strings.Cut(id, "/")
	return owner
}

func (m *workspaceModel) rebuildExtensionSettings(group *pencodeExtensionSettings) {
	group.values = peb.ResolveSettings(group.schema, m.settings.Extensions[group.id])
	group.rows = make([]pencodeSettingProps, len(group.schema))
	for i, setting := range group.schema {
		value := strings.TrimSpace(string(group.values[setting.ID]))
		if setting.Type == peb.SettingString {
			_ = json.Unmarshal(group.values[setting.ID], &value)
		}
		row := pencodeSettingProps{Model: m, Group: group, Index: i, Ref: "extension-setting:" + group.id + ":" + setting.ID, Value: value}
		row.DescriptionRef = row.Ref + ":description"
		for j, option := range setting.Options {
			row.Options = append(row.Options, appearanceOption(uint64(j+1), option.Value, option.Label, option.Value == value))
		}
		group.rows[i] = row
	}
}

func (m *workspaceModel) updateExtensionSetting(group *pencodeExtensionSettings, index int, value string, checked bool) {
	setting := group.schema[index]
	var data json.RawMessage
	switch setting.Type {
	case peb.SettingString:
		data, _ = json.Marshal(value)
	case peb.SettingBoolean:
		data = json.RawMessage("false")
		if checked {
			data = json.RawMessage("true")
		}
	default:
		data = json.RawMessage(strings.TrimSpace(value))
	}
	if err := setting.Validate(data); err != nil {
		m.settingsFeedback = fmt.Sprintf("%s：请输入有效的配置值", setting.Label)
		return
	}
	m.settingsFeedback = ""
	if string(group.values[setting.ID]) == string(data) {
		return
	}
	if m.settings.Extensions == nil {
		m.settings.Extensions = make(map[string]peb.SettingValues)
	}
	if m.settings.Extensions[group.id] == nil {
		m.settings.Extensions[group.id] = make(peb.SettingValues)
	}
	m.settings.Extensions[group.id][setting.ID] = data
	m.publishExtensionSettings(group)
}

func (m *workspaceModel) publishExtensionSettings(group *pencodeExtensionSettings) {
	m.rebuildExtensionSettings(group)
	m.saveSettings()
	if process := m.actions.sessions[group.id]; process != nil {
		process.SubmitSettings(group.values)
	}
	for _, document := range m.customDocuments {
		if document.provider != nil && extensionOwner(document.provider.ID) == group.id {
			document.process.SubmitSettings(group.values)
		}
	}
}

func (m *workspaceModel) resetExtensionSettings() {
	for _, group := range m.extensionSettings {
		if group.id == m.settingsExtension {
			delete(m.settings.Extensions, group.id)
			m.publishExtensionSettings(group)
			return
		}
	}
}

func newSettingNavDefinition(content *module.Content) (*htmlui.Definition[pencodeSettingNavProps, struct{}], error) {
	return module.ContentDefine[pencodeSettingNavProps, struct{}](content, htmlui.NewTemplate(`<button data-ref="select" class="pencode-settings-nav-item pencode-settings-extension-nav" type="button"></button>`), "", module.ContentBehavior[pencodeSettingNavProps, struct{}]{
		Render: func(c module.ContentRenderContext[pencodeSettingNavProps, struct{}], view *htmlui.View) {
			p := c.Props
			view.Event("select", htmlui.EventClick)
			view.Text("select", p.Group.label)
			view.Attr("select", "id", p.Group.navRef)
			view.Attr("select", "title", p.Group.label)
			class := "pencode-settings-nav-item pencode-settings-extension-nav"
			if p.Selected {
				class += " active"
				view.Attr("select", "aria-current", "page")
			} else {
				view.RemoveAttr("select", "aria-current")
			}
			view.Attr("select", "class", class)
		},
		HandleEvent: func(c *module.ContentEventContext[pencodeSettingNavProps, struct{}]) {
			m := c.Props.Model
			m.settingsExtensions, m.settingsShortcuts, m.capturingShortcut = true, false, false
			m.settingsExtension, m.settingsFeedback = c.Props.Group.id, ""
			markPencodeRootDirtyByModel(c.World, m)
		},
	})
}

func newSettingDefinition(content *module.Content) (*htmlui.Definition[pencodeSettingProps, struct{}], error) {
	return module.ContentDefine[pencodeSettingProps, struct{}](content, htmlui.NewTemplate(`<div class="pencode-settings-row pencode-extension-setting"><div class="pencode-setting-copy"><label data-ref="label"></label><p data-ref="description" class="pencode-setting-description"></p></div><input data-ref="input" class="pencode-setting-input" type="text"><input data-ref="boolean" type="checkbox" hidden><select data-ref="select" class="pencode-settings-select" hidden></select></div>`), "", module.ContentBehavior[pencodeSettingProps, struct{}]{
		Render: renderExtensionSetting,
		HandleEvent: func(c *module.ContentEventContext[pencodeSettingProps, struct{}]) {
			p := c.Props
			p.Model.updateExtensionSetting(p.Group, p.Index, c.Event.Value, c.Event.Checked)
			markPencodeRootDirtyByModel(c.World, p.Model)
		},
	})
}

func renderExtensionSetting(c module.ContentRenderContext[pencodeSettingProps, struct{}], view *htmlui.View) {
	p := c.Props
	s := p.Group.schema[p.Index]
	view.Text("label", s.Label)
	view.Text("description", s.Description)
	view.Attr("description", "id", p.DescriptionRef)
	if s.Description == "" {
		view.Attr("description", "hidden", "")
	} else {
		view.RemoveAttr("description", "hidden")
	}
	ref := "input"
	if s.Type == peb.SettingBoolean {
		ref = "boolean"
	} else if len(s.Options) != 0 {
		ref = "select"
	}
	for _, control := range [...]string{"input", "boolean", "select"} {
		if ref == control {
			view.RemoveAttr(control, "hidden")
			view.Attr(control, "id", p.Ref)
		} else {
			view.Attr(control, "hidden", "")
			view.RemoveAttr(control, "id")
		}
	}
	view.Attr("label", "for", p.Ref)
	view.Attr(ref, "aria-label", s.Label)
	view.Attr(ref, "aria-describedby", p.DescriptionRef)
	view.Event(ref, htmlui.EventChange)
	if ref == "boolean" {
		if p.Value == "true" {
			view.Attr(ref, "checked", "")
		} else {
			view.RemoveAttr(ref, "checked")
		}
	} else if ref == "select" {
		view.Keyed(ref, p.Options)
	} else {
		view.Attr(ref, "value", p.Value)
		if s.Type == peb.SettingNumber || s.Type == peb.SettingInteger {
			view.Attr(ref, "inputmode", "decimal")
		}
	}
}

func (m *workspaceModel) renderExtensionSettings(view *htmlui.View, nav *htmlui.Definition[pencodeSettingNavProps, struct{}], row *htmlui.Definition[pencodeSettingProps, struct{}]) {
	if !m.settingsOpen {
		return
	}
	view.Event("settings-extensions-nav", htmlui.EventClick)
	view.Event("reset-extension-settings", htmlui.EventClick)
	view.Attr("settings-extensions-nav", "class", "pencode-settings-nav-item")
	view.RemoveAttr("settings-extensions-nav", "aria-current")
	if m.settingsExtensions && m.settingsExtension == "" {
		view.Attr("settings-extensions-nav", "class", "pencode-settings-nav-item active")
		view.Attr("settings-extensions-nav", "aria-current", "page")
	}
	view.Attr("settings-extensions-nav", "aria-expanded", boolAttribute(m.settingsExtensionsExpanded))
	view.Text("settings-extensions-toggle", "›")
	if m.settingsExtensionsExpanded {
		view.Text("settings-extensions-toggle", "⌄")
		for _, group := range m.extensionSettings {
			htmlui.AppendComponentChild(view, "settings-extension-list", componentKey(group.id), nav, pencodeSettingNavProps{Model: m, Group: group, Selected: m.settingsExtensions && m.settingsExtension == group.id}, struct{}{})
		}
	}
	view.Attr("settings-extensions-pane", "hidden", "")
	view.Attr("reset-extension-settings", "hidden", "")
	if !m.settingsExtensions {
		return
	}
	view.Text("settings-page-title", "扩展")
	view.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item")
	view.RemoveAttr("settings-appearance-nav", "aria-current")
	view.Attr("settings-shortcuts-nav", "class", "pencode-settings-nav-item")
	view.RemoveAttr("settings-shortcuts-nav", "aria-current")
	view.Attr("settings-appearance-pane", "hidden", "")
	view.Attr("settings-shortcuts-pane", "hidden", "")
	view.Attr("reset-shortcuts", "hidden", "")
	view.RemoveAttr("settings-extensions-pane", "hidden")
	view.RemoveAttr("settings-extensions-empty", "hidden")
	view.Text("settings-extensions-empty", "选择左侧扩展以查看配置项")
	if len(m.extensionSettings) == 0 {
		view.Text("settings-extensions-empty", "暂无支持设置的已启用扩展")
	}
	for _, group := range m.extensionSettings {
		if group.id != m.settingsExtension {
			continue
		}
		view.Text("settings-page-title", group.label)
		view.Attr("settings-extensions-empty", "hidden", "")
		view.RemoveAttr("reset-extension-settings", "hidden")
		for _, props := range group.rows {
			props.Model = m
			htmlui.AppendComponentChild(view, "settings-extension-fields", componentKey(props.Ref), row, props, struct{}{})
		}
	}
}
