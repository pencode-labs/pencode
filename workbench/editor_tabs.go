package workbench

import (
	"cmp"
	"iter"
	"path/filepath"
	"slices"
	"sync/atomic"

	"github.com/pencode-labs/pencode/peb"
)

type pencodeEditorTab struct {
	project *pencodeProject
	surface *pencodeExtensionSurface
	source  *sourceTab
	path    string
}

// Loose files from different projects can share the same tab strip.
var editorTabSequence atomic.Uint64

func appendFileTab(paths []string, file *openFile) []string {
	file.tabOrder = editorTabSequence.Add(1)
	return append(paths, file.Path)
}

type orderedEditorTab struct {
	tab   pencodeEditorTab
	order uint64
}

func (m *workspaceModel) editorTabs() iter.Seq[pencodeEditorTab] {
	return func(yield func(pencodeEditorTab) bool) {
		project := m.activeProjectState()
		tabs := m.tabScratch[:0]
		m.tabScratch = nil
		defer func() {
			clear(tabs)
			m.tabScratch = tabs[:0]
		}()
		sorted := true
		appendTab := func(tab pencodeEditorTab, order uint64) {
			sorted = sorted && (len(tabs) == 0 || tabs[len(tabs)-1].order <= order)
			tabs = append(tabs, orderedEditorTab{tab, order})
		}
		appendFiles := func(p *pencodeProject, paths []string, files map[string]*openFile) {
			for _, path := range paths {
				if file := files[path]; file != nil {
					appendTab(pencodeEditorTab{project: p, path: path}, file.tabOrder)
				}
			}
		}
		if project == nil {
			appendFiles(nil, m.tabs, m.files)
		}
		for _, p := range m.projects {
			if p == project || p.Loose {
				appendFiles(p, p.Tabs, p.Files)
			}
		}
		for _, surface := range m.actions.ui.surfaces {
			if surface.ui.Kind == peb.EditorTab {
				appendTab(pencodeEditorTab{surface: surface}, surface.tabOrder)
			}
		}
		if !sorted {
			slices.SortStableFunc(tabs, func(a, b orderedEditorTab) int { return cmp.Compare(a.order, b.order) })
		}
		for _, item := range tabs {
			if !yield(item.tab) {
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

func (m *workspaceModel) editorTabNeighbors(anchor pencodeEditorTab, wrap bool) (left, right pencodeEditorTab) {
	var first, last pencodeEditorTab
	for tab := range m.editorTabs() {
		if first == (pencodeEditorTab{}) {
			first = tab
		}
		if tab == anchor {
			left = last
		} else if last == anchor {
			right = tab
			if !wrap || first != anchor {
				return
			}
		}
		last = tab
	}
	if wrap && first == anchor {
		left = last
	}
	if wrap && last == anchor {
		right = first
	}
	return
}

func (m *workspaceModel) selectEditorTabNeighbor(closed pencodeEditorTab) {
	left, right := m.editorTabNeighbors(closed, false)
	if right == (pencodeEditorTab{}) {
		right = left
	}
	m.selectEditorTab(right)
}

func (m *workspaceModel) selectEditorTab(tab pencodeEditorTab) {
	switch {
	case tab.surface != nil:
		m.selectExtensionTab(tab.surface)
	case tab.source != nil:
		if current := m.actions.ui.editor; current != nil {
			current.saveScrollPositions()
		}
		m.actions.ui.editor = nil
		s := m.sourceState()
		s.Active, s.RestoreScroll = tab.source.Key, true
		s.Selection++
	case tab.project != nil:
		m.selectProjectTab(tab.project, tab.path)
	case tab.path != "":
		m.selectTab(tab.path)
	default:
		m.actions.ui.editor = nil
	}
}

func (tab pencodeEditorTab) file(m *workspaceModel) *openFile {
	if tab.project != nil {
		return tab.project.Files[tab.path]
	}
	return m.files[tab.path]
}

func (tab pencodeEditorTab) dirty(m *workspaceModel) bool {
	if tab.source != nil {
		return tab.source.Result != tab.source.Saved
	}
	file := tab.file(m)
	return file != nil && file.Dirty
}

func (m *workspaceModel) sourceTabRoot(tab *sourceTab) string {
	if tab.Kind == "local-history" {
		return m.projectPath
	}
	return m.sourceState().Snapshot.Repository.Root
}

func (m *workspaceModel) editorTabPath(tab pencodeEditorTab) string {
	if tab.source != nil {
		return filepath.Join(m.sourceTabRoot(tab.source), filepath.FromSlash(tab.source.Path))
	}
	if tab.path == "" {
		return ""
	}
	root := m.projectPath
	if tab.project != nil {
		root = tab.project.Path
	}
	return filepath.Join(root, filepath.FromSlash(tab.path))
}

func (m *workspaceModel) closeEditorTabs(action pencodeCloseAction, anchor pencodeEditorTab) {
	request := pencodeCloseRequest{action: action, project: m.activeProjectState()}
	right := false
	for tab := range m.editorTabs() {
		if m.editorTabCloseIncludes(action, tab, anchor, right) {
			request.editorTabs = append(request.editorTabs, tab)
		}
		right = right || tab == anchor
	}
	if len(request.editorTabs) > 0 {
		m.beginEditorClose(request)
	}
}

func (m *workspaceModel) editorTabCloseIncludes(action pencodeCloseAction, tab, anchor pencodeEditorTab, right bool) bool {
	switch action {
	case pencodeCloseTab:
		return tab == anchor
	case pencodeCloseOtherTabs:
		return tab != anchor
	case pencodeCloseRightTabs:
		return right
	case pencodeCloseSavedTabs:
		return !tab.dirty(m)
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
		if tab.surface != nil || tab.source != nil {
			continue
		}
		file := tab.file(m)
		if document := m.customDocuments[file]; document != nil && document.busy() {
			m.customClose = request
			m.setActiveStatus(m.tr("正在等待编辑工具完成操作…"))
			return
		}
	}
	request.targets = m.closing.targets[:0]
	for _, tab := range request.editorTabs {
		if tab.source == nil && tab.surface == nil && tab.dirty(m) {
			request.targets = append(request.targets, pencodeCloseTarget{tab.project, tab.file(m)})
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
		if tab.source != nil && tab.dirty(m) {
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
			if slices.Contains(m.projectActions(tab.surface.project).ui.surfaces, tab.surface) {
				m.closeExtensionSurface(tab.surface, "button", true)
			}
		case tab.source != nil:
			if slices.Contains(m.sourceState().Tabs, tab.source) {
				m.closeSourceTab(tab.source.Key)
			}
		case tab.project == nil:
			m.discardTab(tab.path)
		default:
			m.discardProjectTab(tab.project, tab.path)
		}
	}
}
