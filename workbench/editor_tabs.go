package workbench

import (
	"iter"
	"path/filepath"
	"slices"

	"github.com/pencode-labs/pencode/peb"
)

type pencodeEditorTab struct {
	project *pencodeProject
	surface *pencodeExtensionSurface
	source  *sourceTab
	path    string
}

// Match the visible strip order: files, extension editors, then comparisons.
func (m *workspaceModel) editorTabs() iter.Seq[pencodeEditorTab] {
	return func(yield func(pencodeEditorTab) bool) {
		project := m.activeProjectState()
		if project != nil {
			for _, path := range project.Tabs {
				if project.Files[path] != nil && !yield(pencodeEditorTab{project: project, path: path}) {
					return
				}
			}
		}
		for _, surface := range m.actions.ui.surfaces {
			if surface.ui.Kind == peb.EditorTab && !yield(pencodeEditorTab{surface: surface}) {
				return
			}
		}
		for _, tab := range m.sourceState().Tabs {
			if !yield(pencodeEditorTab{project: project, source: tab}) {
				return
			}
		}
	}
}

func (tab pencodeEditorTab) dirty() bool {
	if tab.source != nil {
		return tab.source.Result != tab.source.Saved
	}
	return tab.project != nil && tab.project.Files[tab.path] != nil && tab.project.Files[tab.path].Dirty
}

func (m *workspaceModel) editorTabPath(tab pencodeEditorTab) string {
	if tab.source != nil {
		return filepath.Join(m.sourceState().Snapshot.Repository.Root, filepath.FromSlash(tab.source.Path))
	}
	if tab.surface == nil && tab.project != nil {
		return filepath.Join(tab.project.Path, filepath.FromSlash(tab.path))
	}
	return ""
}

func (m *workspaceModel) closeEditorTabs(action pencodeCloseAction, anchor pencodeEditorTab) {
	request := pencodeCloseRequest{action: action, project: m.activeProjectState()}
	right := false
	for tab := range m.editorTabs() {
		if editorTabCloseIncludes(action, tab, anchor, right) {
			request.editorTabs = append(request.editorTabs, tab)
		}
		right = right || tab == anchor
	}
	if len(request.editorTabs) > 0 {
		m.beginEditorClose(request)
	}
}

func editorTabCloseIncludes(action pencodeCloseAction, tab, anchor pencodeEditorTab, right bool) bool {
	switch action {
	case pencodeCloseTab:
		return tab == anchor
	case pencodeCloseOtherTabs:
		return tab != anchor
	case pencodeCloseRightTabs:
		return right
	case pencodeCloseSavedTabs:
		return !tab.dirty()
	case pencodeCloseAllTabs:
		return true
	}
	return false
}

func (m *workspaceModel) beginEditorClose(request pencodeCloseRequest) {
	if m.closing.action != pencodeCloseNone || m.sourceState().modal() || request.project != m.activeProjectState() {
		return
	}
	if m.fileCloseBlocked(request.action, request.project) {
		m.fileClose = request
		m.setActiveStatus(m.tr("正在等待文件操作完成后关闭…"))
		return
	}
	for _, tab := range request.editorTabs {
		if tab.source != nil && m.sourceState().busy() {
			return
		}
		if tab.surface != nil || tab.source != nil || tab.project == nil {
			continue
		}
		file := tab.project.Files[tab.path]
		if document := m.customDocuments[file]; document != nil && document.busy() {
			m.customClose = request
			m.setActiveStatus(m.tr("正在等待编辑工具完成操作…"))
			return
		}
	}
	request.targets = m.closing.targets[:0]
	for _, tab := range request.editorTabs {
		if tab.source == nil && tab.surface == nil && tab.dirty() {
			request.targets = append(request.targets, pencodeCloseTarget{tab.project, tab.project.Files[tab.path]})
		}
	}
	if len(request.targets) == 0 {
		m.finishEditorClose(request.editorTabs)
		return
	}
	m.closing = request
	m.showCloseTarget()
}

func (m *workspaceModel) finishEditorClose(tabs []pencodeEditorTab) {
	for _, tab := range tabs {
		if tab.source != nil && tab.dirty() {
			s := m.sourceState()
			s.Prompt, s.TabClose = "discard-tabs", tabs
			return
		}
	}
	m.discardEditorTabs(tabs)
}

func (m *workspaceModel) discardEditorTabs(tabs []pencodeEditorTab) {
	for _, tab := range slices.Backward(tabs) {
		switch {
		case tab.surface != nil:
			if slices.Contains(m.actions.ui.surfaces, tab.surface) {
				m.closeExtensionSurface(tab.surface, "button", true)
			}
		case tab.source != nil:
			if slices.Contains(m.sourceState().Tabs, tab.source) {
				m.closeSourceTab(tab.source.Key)
			}
		default:
			m.discardProjectTab(tab.project, tab.path)
		}
	}
}
