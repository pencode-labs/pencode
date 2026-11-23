package workbench

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/extensions"
	"github.com/pencode-labs/pencode/internal/languagetool"
	"github.com/pencode-labs/pencode/peb"
)

type pencodeBuiltinTools struct {
	items      map[string]languagetool.Installation
	results    chan pencodeToolResult
	cancel     context.CancelFunc
	expanded   *languagetool.Language
	changing   string
	feedback   string
	feedbackID string
	action     string
}

type pencodeToolResult struct {
	err   error
	items map[string]languagetool.Installation
}

var languageToolActions = map[string]struct{ label, pending, done string }{
	"install":   {"安装", "正在安装 %s…", "已安装 %s"},
	"update":    {"更新", "正在更新 %s…", "已更新 %s"},
	"uninstall": {"卸载", "正在卸载 %s…", "已卸载 %s"},
}

func (m *workspaceModel) reportLanguageTool(id, level, message string) {
	state := m.builtinTools
	state.feedbackID, state.feedback = id, message
	m.logOutput("pencode", level+" "+message)
}

func (m *workspaceModel) startBuiltinTools(action, id string) {
	if !m.backendConnected() {
		return
	}
	state := m.builtinTools
	if state == nil {
		state = &pencodeBuiltinTools{results: make(chan pencodeToolResult, 1)}
		m.builtinTools = state
	}
	if state.cancel != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	state.cancel = cancel
	state.action = action
	if action != "" {
		m.reportLanguageTool(id, "[Info]", fmt.Sprintf(m.tr(languageToolActions[action].pending), languagetool.Find(id).Label))
	} else {
		state.feedbackID, state.feedback = "", m.tr("正在检测语言服务器…")
	}
	var stopped []<-chan struct{}
	// Windows keeps running executables open. Release the managed process
	// before replacing or removing its files, including comparison panes.
	if (action == "update" || action == "uninstall") && m.languages != nil && m.settings.ServerPaths[id] == "" {
		state.changing = id
		m.stopLanguageSessions(func(key pencodeLanguageKey, s *extensions.LanguageServer) bool {
			if s.LocalID != id || !strings.HasPrefix(key.id, "builtin/") {
				return false
			}
			stopped = append(stopped, m.languages.sessions[key].done)
			return true
		})
	}
	backend, paths, wake := m.backend, maps.Clone(m.settings.ServerPaths), m.backgroundWake()
	for id := range paths {
		if languagetool.Find(id) == nil {
			delete(paths, id)
		}
	}
	go func() {
		for _, done := range stopped {
			select {
			case <-done:
			case <-ctx.Done():
			}
		}
		items, err := backend.LanguageTools(ctx, action, id, paths)
		state.results <- pencodeToolResult{err, items}
		if wake != nil {
			wake()
		}
	}()
}

func (m *workspaceModel) pollBuiltinTools() bool {
	state := m.builtinTools
	if state == nil {
		m.startBuiltinTools("", "")
		return false
	}
	if state.cancel == nil {
		return false
	}
	select {
	case result := <-state.results:
		state.cancel()
		state.cancel = nil
		state.changing = ""
		if action := state.action; action != "" {
			state.action = ""
			id := state.feedbackID
			name, labels := languagetool.Find(id).Label, languageToolActions[action]
			if result.err != nil {
				m.reportLanguageTool(id, "[Error]", name+" · "+m.tr(labels.label)+" · "+fmt.Sprintf(m.tr("操作失败：%v"), result.err))
				return true
			}
			m.reportLanguageTool(id, "[Info]", fmt.Sprintf(m.tr(labels.done), name))
		} else if result.err != nil {
			state.feedback = result.err.Error()
			return true
		} else {
			state.feedback = ""
		}
		state.items = result.items
		m.applyBuiltinTools()
		return true
	default:
		return false
	}
}

func (m *workspaceModel) activeTool(lang *languagetool.Language) string {
	preference := m.settings.LanguageServers[lang.ID]
	if m.builtinTools != nil {
		if slices.Contains(lang.Tools, preference) && m.builtinTools.items[preference].Command != "" {
			return preference
		}
		for _, id := range lang.Tools {
			if m.builtinTools.items[id].Command != "" {
				return id
			}
		}
	}
	return ""
}

// Reconcile only affected sessions. Tool discovery and selection never run in
// the per-document, per-frame sync path.
func (m *workspaceModel) applyBuiltinTools() {
	for i := range languagetool.Languages {
		lang := &languagetool.Languages[i]
		id := m.activeTool(lang)
		prefix := "builtin/" + lang.ID + "/"
		var existing *extensions.LanguageServer
		if m.languages != nil {
			for _, s := range m.languages.servers {
				if strings.HasPrefix(s.ID, prefix) {
					existing = s
					break
				}
			}
		}
		if id == "" {
			if existing != nil {
				m.removeLanguageServers(prefix)
			}
			continue
		}
		info, tool := m.builtinTools.items[id], languagetool.Find(id)
		args := tool.Args
		if info.Entry != "" {
			args = append([]string{info.Entry}, args...)
		}
		if existing != nil && existing.LocalID == id && existing.Command == info.Command && slices.Equal(existing.Args, args) {
			m.stopLanguageSessions(func(key pencodeLanguageKey, _ *extensions.LanguageServer) bool {
				return key.id == existing.ID && m.languages.sessions[key].failed
			})
			continue
		}
		if existing != nil {
			m.removeLanguageServers(prefix)
		}
		def := peb.LanguageServer{ID: prefix + id, Label: tool.Label, LanguageID: lang.ID, Selectors: lang.Selectors, Command: info.Command, Args: args}
		if id == "gopls" {
			def.InitializationOptions = `{"semanticTokens":true}`
			def.Configuration = `{"gopls":{"semanticTokens":true,"analyses":{"unusedparams":true},"staticcheck":true,"gofumpt":true}}`
		}
		s := extensions.LanguageServer{LanguageServer: def, LocalID: id}
		if m.languages != nil {
			m.stopLanguageSessions(func(_ pencodeLanguageKey, other *extensions.LanguageServer) bool {
				for _, rule := range other.Selectors {
					if s.Match(rule.Pattern) {
						return true
					}
				}
				for _, rule := range lang.Selectors {
					if other.Match(rule.Pattern) {
						return true
					}
				}
				return false
			})
		}
		m.importLanguageServers([]extensions.LanguageServer{s})
		// Prefer the selected built-in over matching extension contributions.
		servers := m.languages.servers
		last := servers[len(servers)-1]
		copy(servers[1:], servers[:len(servers)-1])
		servers[0] = last
	}
}
