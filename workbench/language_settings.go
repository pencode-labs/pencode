package workbench

import (
	"fmt"
	"strings"

	"github.com/onix-engine/onix/html/markup"
	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/languagetool"
)

type toolSettingsPage uint8

const (
	noToolSettings toolSettingsPage = iota
	languageServerSettings
	debugAdapterSettings
	diskStorageSettings
)

func (m *workspaceModel) toolPaths(id string) *map[string]string {
	if languagetool.IsDebugAdapter(id) {
		return &m.settings.DebugAdapterPaths
	}
	return &m.settings.ServerPaths
}

type languageChoiceProps struct {
	Model    *workspaceModel
	Language *languagetool.Language
	Active   *languagetool.Tool
	Expanded bool
}

type languageToolProps struct {
	Model                      *workspaceModel
	Language                   *languagetool.Language
	Tool                       *languagetool.Tool
	Locale, Override, Feedback string
	Info                       languagetool.Installation
	Active, Busy               bool
}

func newLanguageChoiceDefinition(content *module.Content) (*htmlui.Definition[languageChoiceProps, struct{}], error) {
	template, err := htmlui.BuildTemplate(func(b *markup.Builder) {
		b.Open("button", "data-ref", "choice", "type", "button", "class", "pencode-language-choice")
		b.Element("img", "data-ref", "arrow", "class", "pencode-settings-toggle", "alt", "")
		b.Element("img", "data-ref", "icon", "class", "pencode-language-icon", "alt", "")
		b.Element("span", "data-ref", "name")
		b.Element("span", "data-ref", "active", "class", "pencode-language-active")
		b.End()
	})
	if err != nil {
		return nil, err
	}
	return content.Define[languageChoiceProps, struct{}](template, "", module.ContentBehavior[languageChoiceProps, struct{}]{
		Render: func(c module.ContentRenderContext[languageChoiceProps, struct{}], v *htmlui.View) {
			p := c.Props
			lang := p.Language
			v.Attr("choice", "id", "language-choice-"+lang.ID)
			v.Attr("choice", "aria-expanded", boolAttribute(p.Expanded))
			v.Attr("arrow", "src", p.Model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			v.Attr("icon", "src", lang.Icon)
			v.Text("name", lang.Label)
			label := ""
			if p.Active != nil {
				label = p.Active.Label
			}
			v.Text("active", label)
			v.Event("choice", htmlui.EventClick)
		},
		HandleEvent: func(c *module.ContentEventContext[languageChoiceProps, struct{}]) {
			m := c.Props.Model
			if state := m.builtinTools; state != nil {
				if state.expanded == c.Props.Language {
					state.expanded = nil
				} else {
					state.expanded = c.Props.Language
				}
				markPencodeRootDirtyByModel(c.World, m)
			}
		},
	})
}

func newLanguageToolDefinition(content *module.Content) (*htmlui.Definition[languageToolProps, bool], error) {
	template, err := htmlui.BuildTemplate(func(b *markup.Builder) {
		b.Open("div", "class", "pencode-language-tool")
		b.Open("div", "data-ref", "summary", "class", "pencode-language-summary")
		b.Open("button", "data-ref", "details", "type", "button", "class", "pencode-language-details")
		b.Element("img", "data-ref", "arrow", "class", "pencode-settings-toggle", "alt", "")
		b.Element("span", "data-ref", "name")
		b.End()
		b.Element("span", "data-ref", "status", "class", "pencode-setting-description")
		b.Element("button", "data-ref", "activate", "type", "button", "class", "pencode-settings-reset")
		b.Element("button", "data-ref", "install", "type", "button", "class", "pencode-settings-reset")
		b.Element("button", "data-ref", "uninstall", "type", "button", "class", "pencode-settings-reset")
		b.End()
		b.Element("div", "data-ref", "runtime", "class", "pencode-language-feedback pencode-setting-description", "hidden", "")
		b.Element("div", "data-ref", "feedback", "class", "pencode-language-feedback", "role", "status", "aria-live", "polite", "hidden", "")
		b.Open("div", "data-ref", "path-row", "class", "pencode-language-path", "hidden", "")
		b.Element("label", "data-ref", "label")
		b.Element("input", "data-ref", "path", "type", "text", "class", "pencode-setting-input")
		b.End()
		b.End()
	})
	if err != nil {
		return nil, err
	}
	return content.Define[languageToolProps, bool](template, "", module.ContentBehavior[languageToolProps, bool]{
		Render: func(c module.ContentRenderContext[languageToolProps, bool], v *htmlui.View) {
			p := c.Props
			m, tool := p.Model, p.Tool
			v.Text("name", tool.Label)
			v.Text("feedback", p.Feedback)
			setPanelVisible(v, "feedback", p.Feedback != "")
			v.Attr("arrow", "src", m.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			pathLabel := "可执行文件路径"
			var runtimeHint string
			if tool.ID == languagetool.Xdebug {
				pathLabel = "phpDebug.js 路径"
				runtimeHint = m.tr("适配器需要 Node.js，PHP 运行时需启用 Xdebug 扩展。")
			} else if tool.Manager == "" && p.Info.Command == "" {
				runtimeHint = fmt.Sprintf(m.tr("请在工作区主机安装 %s，并将其加入 PATH 或设置可执行文件路径。"), tool.Package)
			}
			v.Text("runtime", runtimeHint)
			setPanelVisible(v, "runtime", runtimeHint != "")
			v.Attr("details", "title", m.tr(pathLabel))
			v.Attr("details", "aria-expanded", boolAttribute(*c.State))
			v.Attr("details", "aria-controls", "language-tool-path-row-"+tool.ID)
			v.Attr("path-row", "id", "language-tool-path-row-"+tool.ID)
			setPanelVisible(v, "path-row", *c.State)
			status := m.tr("未安装")
			if tool.Manager != "" {
				status += " · " + tool.Manager + " · " + tool.Package
			}
			placeholder := m.tr("留空以自动检测")
			if p.Info.Command != "" {
				source := "系统环境"
				if p.Override != "" {
					source = "自定义路径"
				} else if p.Info.Managed {
					source = "由 Pencode 管理"
				}
				path := p.Info.Entry
				if path == "" {
					path = p.Info.Command
				}
				if p.Override == "" {
					placeholder = path
				}
				status = m.tr(source) + " · " + path
			} else if p.Info.Problem != "" {
				status = p.Info.Problem
			}
			v.Text("status", status)
			v.Attr("status", "title", status)
			v.Text("label", m.tr(pathLabel))
			v.Attr("label", "for", "language-tool-path-"+tool.ID)
			v.Attr("path", "id", "language-tool-path-"+tool.ID)
			v.Attr("path", "value", p.Override)
			v.Attr("path", "placeholder", placeholder)
			v.Event("path", htmlui.EventChange)
			v.Event("summary", htmlui.EventClick)
			for _, ref := range [...]string{"details", "activate", "install", "uninstall"} {
				v.Event(ref, htmlui.EventClick)
				v.Attr(ref, "id", "language-tool-"+ref+"-"+tool.ID)
			}
			activate := "激活"
			if p.Active {
				activate = "已激活"
			}
			setPanelVisible(v, "activate", !languagetool.IsDebugAdapter(tool.ID))
			v.Text("activate", m.tr(activate))
			v.Attr("activate", "aria-pressed", boolAttribute(p.Active))
			install := "安装"
			if p.Info.Managed {
				install = "更新"
			} else if p.Info.Command != "" {
				install = "安装托管副本"
			}
			v.Text("install", m.tr(install))
			setPanelVisible(v, "install", tool.Manager != "")
			v.Text("uninstall", m.tr("卸载"))
			setPanelVisible(v, "uninstall", p.Info.Managed)
			for _, ref := range [...]string{"activate", "install", "uninstall", "path"} {
				setContextMenuEnabled(v, ref, !p.Busy)
			}
		},
		HandleEvent: func(c *module.ContentEventContext[languageToolProps, bool]) {
			if c.Event.Kind == htmlui.EventClick {
				c.StopPropagation()
			}
			if c.Ref == "summary" || c.Ref == "details" {
				c.UpdateState(func(expanded *bool) { *expanded = !*expanded })
				return
			}
			p := c.Props
			m, id := p.Model, p.Tool.ID
			if m.builtinTools == nil || m.builtinTools.cancel != nil || !m.backendConnected() {
				return
			}
			switch c.Ref {
			case "activate":
				if p.Info.Command == "" {
					m.reportLanguageTool(id, "[Warning]", fmt.Sprintf(m.tr("请先安装 %s 再激活。"), p.Tool.Label))
					break
				}
				if m.settings.LanguageServers == nil {
					m.settings.LanguageServers = make(map[string]string)
				}
				m.settings.LanguageServers[p.Language.ID] = id
				m.saveSettings()
				m.applyBuiltinTools()
				message := p.Language.Label + " · " + p.Tool.Label + " · " + m.tr("已激活")
				if !m.settingsRemote && m.settingsFeedback != "" {
					m.reportLanguageTool(id, "[Error]", message+" · "+m.settingsFeedback)
				} else {
					m.reportLanguageTool(id, "[Info]", message)
				}
			case "path":
				path := strings.TrimSpace(c.Event.Value)
				paths := m.toolPaths(id)
				if (*paths)[id] == path {
					return
				}
				if path == "" {
					delete(*paths, id)
				} else {
					if *paths == nil {
						*paths = make(map[string]string)
					}
					(*paths)[id] = path
				}
				m.saveSettings()
				m.startBuiltinTools("", "")
			case "install":
				op := "install"
				if p.Info.Managed {
					op = "update"
				}
				m.startBuiltinTools(op, id)
			case "uninstall":
				m.startBuiltinTools("uninstall", id)
			}
			markPencodeRootDirtyByModel(c.World, m)
		},
	})
}

func (m *workspaceModel) renderLanguageSettings(v *htmlui.View, choice *htmlui.Definition[languageChoiceProps, struct{}], row *htmlui.Definition[languageToolProps, bool]) {
	if !m.settingsOpen {
		return
	}
	for _, nav := range [...]string{"settings-language-servers-nav", "settings-debug-adapters-nav"} {
		v.Event(nav, htmlui.EventClick)
		v.Attr(nav, "class", "pencode-settings-nav-item")
		v.RemoveAttr(nav, "aria-current")
	}
	open := m.settingsToolPage == languageServerSettings || m.settingsToolPage == debugAdapterSettings
	setPanelVisible(v, "settings-language-servers-pane", open)
	setPanelVisible(v, "language-tools-refresh", open)
	if !open {
		return
	}
	nav, title, languages := "settings-language-servers-nav", "语言服务器", languagetool.Languages[:]
	if m.settingsToolPage == debugAdapterSettings {
		nav, title, languages = "settings-debug-adapters-nav", "调试器（DAP）", languagetool.DebugLanguages[:]
	}
	v.Text("settings-page-title", m.tr(title))
	v.Attr(nav, "class", "pencode-settings-nav-item active")
	v.Attr(nav, "aria-current", "page")
	v.Attr("settings-appearance-nav", "class", "pencode-settings-nav-item")
	v.RemoveAttr("settings-appearance-nav", "aria-current")
	setPanelVisible(v, "settings-appearance-pane", false)
	v.Event("language-tools-refresh", htmlui.EventClick)
	state := m.builtinTools
	if state == nil {
		v.Text("language-tools-feedback", m.tr("等待工作区连接"))
		setContextMenuEnabled(v, "language-tools-refresh", m.backendConnected())
		return
	}
	busy := state.cancel != nil || !m.backendConnected()
	setContextMenuEnabled(v, "language-tools-refresh", !busy)
	feedback := state.feedback
	if state.feedbackID != "" {
		feedback = ""
	}
	v.Text("language-tools-feedback", feedback)
	for i := range languages {
		lang := &languages[i]
		active := m.activeTool(lang)
		expanded := state.expanded == lang
		v.AppendComponentChild("language-list", uint64(i+1), choice, languageChoiceProps{m, lang, languagetool.Find(active), expanded}, struct{}{})
		if expanded {
			for _, id := range lang.Tools {
				props := languageToolProps{Model: m, Language: lang, Tool: languagetool.Find(id), Locale: m.settings.Language, Override: (*m.toolPaths(id))[id], Info: state.items[id], Active: active == id, Busy: busy}
				if id == state.feedbackID {
					props.Feedback = state.feedback
				}
				v.AppendComponentChild("language-list", componentKey(id), row, props, false)
			}
		}
	}
}
