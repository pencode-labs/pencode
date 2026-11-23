package workbench

import (
	"fmt"
	"strings"

	"github.com/onix-engine/onix/module"
	htmlui "github.com/onix-engine/onix/ui/html"
	"github.com/pencode-labs/pencode/internal/languagetool"
)

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
	const template = `<button data-ref="choice" type="button" class="pencode-language-choice"><img data-ref="arrow" class="pencode-settings-toggle" alt=""><img data-ref="icon" class="pencode-language-icon" alt=""><span data-ref="name"></span><span data-ref="active" class="pencode-language-active"></span></button>`
	return content.Define[languageChoiceProps, struct{}](htmlui.NewTemplate(template), "", module.ContentBehavior[languageChoiceProps, struct{}]{
		Render: func(c module.ContentRenderContext[languageChoiceProps, struct{}], v *htmlui.View) {
			p := c.Props
			lang := p.Language
			v.Attr("choice", "id", "language-choice-"+lang.ID)
			v.Attr("choice", "aria-expanded", boolAttribute(p.Expanded))
			v.Attr("arrow", "src", p.Model.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			v.Attr("icon", "src", lang.Icon)
			v.Text("name", lang.Label)
			if p.Active != nil {
				v.Text("active", p.Active.Label)
			}
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
	const template = `<div class="pencode-language-tool">
		<div data-ref="summary" class="pencode-language-summary">
			<button data-ref="details" type="button" class="pencode-language-details"><img data-ref="arrow" class="pencode-settings-toggle" alt=""><span data-ref="name"></span></button>
			<span data-ref="status" class="pencode-setting-description"></span>
			<button data-ref="activate" type="button" class="pencode-settings-reset"></button>
			<button data-ref="install" type="button" class="pencode-settings-reset"></button>
			<button data-ref="uninstall" type="button" class="pencode-settings-reset"></button>
		</div>
		<div data-ref="feedback" class="pencode-language-feedback" role="status" aria-live="polite" hidden></div>
		<div data-ref="path-row" class="pencode-language-path" hidden><label data-ref="label"></label><input data-ref="path" type="text" class="pencode-setting-input"></div>
	</div>`
	return content.Define[languageToolProps, bool](htmlui.NewTemplate(template), "", module.ContentBehavior[languageToolProps, bool]{
		Render: func(c module.ContentRenderContext[languageToolProps, bool], v *htmlui.View) {
			p := c.Props
			m, tool := p.Model, p.Tool
			v.Text("name", tool.Label)
			v.Text("feedback", p.Feedback)
			setPanelVisible(v, "feedback", p.Feedback != "")
			v.Attr("arrow", "src", m.themedIcon("icons/chevron-right.svg", "icon.foreground"))
			v.Attr("details", "title", m.tr("可执行文件路径"))
			v.Attr("details", "aria-expanded", boolAttribute(*c.State))
			v.Attr("details", "aria-controls", "language-tool-path-row-"+tool.ID)
			v.Attr("path-row", "id", "language-tool-path-row-"+tool.ID)
			setPanelVisible(v, "path-row", *c.State)
			status := m.tr("未安装") + " · " + tool.Manager + " · " + tool.Package
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
				status = m.tr(source) + " · " + path
			} else if p.Info.Problem != "" {
				status = p.Info.Problem
			}
			v.Text("status", status)
			v.Attr("status", "title", status)
			v.Text("label", m.tr("可执行文件路径"))
			v.Attr("label", "for", "language-tool-path-"+tool.ID)
			v.Attr("path", "id", "language-tool-path-"+tool.ID)
			v.Attr("path", "value", p.Override)
			v.Attr("path", "placeholder", m.tr("留空以自动检测"))
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
			v.Text("activate", m.tr(activate))
			v.Attr("activate", "aria-pressed", boolAttribute(p.Active))
			install := "安装"
			if p.Info.Managed {
				install = "更新"
			} else if p.Info.Command != "" {
				install = "安装托管副本"
			}
			v.Text("install", m.tr(install))
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
				if m.settings.ServerPaths[id] == path {
					return
				}
				if path == "" {
					delete(m.settings.ServerPaths, id)
				} else {
					if m.settings.ServerPaths == nil {
						m.settings.ServerPaths = make(map[string]string)
					}
					m.settings.ServerPaths[id] = path
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
	v.Event("settings-language-servers-nav", htmlui.EventClick)
	v.Attr("settings-language-servers-nav", "class", "pencode-settings-nav-item")
	v.RemoveAttr("settings-language-servers-nav", "aria-current")
	setPanelVisible(v, "settings-language-servers-pane", m.settingsLanguageServers)
	setPanelVisible(v, "language-tools-refresh", m.settingsLanguageServers)
	if !m.settingsLanguageServers {
		return
	}
	v.Text("settings-page-title", m.tr("语言服务器"))
	v.Attr("settings-language-servers-nav", "class", "pencode-settings-nav-item active")
	v.Attr("settings-language-servers-nav", "aria-current", "page")
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
	for i := range languagetool.Languages {
		lang := &languagetool.Languages[i]
		active := m.activeTool(lang)
		expanded := state.expanded == lang
		v.AppendComponentChild("language-list", uint64(i+1), choice, languageChoiceProps{m, lang, languagetool.Find(active), expanded}, struct{}{})
		if expanded {
			for _, id := range lang.Tools {
				props := languageToolProps{Model: m, Language: lang, Tool: languagetool.Find(id), Locale: m.settings.Language, Override: m.settings.ServerPaths[id], Info: state.items[id], Active: active == id, Busy: busy}
				if id == state.feedbackID {
					props.Feedback = state.feedback
				}
				v.AppendComponentChild("language-list", componentKey(id), row, props, false)
			}
		}
	}
}
