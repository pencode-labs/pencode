package workbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/onix-engine/onix/scene"
)

// Session metadata contains no document contents or running process state.
// It is read on project open and written on close, never on the frame path.
type pencodeProjectState struct {
	ExpandedDirs            map[string]bool   `json:"expandedDirs,omitempty"`
	ActivePath              string            `json:"activePath,omitempty"`
	Repository              string            `json:"repository,omitempty"`
	SidebarWidth            string            `json:"sidebarWidth,omitempty"`
	PanelHeight             string            `json:"panelHeight,omitempty"`
	SourceChangesWidth      string            `json:"sourceChangesWidth,omitempty"`
	OutputSource            string            `json:"outputSource,omitempty"`
	OutputFilter            string            `json:"outputFilter,omitempty"`
	Tabs                    []pencodeTabState `json:"tabs,omitempty"`
	TreePosition            float32           `json:"treePosition,omitempty"`
	Panel                   pencodePanel      `json:"panel"`
	SidebarHidden           bool              `json:"sidebarHidden,omitempty"`
	Collapsed               bool              `json:"collapsed,omitempty"`
	AllDirectoriesCollapsed bool              `json:"allDirectoriesCollapsed,omitempty"`
}

type pencodeTabState struct {
	Path       string  `json:"path"`
	Editor     string  `json:"editor,omitempty"`
	Caret      int     `json:"caret,omitempty"`
	Anchor     int     `json:"anchor,omitempty"`
	ScrollX    float32 `json:"scrollX,omitempty"`
	ScrollY    float32 `json:"scrollY,omitempty"`
	ForcedText bool    `json:"forcedText,omitempty"`
	Split      bool    `json:"split,omitempty"`
	Preview    bool    `json:"preview,omitempty"`
}

func (s *Session) projectStateFile(key string) string {
	if s == nil || s.stateDirectory == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return filepath.Join(s.stateDirectory, "projects", hex.EncodeToString(digest[:])+".json")
}

func (m *workspaceModel) saveProjectStates() {
	for _, project := range m.projects {
		m.saveProjectState(project)
	}
}

func (m *workspaceModel) saveProjectState(p *pencodeProject) {
	file := m.session.projectStateFile(p.historyKey)
	if file == "" {
		return
	}
	if m.fontResources != nil {
		m.editor.saveView(m.fontResources, m)
		if !p.tree.restore {
			if scroll := m.fontResources.Ref[scene.UIScroll](p.tree.area); scroll != nil {
				p.tree.position = scroll.Position.Y()
			}
		}
	}
	state := pencodeProjectState{
		ExpandedDirs: p.ExpandedDirs, ActivePath: p.ActivePath, Repository: p.sourceControl.Snapshot.Repository.Root,
		TreePosition: p.tree.position, Collapsed: p.Collapsed, AllDirectoriesCollapsed: p.AllDirectoriesCollapsed,
		SidebarHidden: m.sidebarHidden, SidebarWidth: m.sidebarWidthStyle, PanelHeight: m.bottom.heightStyle,
		SourceChangesWidth: m.sourceChangesWidthStyle, Panel: m.bottom.active, OutputFilter: m.bottom.filter,
	}
	if m.bottom.source != nil {
		state.OutputSource = m.bottom.source.Name
	}
	if p.restoring != nil {
		state.Tabs = slices.Clone(p.restoring.Tabs)
		if state.ActivePath == "" {
			state.ActivePath = p.restoring.ActivePath
		}
	}
	for _, path := range p.Tabs {
		f := p.Files[path]
		if f == nil {
			continue
		}
		tab := pencodeTabState{Path: path, Editor: f.editorID, ForcedText: f.forcedText, Split: f.editorSplit, Preview: f.Preview,
			Caret: f.editorView.caret, Anchor: f.editorView.anchor, ScrollX: f.editorView.scrollX, ScrollY: f.editorView.scrollY}
		if p.restoring != nil {
			if index := slices.IndexFunc(state.Tabs, func(saved pencodeTabState) bool { return saved.Path == path }); index >= 0 {
				state.Tabs[index] = tab
				continue
			}
		}
		state.Tabs = append(state.Tabs, tab)
	}
	if err := writeProjectState(file, &state); err != nil {
		m.logOutput("pencode", "保存项目状态失败："+err.Error())
	}
}

func writeProjectState(name string, state *pencodeProjectState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	directory := filepath.Dir(name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".project-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), name)
}

func (m *workspaceModel) restoreProjectState(p *pencodeProject) {
	file := m.session.projectStateFile(p.historyKey)
	if file == "" {
		return
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return
	}
	state := &pencodeProjectState{}
	if err == nil {
		err = json.Unmarshal(data, state)
	}
	if err != nil {
		m.logOutput("pencode", "读取项目状态失败："+err.Error())
		return
	}
	p.ExpandedDirs, p.Collapsed, p.AllDirectoriesCollapsed = state.ExpandedDirs, state.Collapsed, state.AllDirectoriesCollapsed
	p.tree.position, p.tree.restore = min(0, state.TreePosition), true
	p.tree.revision++
	p.sourceControl.Snapshot.Repository = sourceRepository{Root: state.Repository, Backend: m.backend}
	p.sourceControl.FollowPath = state.ActivePath
	m.sidebarHidden, m.sidebarWidthStyle = state.SidebarHidden, state.SidebarWidth
	m.bottom.heightStyle, m.sourceChangesWidthStyle = state.PanelHeight, state.SourceChangesWidth
	if state.Panel <= pencodePanelDebug && state.Panel != pencodePanelExtension {
		m.bottom.active = state.Panel
	}
	m.bottom.filter = state.OutputFilter
	if state.OutputSource != "" {
		m.bottom.source = m.outputStore().Channel(state.OutputSource)
	}
	m.syncActiveProjectFields()
	if m.bottom.active == pencodePanelTerminal && (m.terminals == nil || m.terminals.active == nil) {
		m.newTerminal()
	}
	if len(state.Tabs) == 0 {
		return
	}
	p.restoring = state
	if err := m.submitFileIO(&fileIOJob{kind: fileRestore, project: p, workspace: p.workspace.ID, restore: state}); err != nil {
		p.restoring = nil
		m.setProjectStatus(p, "恢复标签失败："+err.Error())
	}
}

func (m *workspaceModel) acceptProjectRestore(r *fileIOResult) {
	p, state := r.job.project, r.job.restore
	if p.restoring != state {
		return
	}
	p.restoring = nil
	active := p.ActivePath
	sourceActive, selection := p.sourceControl.Active, p.sourceControl.Selection
	for i := range r.reload {
		item, saved := &r.reload[i], state.Tabs[i]
		if item.err != nil || p.Files[item.relative] != nil {
			continue // Deleted files do not prevent the other tabs from reopening.
		}
		acceptProjectFile(p, item.relative, saved.Preview, item.document, item.content)
		item.document = nil
		f := p.Files[item.relative]
		f.editorID, f.editorSplit = saved.Editor, saved.Split
		f.forcedText = saved.ForcedText || saved.Editor == pencodeTextEditor
		f.editorView = pencodeEditorView{caret: max(0, saved.Caret), anchor: max(0, saved.Anchor), scrollX: max(0, saved.ScrollX), scrollY: max(0, saved.ScrollY)}
	}
	if active != "" {
		p.ActivePath = active
	} else if p.Files[state.ActivePath] != nil {
		p.ActivePath = state.ActivePath
	}
	p.sourceControl.Active, p.sourceControl.Selection = sourceActive, selection
	if active == "" {
		p.sourceControl.FollowPath = p.ActivePath
		if p == m.activeProjectState() {
			// Restoring selection must not expand or recenter the saved tree.
			m.activePath = p.ActivePath
		}
	}
	m.syncActiveProjectFields()
}
